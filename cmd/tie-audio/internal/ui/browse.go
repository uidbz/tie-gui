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

// browsePage is the album cover wall: a gallery grid driven by a Tags/Files/
// Settings sidebar (tag selection with co-tag refinement; a tie filesystem
// tree; the settings page). Opening a tile swaps the page to the album's
// track list (see album.go).
type browsePage struct {
	app     fyne.App
	win     fyne.Window
	session *data.Session

	viewer *gallery.Gallery
	ts     *tagselection.TagSelection

	// transport is the shared playback controller, used to feed now-playing
	// labels when albums are played or enqueued. Wired by the App shell.
	transport *transportBar

	// fsTree is the file-browser tab: the tie virtual filesystem as a tree,
	// mirroring tie-view. Built before the sidebar (it is one of its tabs).
	fsTree *tieFSTree
	// tabs is the sidebar's Tags / Files / Settings tab container, kept so
	// the settings page can switch back to its own tab (e.g. its Back
	// button) instead of leaving the settings view.
	tabs *container.AppTabs
	// settingsTab is the Settings tab item (created by the App shell via
	// buildSettingsTab); the App shell needs the same object to open the
	// settings view.
	settingsTab *container.TabItem

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
	b.fsTree = newTieFSTree(b)
	b.viewer.Sidebar = b.buildSidebar()
	b.viewer.Init()
	// The file-browser tab shows hidden directories only on demand, toggled
	// from the gallery ☰ menu (matching tie-view).
	b.viewer.MenuItems = func() []*fyne.MenuItem {
		label := "Show hidden directories"
		if b.fsTree.showHidden {
			label = "Hide hidden directories"
		}
		return []*fyne.MenuItem{
			fyne.NewMenuItem(label, func() { b.fsTree.SetShowHidden(!b.fsTree.showHidden) }),
		}
	}
	b.viewer.ToggleLabels() // album titles under covers, on by default

	b.viewer.LoadGallery()
	b.viewer.CreateView()
	return b
}

// Content is the gallery's root object, used as the window's initial content.
func (b *browsePage) Content() fyne.CanvasObject { return b.viewer.Content }

// buildSidebar creates the navigation sidebar: a Tags tab (tag selection
// with co-tag faceted refinement and ☆/★ favorites, matching tie-view), a
// Files tab (the tie virtual filesystem tree), and — appended later via
// setSettingsTab — a Settings tab.
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

	// The tag list grows with the store; wrap it in a scroll so a large tag
	// count doesn't inflate the window's minimum size.
	tagTab := container.NewTabItemWithIcon("Tags", theme.ListIcon(), container.NewVScroll(ts))
	filesTab := container.NewTabItemWithIcon("Files", theme.FolderIcon(), b.fsTree.tree)
	b.tabs = container.NewAppTabs(tagTab, filesTab)
	b.tabs.SetTabLocation(container.TabLocationBottom)
	return b.tabs
}

// setSettingsTab installs the Settings tab (created by the App shell, which
// owns the settings page) as the sidebar's last tab, so the sidebar shows
// Tags / Files / Settings like tie-view's.
func (b *browsePage) setSettingsTab(tab *container.TabItem) {
	b.settingsTab = tab
	b.tabs.Append(tab)
}

// showSettingsTab switches the sidebar to the Settings tab.
func (b *browsePage) showSettingsTab() {
	if b.settingsTab != nil {
		b.tabs.Select(b.settingsTab)
	}
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
