package ui

// TestSiteCapture renders tie-audio's real UI through the headless Fyne driver
// for the tiedb.com screenshot pipeline. It needs a running tie test-env with
// albums tagged "demo" and a tie config; both are provided by the site scripts.
//
//	SITE_CAPTURE_DIR=/tmp/shots \
//	SITE_TIE_CONFIG=/tmp/env/site-tie.toml \
//	TIE_THEME=dark \
//	go test ./cmd/tie-audio/internal/ui -run TestSiteCapture -count=1
//
// Captures are rendered at 2x.

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/tiestyle"
)

func TestSiteCapture(t *testing.T) {
	out := os.Getenv("SITE_CAPTURE_DIR")
	if out == "" {
		t.Skip("set SITE_CAPTURE_DIR to render site captures")
	}
	tieCfg := os.Getenv("SITE_TIE_CONFIG")
	if tieCfg == "" {
		t.Skip("set SITE_TIE_CONFIG to a tie client config for the demo server")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FILESDIR", t.TempDir())
	newTestApp(t)
	tiestyle.Apply(fyne.CurrentApp())

	cfg := config.Default()
	cfg.TieConfig = tieCfg
	cfg.Backend = config.BackendPwplay
	cfg.Layout = config.LayoutRegular
	cfg.StartupPage = config.StartupHome
	cfg.BrowseView = config.BrowseCovers
	session := data.NewSession(cfg)
	session.Backend = &statusErrBackend{}

	albums, err := session.QueryAlbums([]string{"demo"}, nil)
	if err != nil {
		t.Fatalf("query demo albums: %v", err)
	}
	if len(albums) < 4 {
		t.Fatalf("need at least 4 demo albums, got %d", len(albums))
	}
	seedPlays(t, session, albums)

	win := test.NewWindow(nil)
	a := NewApp(win, session)
	a.player.Stop()
	win.SetContent(a.Root())
	win.Resize(fyne.NewSize(1440, 900))

	// A marketing shot wants the library, not the empty queue pane: drop it
	// and let the cover wall use the width. The tag sidebar stays, because it
	// is part of how the app is used.
	a.shell.queuePanel = nil
	a.shell.dropSplit()
	a.shell.rewrap()
	if c, ok := win.Canvas().(interface{ SetScale(float32) }); ok {
		c.SetScale(2)
	}

	waitForCond(t, "home shelves", func() bool {
		return a.browse.feed == feedHome && a.browse.home != nil && len(a.browse.home.home.Shelves) > 0
	})
	sleepUI(4 * time.Second) // let covers decode and land
	win.Content().Refresh()
	captureSite(t, win, filepath.Join(out, "home-"+captureTheme()+".png"))

	a.navAlbums()
	waitForCond(t, "album wall", func() bool { return a.browse.feed == feedLatest && len(a.browse.albums) >= 4 })
	sleepUI(4 * time.Second)
	captureSite(t, win, filepath.Join(out, "wall-"+captureTheme()+".png"))

	// Open the first album on the wall, then show the track list.
	a.browse.openAlbum(a.browse.albums[0])
	waitForCond(t, "album open", func() bool { return a.browse.albumOpen })
	sleepUI(3 * time.Second)
	captureSite(t, win, filepath.Join(out, "album-"+captureTheme()+".png"))

	// The compact (phone) home gets its own app so the shell is built in
	// compact mode from the start.
	ccfg := config.Default()
	ccfg.TieConfig = tieCfg
	ccfg.Backend = config.BackendPwplay
	ccfg.Layout = config.LayoutCompact
	ccfg.StartupPage = config.StartupHome
	ccfg.BrowseView = config.BrowseCovers
	csession := data.NewSession(ccfg)
	csession.Backend = &statusErrBackend{}
	cwin := test.NewWindow(nil)
	ca := NewApp(cwin, csession)
	ca.player.Stop()
	cwin.SetContent(ca.Root())
	cwin.Resize(fyne.NewSize(420, 860))
	if c, ok := cwin.Canvas().(interface{ SetScale(float32) }); ok {
		c.SetScale(2)
	}
	waitForCond(t, "compact home", func() bool {
		return ca.browse.feed == feedHome && ca.browse.home != nil && len(ca.browse.home.home.Shelves) > 0
	})
	sleepUI(4 * time.Second)
	captureSite(t, cwin, filepath.Join(out, "compact-"+captureTheme()+".png"))
}

func captureTheme() string {
	if v := os.Getenv("TIE_THEME"); v != "" {
		return v
	}
	return "dark"
}

// seedPlays spreads play events over hours, days and a year so the Home
// shelves (jump back in, recently played, haven't heard in a while, on this
// day) all have something to show.
func seedPlays(t *testing.T, session *data.Session, albums []data.Album) {
	t.Helper()
	offsets := []time.Duration{
		-35 * time.Minute,
		-2 * time.Hour,
		-26 * time.Hour,
		-3 * 24 * time.Hour,
		-9 * 24 * time.Hour,
		-40 * 24 * time.Hour,
		-160 * 24 * time.Hour,
		-370 * 24 * time.Hour,
	}
	var events []tieclient.PlayEvent
	for i, a := range albums {
		if i >= len(offsets) {
			break
		}
		tracks, err := session.AlbumTracks(a)
		if err != nil || len(tracks) == 0 {
			continue
		}
		events = append(events, tieclient.PlayEvent{
			Source:     "site-capture",
			Seq:        uint64(time.Now().UnixNano()) + uint64(i),
			Subject:    tracks[0].Hash,
			Collection: a.UID,
			PlayedAt:   time.Now().Add(offsets[i]),
			Listened:   180,
			Duration:   200,
			Outcome:    tieclient.OutcomeComplete,
		})
	}
	if _, err := session.Tie.RecordPlays(events); err != nil {
		t.Fatalf("record plays: %v", err)
	}
}

func captureSite(t *testing.T, win fyne.Window, path string) {
	t.Helper()
	pumpUI()
	win.Content().Refresh()
	sleepUI(250 * time.Millisecond)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("capture panicked: %v", r)
			}
		}()
		img := win.Canvas().Capture()
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}()
	fmt.Println("wrote", path)
}
