package ui

// Live integration test for the Home page: records plays of the smoke
// albums in the tie test-env, builds the real App with the Home startup page
// and checks the shelves, tile navigation (open album, back to Home), the
// nav bar highlight, and the switch to the album wall. Skipped unless the
// test-env runs. A headless capture is written to $TMPDIR for eyeballing.

import (
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
)

func TestHomeLiveIntegration(t *testing.T) {
	requireSmokeEnv(t)
	t.Setenv("FILESDIR", t.TempDir())
	newTestApp(t)

	cfg := config.Default()
	cfg.TieConfig = smokeTieConfig(t)
	cfg.Backend = config.BackendPwplay
	cfg.Layout = config.LayoutCompact
	cfg.StartupPage = config.StartupHome
	session := data.NewSession(cfg)
	ensureSmokeAlbums(t, session)
	session.Backend = &statusErrBackend{}

	albums, err := session.QueryAlbums([]string{smokeTag}, nil)
	if err != nil || len(albums) < 2 {
		t.Fatalf("smoke albums: %v %v", albums, err)
	}
	// One fresh complete play per smoke album, so "Jump back in" has them.
	var events []tieclient.PlayEvent
	for i, a := range albums[:2] {
		tracks, err := session.AlbumTracks(a)
		if err != nil || len(tracks) == 0 {
			t.Fatalf("tracks of %s: %v", a.UID, err)
		}
		events = append(events, tieclient.PlayEvent{
			Source: "home-test", Seq: uint64(time.Now().UnixNano()) + uint64(i),
			Subject: tracks[0].Hash, Collection: a.UID,
			PlayedAt: time.Now().Add(-time.Duration(i+1) * time.Minute),
			Listened: 200, Duration: 200, Outcome: tieclient.OutcomeComplete,
		})
	}
	if _, err := session.Tie.RecordPlays(events); err != nil {
		t.Fatal(err)
	}

	win := test.NewWindow(nil)
	a := NewApp(win, session)
	a.player.Stop()
	win.SetContent(a.Root())
	win.Resize(fyne.NewSize(400, 800))

	waitForCond(t, "home feed", func() bool { return a.browse.feed == feedHome && a.browse.home != nil })
	waitForCond(t, "home shelves", func() bool { return len(a.browse.home.home.Shelves) > 0 })
	jb := a.browse.home.home.Shelves[0]
	if jb.ID != data.ShelfJumpBackIn || len(jb.Albums) < 2 || jb.Albums[0].UID != albums[0].UID {
		t.Fatalf("first shelf = %s with %d albums", jb.ID, len(jb.Albums))
	}
	if got := a.currentTab(); got != tabHome {
		t.Errorf("tab on Home = %v, want tabHome", got)
	}

	if dir := os.TempDir(); dir != "" {
		if f, err := os.Create(filepath.Join(dir, "tie-audio-home.png")); err == nil {
			_ = png.Encode(f, win.Canvas().Capture())
			f.Close()
		}
	}

	// Open an album from a shelf; Back returns to Home without a rebuild.
	built := a.browse.home.built
	a.browse.openAlbum(jb.Albums[0])
	waitForCond(t, "album view", func() bool { return a.browse.albumOpen })
	a.showBrowseView()
	if a.browse.feed != feedHome || a.browse.albumOpen || a.browse.home.built != built {
		t.Errorf("back from album: feed %v open %v rebuilt %v", a.browse.feed, a.browse.albumOpen, a.browse.home.built != built)
	}

	// Albums tab from Home: the album wall.
	a.navAlbums()
	waitForCond(t, "latest wall", func() bool { return a.browse.feed == feedLatest && len(a.browse.albums) >= 2 })
	if got := a.currentTab(); got != tabAlbums {
		t.Errorf("tab on wall = %v, want tabAlbums", got)
	}
	a.navHome()
	if a.browse.feed != feedHome {
		t.Error("Home tab did not return to Home")
	}
}
