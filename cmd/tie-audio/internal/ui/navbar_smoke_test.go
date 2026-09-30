package ui

// Live integration test for the compact shell's persistent bottom panel:
// builds the real App in the compact layout against the tie test-env and
// walks the nav bar's destinations, checking what each view pins at the
// bottom, which tab is highlighted, and the mini bar cover routing. Skipped
// unless the test-env runs (see walltable_smoke_test.go).

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// statusErrBackend is a fakeBackend whose status poll always fails.
type statusErrBackend struct{ fakeBackend }

func (*statusErrBackend) Status() (playback.Status, error) {
	return playback.Status{}, errors.New("no backend in this test")
}

func TestCompactNavLiveIntegration(t *testing.T) {
	requireSmokeEnv(t)
	t.Setenv("FILESDIR", t.TempDir())
	test.NewApp()

	cfg := config.Default()
	cfg.TieConfig = smokeTieConfig(t)
	cfg.Backend = config.BackendPwplay
	cfg.Layout = config.LayoutCompact
	cfg.BrowseView = config.BrowseCovers
	cfg.StartupPage = config.StartupTag
	cfg.StartupTag = smokeTag
	session := data.NewSession(cfg)
	ensureSmokeAlbums(t, session)
	// No live playback: the test driver runs fyne.Do inline, so status polls
	// landing from background goroutines would shape text concurrently with
	// this goroutine. A backend whose Status fails keeps every poll inert.
	session.Backend = &statusErrBackend{}

	win := test.NewWindow(nil)
	a := NewApp(win, session)
	a.player.Stop()
	win.SetContent(a.Root())
	win.Resize(fyne.NewSize(400, 800))

	waitForCond(t, "smoketest albums on the wall", func() bool {
		return len(a.browse.albums) >= 2
	})

	if !a.compact {
		t.Fatal("layout preference compact did not select the compact layout")
	}

	// The gallery's own bar drops the duplicate drawer button and ☰ (the nav
	// bar has both), and on a single-page wall disappears entirely.
	if !a.browse.viewer.HideSidebarToggle || !a.browse.viewer.HideMenuButton {
		t.Error("compact gallery still shows its sidebar toggle / ☰ button")
	}
	if a.browse.viewer.BottomBarVisible() {
		t.Error("single-page compact wall still shows the gallery bottom bar")
	}

	// expect asserts the bottom panel and the highlighted tab for a view.
	expect := func(what string, view appView, tab navTab, mini bool) {
		t.Helper()
		if a.view != view {
			t.Errorf("%s: view = %v, want %v", what, a.view, view)
		}
		if a.nav.active != tab {
			t.Errorf("%s: active tab = %v, want %v", what, a.nav.active, tab)
		}
		content := win.Content()
		if !contains(content, a.nav.Object()) {
			t.Errorf("%s: nav bar not on screen", what)
		}
		if got := contains(content, a.mini.Object()); got != mini {
			t.Errorf("%s: mini bar on screen = %v, want %v", what, got, mini)
		}
		if mini && !a.mini.volRow.Visible() {
			t.Errorf("%s: mini bar volume row hidden", what)
		}
	}

	expect("wall", viewBrowse, tabAlbums, true)
	if a.nav.buttons[tabMenu].disabled {
		t.Error("☰ slot disabled on the wall")
	}

	// Tags opens the drawer on its Tags page; tapping again closes it.
	a.browse.showSettingsTab()
	a.navTags()
	if !a.browse.sidebarOpen() {
		t.Fatal("Tags tab did not open the drawer")
	}
	if a.browse.tabs.SelectedIndex() != 0 {
		t.Errorf("drawer opened on tab %d, want Tags (0)", a.browse.tabs.SelectedIndex())
	}
	expect("drawer", viewBrowse, tabTags, true)
	a.navTags()
	if a.browse.sidebarOpen() {
		t.Error("second Tags tap did not close the drawer")
	}

	// The playlist keeps the mini bar and the nav bar; its edge strips page
	// between tabs.
	a.showQueueView()
	expect("playlist", viewQueue, tabPlaylist, true)
	if a.edgeOverlay() == nil {
		t.Error("playlist view has no edge-swipe strips")
	}
	if !a.nav.buttons[tabMenu].disabled {
		t.Error("☰ slot enabled on the playlist, want disabled")
	}

	// Cover tap on the playlist → Now Playing (nav only, no mini bar).
	a.onMiniCover()
	expect("now playing", viewNowPlaying, tabNone, false)

	// Albums from Now Playing, then a cover tap from the wall → playlist.
	a.navAlbums()
	expect("albums tab", viewBrowse, tabAlbums, true)
	if a.edgeOverlay() != nil {
		t.Error("cover wall got edge strips; the gallery's own swipe overlay handles it")
	}
	a.onMiniCover()
	expect("cover → playlist", viewQueue, tabPlaylist, true)

	a.showSettingsView()
	expect("settings", viewSettings, tabSettings, true)

	// An open album belongs to the Albums tab and gets a back edge strip.
	a.navAlbums()
	a.browse.openAlbum(a.browse.albums[0])
	waitForCond(t, "album view opens", func() bool { return a.browse.albumOpen })
	expect("album view", viewBrowse, tabAlbums, true)
	if a.edgeOverlay() == nil {
		t.Error("album view has no back edge strip")
	}
	a.navAlbums()
	if a.browse.albumOpen {
		t.Error("Albums tab did not close the open album")
	}

	// Clearing the tag selection must not leave an empty wall: it falls back
	// to the latest albums (every album, newest first).
	a.browse.ts.RemoveSelected(smokeTag)
	waitForCond(t, "latest albums after clearing the tags", func() bool {
		return a.browse.feed == feedLatest && len(a.browse.albums) >= 2
	})
	// The ☰ "Latest albums" item gets there from a tag wall too.
	a.browse.ts.SetSelected([]string{smokeTag})
	a.browse.refreshAlbums([]string{smokeTag}, nil)
	waitForCond(t, "tag wall", func() bool { return a.browse.feed == feedTags })
	a.browse.showLatest()
	if in, ex := a.browse.ts.SelectedTags(); len(in)+len(ex) != 0 {
		t.Errorf("showLatest left a selection: %v / %v", in, ex)
	}
	waitForCond(t, "latest albums from the menu", func() bool {
		return a.browse.feed == feedLatest && len(a.browse.albums) >= 2
	})
}

// The nav bar highlights exactly one destination (never the ☰ slot), and the
// Playlist badge follows the polled queue length.
func TestNavBarActiveAndBadge(t *testing.T) {
	test.NewApp()
	n := newNavBar(navActions{})
	n.setActive(tabPlaylist)
	for i, b := range n.buttons {
		if b.active != (navTab(i) == tabPlaylist) {
			t.Errorf("button %d active = %v", i, b.active)
		}
	}
	n.setActive(tabMenu)
	if n.buttons[tabMenu].active {
		t.Error("☰ slot highlighted")
	}
	n.setActive(tabNone)
	for i, b := range n.buttons {
		if b.active {
			t.Errorf("button %d active with tabNone", i)
		}
	}
	n.apply(transportState{queueLen: 7})
	if n.buttons[tabPlaylist].badge != 7 {
		t.Errorf("badge = %d, want 7", n.buttons[tabPlaylist].badge)
	}
	n.setMenuEnabled(false)
	called := false
	n.buttons[tabMenu].onTap = func() { called = true }
	n.buttons[tabMenu].Tapped(nil)
	if called {
		t.Error("disabled ☰ slot fired")
	}
}

func init() { enableMPRIS = false }
