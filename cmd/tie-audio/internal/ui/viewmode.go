package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
)

// nowPlayingMode reports whether the desktop browse wall is showing the large
// now-playing cover instead of the album grid/table. The compact layout has
// its own full-screen Now Playing page, so it always keeps the album views.
func (b *browsePage) nowPlayingMode() bool {
	return !b.compact && b.session.Cfg.BrowseView == config.BrowseNowPlaying
}

// ensureViewTabs builds the desktop view selector on first use.
func (b *browsePage) ensureViewTabs() *browseViewTabs {
	if b.viewTabs == nil {
		b.viewTabs = newBrowseViewTabs(b.setBrowseView)
	}
	return b.viewTabs
}

// decorateContent wraps a browse view with the Covers/Table/Now Playing tabs.
// The shell applies this to the main pane (not the queue pane), including
// content the gallery pushes to the window itself.
func (b *browsePage) decorateContent(o fyne.CanvasObject) fyne.CanvasObject {
	if b.compact || b.feed == feedHome {
		return o
	}
	tabs := b.ensureViewTabs()
	tabs.setActive(b.session.Cfg.BrowseView)
	head := container.NewVBox(tabs.object, b.ensureSearchBar())
	return container.NewBorder(head, nil, nil, nil, o)
}

// setViewTabsEnabled turns the desktop view tabs on for browse content and off
// for the App's other full-screen views (queue, settings, compact Now
// Playing). The compact layout and Home use the nav bar instead.
func (b *browsePage) setViewTabsEnabled(on bool) {
	if b.shell == nil {
		return
	}
	if on && !b.compact && b.feed != feedHome {
		b.shell.SetContentDecorator(b.decorateContent)
		return
	}
	b.shell.SetContentDecorator(nil)
}

// syncViewTabs applies the tab visibility for the current content. The App
// calls it once the startup page has been chosen, before the first render.
func (b *browsePage) syncViewTabs() { b.setViewTabsEnabled(true) }

// setBrowseView switches the wall rendering and persists the choice. It is
// bound to the desktop view tabs and the gallery ☰ menu.
func (b *browsePage) setBrowseView(view string) {
	if view != config.BrowseCovers && view != config.BrowseTable && view != config.BrowseNowPlaying {
		return
	}
	if b.session.Cfg.BrowseView == view {
		if b.feed != feedHome {
			b.showWall()
		}
		return
	}
	b.session.Cfg.BrowseView = view
	if err := config.Save(b.session.Cfg); err != nil {
		dialog.ShowError(err, b.win)
	}
	b.albumOpen = false
	b.albumTable = nil
	b.albumPlaying = nil
	if b.feed == feedHome {
		// An explicit view choice leaves the Home page for the album wall.
		b.showLatest()
		return
	}
	b.showWall()
}

// showNowPlayingView shows the large cover of the current track.
func (b *browsePage) showNowPlayingView() {
	b.setViewTabsEnabled(true)
	b.win.SetContent(b.nowPlayingObject())
	if b.viewTabs != nil {
		b.viewTabs.setActive(config.BrowseNowPlaying)
	}
}

// nowPlayingObject builds the desktop Now Playing view on first use and
// registers it with the player (when it is already wired).
func (b *browsePage) nowPlayingObject() fyne.CanvasObject {
	if b.desktopPlaying == nil {
		b.desktopPlaying = newDesktopNowPlaying()
		if b.transport != nil {
			b.transport.AddView(b.desktopPlaying)
		}
	}
	return b.desktopPlaying.Object()
}
