package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/tagselection"

	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/data"
)

// browsePage is the album cover wall: a gallery grid driven by a tie tag
// selection sidebar with co-tag refinement. Opening a tile swaps the page to
// the album's track list (see album.go).
type browsePage struct {
	app     fyne.App
	win     fyne.Window
	session *data.Session

	viewer *gallery.Gallery
	ts     *tagselection.TagSelection

	// transport is the shared playback controller, used to feed now-playing
	// labels when albums are played or enqueued. Wired by the App shell.
	transport *transportBar

	// onSettings, when set, opens the settings sub-view. Wired by the App shell.
	onSettings func()
	// onQueue, when set, opens the queue sub-view. Wired by the App shell (mobile
	// only; on desktop the queue is always visible in the right pane).
	onQueue func()

	// mobile is true on touch platforms, where the queue is a swipe-reached
	// full-screen view rather than a persistent pane.
	mobile bool

	// queuePanel is the desktop right-hand playlist pane; a cover dragged onto its
	// bounds is inserted at the drop point. nil on mobile (no persistent pane).
	// Set by the App shell via enableAlbumDragToQueue.
	queuePanel fyne.CanvasObject
	// queue is the desktop playlist page, used to draw the drop indicator and
	// insert a dragged album at a position. Set alongside queuePanel.
	queue *queuePage
	// dragAlbum / dragGhost track an in-flight cover→queue drag: the album picked
	// up and the floating label shown under the cursor as the drag cue.
	dragAlbum data.Album
	dragGhost *widget.PopUp

	// allTags is the full tag list from the last load, restored when the
	// selection is cleared.
	allTags        []string
	favoritesLabel string
}

// newBrowsePage builds the cover wall and its sidebar for the given session.
// The gallery owns the window content; sub-views (album, settings) swap it via
// window.SetContent and restore the wall with viewer.ChangeGallery.
func newBrowsePage(app fyne.App, win fyne.Window, session *data.Session) *browsePage {
	b := &browsePage{app: app, win: win, session: session}

	config := gallery.LoadConfig(win, "")

	b.viewer = gallery.NewGallery(app, win, config, func(t *gallery.Tile) {
		t.Viewer.ChangeImage(t.Info) // Openable → routes to AudioAlbumItem.Open
	})
	b.viewer.OnTileSecondaryTapped = func(t *gallery.Tile) {
		if item, ok := t.Info.CustomReader.(*AudioAlbumItem); ok {
			b.showAlbumMenu(t, item.album)
		}
	}
	b.mobile = b.viewer.Platform().IsMobile()
	if b.mobile {
		config.AdjustForMobile()
	}
	b.viewer.Thumbnailer = &coverThumbnailer{
		page:      b,
		tileWidth: int(config.General.TileWidth),
	}
	b.viewer.Sidebar = b.buildSidebar()
	b.viewer.Init()
	b.viewer.ToggleLabels() // album titles under covers, on by default

	b.viewer.LoadGallery()
	b.viewer.CreateView()
	return b
}

// Content is the gallery's root object, used as the window's initial content.
func (b *browsePage) Content() fyne.CanvasObject { return b.viewer.Content }

// buildSidebar creates the tag selection widget with a top toolbar (Settings)
// and wires selection changes to re-query albums, with co-tag faceted
// refinement in the background.
func (b *browsePage) buildSidebar() fyne.CanvasObject {
	ts := tagselection.NewTagSelection(b.win)
	ts.ShowIncludeExclude = true
	b.ts = ts

	ts.OnSelectedChanged = func() {
		in, ex := ts.SelectedTags()
		b.win.Canvas().Unfocus()
		b.refreshAlbums(in, ex)
		go b.refineTags(in, ex)
	}

	b.loadTags()

	settingsBtn := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), func() {
		if b.onSettings != nil {
			b.onSettings()
		}
	})
	// On desktop the queue lives in a persistent right pane, so no nav button is
	// needed; on mobile it is reached via the Queue button (and swipe).
	var nav fyne.CanvasObject
	if b.mobile {
		queueBtn := widget.NewButtonWithIcon("Playlist", theme.ListIcon(), func() {
			if b.onQueue != nil {
				b.onQueue()
			}
		})
		nav = container.NewGridWithColumns(2, queueBtn, settingsBtn)
	} else {
		nav = settingsBtn
	}
	return container.NewBorder(nav, nil, nil, nil, ts)
}

// refreshAlbums re-queries the album wall for the current tag selection.
func (b *browsePage) refreshAlbums(include, exclude []string) {
	b.viewer.ReadCustomAsync(func() []gallery.CustomReader {
		albums, err := b.session.QueryAlbums(include, exclude)
		if err != nil {
			fmt.Println("Error querying albums:", err)
			return nil
		}
		return b.readers(albums)
	})
	b.viewer.ChangeGallery()
}

// readers wraps albums as gallery tiles bound to this page's album opener.
func (b *browsePage) readers(albums []data.Album) []gallery.CustomReader {
	readers := make([]gallery.CustomReader, 0, len(albums))
	for _, a := range albums {
		readers = append(readers, &AudioAlbumItem{album: a, open: b.openAlbum})
	}
	return readers
}

// loadTags fetches the full tag list and populates the sidebar.
func (b *browsePage) loadTags() {
	b.ts.SetListLabel("Loading…")
	go func() {
		tags, err := b.session.AllTags()
		fyne.Do(func() {
			if err != nil {
				b.ts.SetListLabel("Error loading tags")
				fmt.Println("Error loading tags:", err)
				return
			}
			b.allTags = tags
			b.favoritesLabel = "All tags"
			b.ts.ClearAllTags()
			for _, tag := range tags {
				b.ts.AddTag(tag)
			}
			b.ts.SetListLabel(b.favoritesLabel)
			b.ts.SetFavorites(tags)
		})
	}()
}

// refineTags narrows the sidebar to tags co-occurring with the current
// selection; an empty selection restores the full list.
func (b *browsePage) refineTags(include, exclude []string) {
	if len(include) == 0 && len(exclude) == 0 {
		fyne.Do(func() {
			b.ts.ClearAllTags()
			for _, tag := range b.allTags {
				b.ts.AddTag(tag)
			}
			b.ts.SetListLabel(b.favoritesLabel)
			b.ts.SetFavorites(b.allTags)
		})
		return
	}
	coTags, err := b.session.CoTags(include, exclude)
	if err != nil {
		fmt.Println("Error getting co-tags:", err)
		return
	}
	fyne.Do(func() {
		b.ts.ClearAllTags()
		for _, tag := range coTags {
			b.ts.AddTag(tag)
		}
		if len(coTags) > 0 {
			b.ts.SetListLabel("Related tags")
		} else {
			b.ts.SetListLabel("No related tags")
		}
		b.ts.SetFavorites(coTags)
	})
}
