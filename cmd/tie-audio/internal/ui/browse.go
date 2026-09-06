package ui

import (
	"fmt"
	"slices"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/tagselection"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
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

	// allTags is the full tag list and starred the tie favorites from the
	// most recent fetch, kept current by star toggles. All reads and writes
	// happen on the UI goroutine (inside fyne.Do), so no mutex is needed.
	allTags []string
	starred []string
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
// refinement in the background. The quick-pick list shows the starred
// favorites (falling back to every tag while none are starred) and its rows
// carry a ☆/★ toggle, matching tie-view's sidebar.
func (b *browsePage) buildSidebar() fyne.CanvasObject {
	ts := tagselection.NewTagSelection(b.win)
	ts.ShowIncludeExclude = true
	ts.ShowStars = true
	b.ts = ts

	ts.OnSelectedChanged = func() {
		in, ex := ts.SelectedTags()
		b.win.Canvas().Unfocus()
		b.refreshAlbums(in, ex)
		go b.refineTags(in, ex)
	}

	// Starring here persists to tie's ("tags","favorite") registry like
	// tie-view's sidebar does; the optimistic update is rolled back if the
	// write fails.
	ts.OnStar = func(tag string, starred bool) {
		b.win.Canvas().Unfocus() // the star button took keyboard focus
		b.setStarred(tag, starred)
		go func() {
			var err error
			if starred {
				err = b.session.StarTag(tag)
			} else {
				err = b.session.UnstarTag(tag)
			}
			if err != nil {
				fmt.Printf("sidebar: failed to %s tag %q: %v\n",
					map[bool]string{true: "star", false: "unstar"}[starred], tag, err)
				fyne.Do(func() { b.setStarred(tag, !starred) })
			}
		}()
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
	// The tag list grows with the store; wrap it in a scroll so a large tag
	// count doesn't inflate the window's minimum size.
	return container.NewBorder(nav, nil, nil, nil, container.NewVScroll(ts))
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

// clearAlbums empties the cover wall without changing the window content.
// Used after a collection switch (from Settings) so albums carried over from
// the prior collection can neither be displayed nor opened; the empty wall
// renders when the user returns via showBrowse.
func (b *browsePage) clearAlbums() {
	b.viewer.ReadCustomAsync(func() []gallery.CustomReader {
		return []gallery.CustomReader{}
	})
}

// loadTags fetches the tag lists (all tags + starred favorites) and populates
// the sidebar. It clears the selection and tag state first, so it also serves
// as the reload after a collection switch (matching tie-view's reloadTags);
// the network fetch runs in a goroutine.
func (b *browsePage) loadTags() {
	b.ts.ClearSelected()
	b.ts.ClearAllTags()
	b.ts.ClearFavorites()
	b.ts.SetStarred(nil)
	b.allTags = nil
	b.starred = nil
	b.ts.SetListLabel("Loading…")
	go func() {
		all, favorites, err := b.session.TagSets()
		fyne.Do(func() {
			if err != nil {
				b.ts.SetListLabel("Error loading tags")
				fmt.Println("Error loading tags:", err)
				return
			}
			b.allTags = all
			b.starred = favorites
			for _, tag := range all {
				b.ts.AddTag(tag)
			}
			b.ts.SetStarred(favorites)
			b.applyFavoritesView()
		})
	}()
}

// applyFavoritesView shows the default quick-pick list — the starred tags,
// or every tag while none are starred — when no selection narrows the list
// (matching tie-view).
func (b *browsePage) applyFavoritesView() {
	favorites, label := b.starred, "Favorites"
	if len(b.starred) == 0 {
		favorites, label = b.allTags, "All tags"
	}
	if in, ex := b.ts.SelectedTags(); len(in) == 0 && len(ex) == 0 {
		b.ts.SetListLabel(label)
		b.ts.SetFavorites(favorites)
	}
}

// setStarred records a star toggle locally (no tie write): the ☆/★ button
// state and the default quick-pick list.
func (b *browsePage) setStarred(tag string, starred bool) {
	idx := slices.Index(b.starred, tag)
	switch {
	case starred && idx < 0:
		b.starred = append(b.starred, tag)
		sort.Strings(b.starred)
	case !starred && idx >= 0:
		b.starred = slices.Delete(b.starred, idx, idx+1)
	default:
		return
	}
	b.ts.ToggleStar(tag, starred)
	b.applyFavoritesView()
}

// refineTags narrows the sidebar to tags co-occurring with the current
// selection; an empty selection restores the default quick-pick list.
func (b *browsePage) refineTags(include, exclude []string) {
	if len(include) == 0 && len(exclude) == 0 {
		fyne.Do(func() {
			b.ts.ClearAllTags()
			for _, tag := range b.allTags {
				b.ts.AddTag(tag)
			}
			b.applyFavoritesView()
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
