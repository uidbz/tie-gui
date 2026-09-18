package ui

// Live integration test for the browse wall's table view: builds the real App
// against the tie test-env with the wall in table mode and exercises the view
// end to end (feed, sort, open album, back, toggle both ways). Skipped unless
// the test-env runs (start it with ../tie/test-env/start.sh); the smoke
// albums are imported on first run.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/widget/tablewidget"
)

// smokeTag tags the albums the table-view integration test browses.
const smokeTag = "smoketest"

// smokeTieConfig mirrors the tie repo's test-env (triplestore :2161, filehost
// :2162, namespace Collections, collection Main), written to a temp file so
// AppConfig.TieConfig can point at it. DefaultCollection is what
// NewTieClient binds, so it must be set (Collection alone is the fallback).
func smokeTieConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tie.toml")
	content := `Username = "defaultuser"
Password = "defaultpassword"
Namespace = "Collections"
Collection = "Main"
DefaultCollection = "Main"
TripleStoreURL = "http://localhost:2161"
DefaultFileHosts = ["default"]
[FileHosts.default]
URL = "http://localhost:2162"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// requireSmokeEnv skips unless the tie test-env is running locally.
func requireSmokeEnv(t *testing.T) {
	t.Helper()
	resp, err := http.DefaultClient.Head("http://localhost:2162/" + strings.Repeat("0", 64))
	if err != nil {
		t.Skipf("tie test-env not running (start it with ../tie/test-env/start.sh): %v", err)
	}
	resp.Body.Close()
}

// ensureSmokeAlbums imports the fixture albums tagged smokeTag when the
// test-env does not have at least two yet (first run against a fresh env).
func ensureSmokeAlbums(t *testing.T, s *data.Session) {
	t.Helper()
	albums, err := s.QueryAlbums([]string{smokeTag}, nil)
	if err == nil && len(albums) >= 2 {
		return
	}
	src := filepath.Join("..", "data", "testdata")
	root := t.TempDir()
	one := filepath.Join(root, "Smoke Album One")
	two := filepath.Join(root, "Smoke Album Two")
	if err := os.MkdirAll(one, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(two, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01 - Track 1.flac", "02 - Track 2.flac", "03 - Track 3.flac", "cover.jpg"} {
		blob, err := os.ReadFile(filepath.Join(src, "archive-src", name))
		if err != nil {
			t.Skip("archive fixtures not present:", err)
		}
		if err := os.WriteFile(filepath.Join(one, name), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cover, err := os.ReadFile(filepath.Join(src, "track-with-cover.flac"))
	if err != nil {
		t.Skip("cover fixture not present:", err)
	}
	for _, name := range []string{"01 - Track 1.flac", "02 - Track 2.flac"} {
		if err := os.WriteFile(filepath.Join(two, name), cover, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	host, err := s.Tie.ResolveHost("")
	if err != nil {
		t.Fatal("smoke album import:", err)
	}
	for _, dir := range []string{one, two} {
		if err := s.Tie.ImportDir(dir, host, s.Tie.Config.DefaultCollection, "audio-dir", []string{smokeTag}, "", 0); err != nil {
			t.Fatal("smoke album import:", err)
		}
	}
}

func TestWallTableLiveIntegration(t *testing.T) {
	requireSmokeEnv(t)
	// Keep config.Save (view toggle) inside a temp dir, never the real HOME.
	t.Setenv("FILESDIR", t.TempDir())
	test.NewApp()

	cfg := config.Default()
	cfg.TieConfig = smokeTieConfig(t)
	cfg.Backend = config.BackendPwplay
	cfg.BrowseView = config.BrowseTable
	cfg.StartupPage = config.StartupTag
	cfg.StartupTag = smokeTag
	session := data.NewSession(cfg)
	ensureSmokeAlbums(t, session)

	win := test.NewWindow(nil)
	a := NewApp(win, session)
	win.SetContent(a.Root())
	win.Resize(fyne.NewSize(1200, 800))

	// The startup tag feed is async; wait for the smoke albums to land.
	waitForCond(t, "smoketest albums on the wall", func() bool {
		return len(a.browse.albums) >= 2
	})
	waitForCond(t, "wall table fed", func() bool {
		return a.browse.wallTable != nil && a.browse.wallTable.table.RowCount() == len(a.browse.albums)
	})

	// The window content must contain the wall album table (not the gallery
	// grid). The queue pane has its own FlexTable, so match the wall table's
	// by identity.
	wallFT := a.browse.wallTable.table.GetFlexTable()
	var walk func(o fyne.CanvasObject) bool
	walk = func(o fyne.CanvasObject) bool {
		if f, ok := o.(*tablewidget.FlexTable); ok && f == wallFT {
			return true
		}
		switch v := o.(type) {
		case *fyne.Container:
			for _, c := range v.Objects {
				if walk(c) {
					return true
				}
			}
		case *container.Split:
			return walk(v.Leading) || walk(v.Trailing)
		}
		return false
	}
	content := win.Content()
	if !walk(content) {
		t.Fatal("window content has no wall FlexTable in table mode")
	}
	// The Art column's cell text is the album UID; the displayed rows must
	// match the wall's album slice (the fixture albums share one embedded
	// album tag, so titles cannot tell them apart).
	for i, al := range a.browse.albums {
		if got := wallFT.CellText(0, i); got != al.UID {
			t.Errorf("row %d UID = %q, want album %q", i, got, al.UID)
		}
	}
	if wallFT.CellText(1, 0) == "" {
		t.Error("title column of first row is empty")
	}

	// The regular layout keeps the sidebar as a split pane beside the table.
	if findSplit(content) == nil {
		t.Error("table mode (regular) lost the sidebar split")
	}

	// Sort by title via the header hook: the displayed rows must follow the
	// table's own (re-sorted) album slice.
	a.browse.wallTable.onSort("Title", false)
	if got := wallFT.CellText(0, 0); got != a.browse.wallTable.albums[0].UID {
		t.Errorf("after sort, row 0 UID = %q, want %q", got, a.browse.wallTable.albums[0].UID)
	}

	// Row activation opens the album track list of the displayed row's album.
	want := a.browse.wallTable.albums[0]
	a.browse.wallTable.table.OnRowActivated(0)
	waitForCond(t, "album view opens from the table", func() bool {
		return a.browse.albumOpen
	})
	if a.browse.album.UID != want.UID {
		t.Errorf("opened album = %q, want %q", a.browse.album.UID, want.UID)
	}

	// Back returns to the table (not the cover grid).
	a.browse.showBrowse()
	if !walk(win.Content()) {
		t.Error("Back from the album view did not restore the table")
	}

	// Toggling back to covers restores the gallery grid (skip config.Save).
	a.session.Cfg.BrowseView = config.BrowseCovers
	a.browse.showWall()
	if walk(win.Content()) {
		t.Error("cover mode still shows the wall FlexTable")
	}
	if !contains(win.Content(), a.browse.viewer.Content) {
		t.Error("cover mode did not restore the gallery content")
	}

	// The table view's own button row carries the way back to the cover grid.
	a.session.Cfg.BrowseView = config.BrowseTable
	a.browse.showWall()
	var coverBtn *widget.Button
	var findBtn func(o fyne.CanvasObject) bool
	findBtn = func(o fyne.CanvasObject) bool {
		if b, ok := o.(*widget.Button); ok && b.Text == "Cover view" {
			coverBtn = b
			return true
		}
		switch v := o.(type) {
		case *fyne.Container:
			for _, ch := range v.Objects {
				if findBtn(ch) {
					return true
				}
			}
		case *container.Split:
			return findBtn(v.Leading) || findBtn(v.Trailing)
		}
		return false
	}
	if !findBtn(win.Content()) {
		t.Error("table view has no Cover view button")
	} else {
		coverBtn.Tapped(&fyne.PointEvent{})
		if a.session.Cfg.BrowseView != config.BrowseCovers {
			t.Error("Cover view button did not toggle the mode")
		}
	}
}
