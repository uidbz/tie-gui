package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/tagselection"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// wallFeed identifies what the cover wall is currently showing, so
// reloadWall can re-run the same feed on demand.
type wallFeed int

const (
	feedNone   wallFeed = iota // empty wall (startup "none", or just after a collection switch)
	feedTags                   // a tag query (the sidebar selection or a startup tag page)
	feedLatest                 // the latest-albums startup page
	feedDir                    // a Files-tab directory listing
	feedHome                   // the Home page (home.go)
	feedList                   // a fixed album list (a Home shelf's "See all")
	feedSearch                 // a free-text search result wall
)

// browsePage is the album cover wall: a gallery grid driven by a Tags/Files/
// Settings sidebar (tag selection with co-tag refinement; a tie filesystem
// tree; the settings page). Opening a tile swaps the page to the album's
// track list (see album.go).
type browsePage struct {
	app fyne.App
	win fyne.Window
	// session swaps atomically on settings/connection changes (adoptSession):
	// fetch goroutines load it once up front, and generation guards drop their
	// superseded results instead of racing the swap.
	session atomic.Pointer[data.Session]

	// shell is the real window wrapper when win is one (it always is in the
	// app; tests may use a plain test window). The browse page installs the
	// desktop view-tab decorator on it.
	shell *shellWindow
	// viewTabs is the desktop Covers/Table/Now Playing selector, built on
	// first use. desktopPlaying is the Now Playing tab's content and a
	// player view, registered when built.
	viewTabs       *browseViewTabs
	desktopPlaying *desktopNowPlaying

	viewer *gallery.Gallery
	ts     *tagselection.TagSelection

	// feed records what currently feeds the cover wall, so reloadWall can
	// re-run it. album / albumOpen track the open album track list, so a
	// refresh requested while one covers the wall reloads it in place
	// instead of yanking the user back to the wall.
	feed      wallFeed
	album     data.Album
	albumOpen bool

	// albums is the wall's current listing, recorded by every feed so the
	// table view (walltable.go) renders the same albums as the cover grid.
	// wallTable is the table rendering, built lazily on first use (eagerly
	// when the config starts the app in table mode).
	albums    []data.Album
	wallTable *wallTable
	// home is the Home page, built on first use.
	home *homeView

	// Free-text search state. searchEntry/searchStatus live in the desktop
	// decorator; the compact layout reaches the same flow through the ☰ menu's
	// "Search…" item. searchPrevFeed is the feed to restore when the query is
	// cleared.
	searchEntry    *widget.Entry
	searchScope    *widget.Check
	searchStatus   *widget.Label
	searchBox      *fyne.Container
	searchQuery    string
	searchPrevFeed wallFeed

	// onFeedChanged, when set, is called after the wall switched feeds (the
	// App re-highlights its nav bar: Home vs Albums).
	onFeedChanged func()

	// transport is the shared playback controller, used to feed now-playing
	// labels when albums are played or enqueued. Wired by the App shell.
	transport *player

	// covers is the shared album-art cache, used by the wall's thumbnailer.
	covers *coverStore

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
	// compact is true in the phone layout, where the sidebar is a slide-over
	// drawer and the selected tags are summarised as filter chips above the
	// grid (the sidebar being off-screen, they would otherwise be invisible).
	compact bool

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
	// tagGen numbers loadTags calls (UI goroutine only): a fetch whose
	// generation is no longer current — a collection switch started another
	// one while it was in flight — is dropped instead of landing the old
	// collection's tags in the sidebar.
	tagGen int

	// wallGen numbers the album-wall feed fetches (refreshAlbums,
	// refreshLatest, startSearch, directory listings) and albumGen numbers
	// openAlbum; both are bumped on the UI goroutine only. A reply whose
	// generation is no longer current — the user picked another tag, started
	// a newer search or switched collections while it was in flight — is
	// dropped (rendered nothing AND no gallery readers), so the last
	// completion cannot win over the last request.
	wallGen  int
	albumGen int
	// coTagGen numbers refineTags; refineTags runs on its own goroutine, so
	// the counter is atomic.
	coTagGen atomic.Uint64

	// wallStale marks the gallery's contents as swapped without a re-render
	// (clearAlbums), so the next showBrowse must reload the grid. Otherwise
	// returning to the wall just re-shows it, keeping the scroll position.
	wallStale bool
	// onNowPlayingChip handles a tap on an open album's "Now playing" chip
	// (set by the App shell).
	onNowPlayingChip func()

	// playingHash is the tie hash of the track now playing (albumNowPlaying
	// feeds it); an open album view marks that row. albumTable and
	// albumPlaying are the open album view's table and "Now playing" chip.
	playingHash  string
	albumTable   *trackTable
	albumPlaying *widget.Button
}

// newBrowsePage builds the cover wall and its sidebar for the given session.
// The gallery owns the window content; sub-views (album, settings) swap it via
// window.SetContent and restore the wall with showWall (which renders the
// cover grid or the album table per the configured BrowseView).
func newBrowsePage(app fyne.App, win fyne.Window, session *data.Session, covers *coverStore, compact bool) *browsePage {
	b := &browsePage{app: app, win: win, covers: covers, compact: compact}
	b.session.Store(session)
	if shell, ok := win.(*shellWindow); ok {
		b.shell = shell
	}

	galCfg := gallery.LoadConfig(win, "")
	// Adjust before NewGallery: the config is passed by value, so a later
	// AdjustForMobile would only reach this page's copy and leave the gallery
	// itself with desktop tile sizes, page size and worker count.
	b.mobile = gallery.NewPlatform().IsMobile()
	if b.mobile {
		galCfg.AdjustForMobile()
	}

	b.viewer = gallery.NewGallery(app, win, galCfg, func(t *gallery.Tile) {
		t.Viewer.ChangeImage(t.Info) // Openable → routes to AudioAlbumItem.Open
	})
	b.viewer.OnTileSecondaryTapped = func(t *gallery.Tile) {
		if item, ok := t.Info.CustomReader.(*AudioAlbumItem); ok {
			b.showAlbumMenu(t, item.album)
		}
	}
	// Pull-to-refresh on the grid (mobile) re-runs the wall's current feed,
	// like the ☰ menu's "Reload albums" and the nav bar's Refresh button.
	b.viewer.OnPullRefresh = b.reloadWall
	b.viewer.Thumbnailer = &coverThumbnailer{
		page:      b,
		tileWidth: int(galCfg.General.TileWidth),
	}
	b.fsTree = newTieFSTree(b)
	b.viewer.Sidebar = b.buildSidebar()
	b.setGalleryChrome(compact)
	b.viewer.Init()
	// The file-browser tab shows hidden directories only on demand, toggled
	// from the gallery ☰ menu (matching tie-view). "Reload albums" re-runs
	// whatever the wall currently shows (and drops the tree's cached
	// listings), so content imported since they were read (e.g. via tie-fm)
	// appears without restarting the app.
	b.viewer.MenuItems = func() []*fyne.MenuItem {
		label := "Show hidden directories"
		if b.fsTree.showHidden {
			label = "Hide hidden directories"
		}
		items := []*fyne.MenuItem{
			fyne.NewMenuItem("Home", b.showHome),
			fyne.NewMenuItem("Search…", b.promptSearch),
			fyne.NewMenuItem(label, func() { b.fsTree.SetShowHidden(!b.fsTree.showHidden) }),
			fyne.NewMenuItem("Reload albums", b.reloadWall),
			// The full listing from anywhere (a tag wall, a Files directory):
			// clears the tag selection, which feeds the latest albums.
			fyne.NewMenuItem("Latest albums", b.showLatest),
		}
		// Swap the cover grid for the table or the large now-playing cover.
		// The desktop view tabs expose the same switch; the menu keeps it
		// reachable from the compact layout and the Home page.
		if !b.tableMode() {
			items = append(items, fyne.NewMenuItem("Table view", func() { b.setBrowseView(config.BrowseTable) }))
		}
		if !b.nowPlayingMode() && !b.compact {
			items = append(items, fyne.NewMenuItem("Now playing view", func() { b.setBrowseView(config.BrowseNowPlaying) }))
		}
		if b.tableMode() || b.nowPlayingMode() {
			items = append(items, fyne.NewMenuItem("Cover view", func() { b.setBrowseView(config.BrowseCovers) }))
		}
		return items
	}
	b.viewer.ToggleLabels() // album titles under covers, on by default

	// Configured to start in table mode: build the table now so Content can
	// return it as the initial view.
	if b.tableMode() {
		b.wallTable = newWallTable(b)
	}

	b.viewer.LoadGallery()
	b.viewer.CreateView()
	return b
}

// sess returns the live session (swapped atomically by adoptSession).
func (b *browsePage) sess() *data.Session { return b.session.Load() }

// setSession points the browse page at a rebuilt session.
func (b *browsePage) setSession(s *data.Session) { b.session.Store(s) }

// onSessionSwapped refreshes everything that was cached per connection or
// collection after adoptSession installs a new session.
func (b *browsePage) onSessionSwapped() {
	// Invalidate every in-flight reply from the old session: wall feeds and
	// search readers, album opens and co-tag refreshes.
	b.wallGen++
	b.albumGen++
	b.coTagGen.Add(1)
	b.fsTree.reset()
	b.loadTags()
	if b.home != nil {
		b.home.reset()
	}
	b.clearAlbums()
	// A switch lands on the same default view as a launch.
	b.applyStartupPage()
}

// Content is the browse page's root object, used as the window's initial
// content: the Home page when it is the startup page, else the gallery's
// root in cover mode, the album table in table mode.
func (b *browsePage) Content() fyne.CanvasObject {
	if b.feed == feedHome && b.home != nil {
		return b.homeRoot()
	}
	if b.nowPlayingMode() {
		return b.nowPlayingObject()
	}
	if b.tableMode() && b.wallTable != nil {
		return b.wallTableRoot()
	}
	return b.viewer.Content
}

// setCompact switches the sidebar between the split pane and the slide-over
// drawer, and (in the compact layout) turns on the filter chip row. The caller
// re-pushes the content afterwards.
func (b *browsePage) setCompact(compact bool) {
	if b.compact == compact {
		return
	}
	b.compact = compact
	b.setGalleryChrome(compact)
	b.syncSettingsTab()
	b.viewer.CreateView()
	if compact {
		b.setViewTabsEnabled(false)
	}
	if b.wallTable != nil {
		b.wallTable.setCompact(compact)
	}
	b.updateFilterChips()
}

// setGalleryChrome configures the gallery for the layout mode. In the compact
// layout the sidebar is a drawer, and the gallery's bottom-bar side buttons
// are dropped: the App's persistent nav bar carries both the drawer (Tags)
// and the ☰ menu, so the gallery's bar shrinks to its page links — and
// vanishes on a single-page wall.
func (b *browsePage) setGalleryChrome(compact bool) {
	b.viewer.SidebarDrawer = compact
	b.viewer.HideSidebarToggle = compact
	b.viewer.HideMenuButton = compact
}

// showMenuAt opens the library's ☰ menu (the gallery menu plus the page's own
// items) anchored at anchor — the compact nav bar's Menu slot.
func (b *browsePage) showMenuAt(anchor fyne.CanvasObject) {
	b.viewer.ShowMenuAt(anchor)
}

// showTagsTab switches the sidebar to its Tags tab (the nav bar's Tags
// destination; the Settings tab may have been left selected).
func (b *browsePage) showTagsTab() {
	if b.tabs != nil && len(b.tabs.Items) > 0 {
		b.tabs.SelectIndex(0)
	}
}

// openSidebar shows the tag/files sidebar: opening the drawer in the compact
// layout, and a no-op in the regular one where it is already a visible pane.
func (b *browsePage) openSidebar() {
	if !b.compact {
		return
	}
	// The cover wall must be the current view for the drawer to be visible.
	if b.albumOpen {
		b.showBrowse()
	}
	b.viewer.OpenSidebar()
}

// closeSidebar hides the drawer (compact layout only).
func (b *browsePage) closeSidebar() {
	if !b.compact {
		return
	}
	b.viewer.CloseSidebar()
}

// sidebarOpen reports whether the drawer is currently covering the grid. It is
// false in the regular layout, where the sidebar is a pane and nothing needs
// dismissing.
func (b *browsePage) sidebarOpen() bool {
	return b.compact && b.viewer.SidebarOpen()
}

// updateFilterChips refreshes the summary of the current tag selection shown
// above the grid. Only the compact layout needs it: with the sidebar in a
// drawer the selection is otherwise invisible, so the user cannot tell why the
// wall holds what it holds. Each chip drops its own tag; the row as a whole
// reopens the drawer.
func (b *browsePage) updateFilterChips() {
	if !b.compact {
		b.viewer.SetFilterChips(nil, nil)
		return
	}
	include, exclude := b.ts.SelectedTags()
	chips := gallery.TagFilterChips(include, exclude, func(tag string) {
		b.ts.RemoveSelected(tag)
	})
	b.viewer.SetFilterChips(chips, b.openSidebar)
}

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
		b.updateFilterChips()
		// In the compact layout the drawer covers the grid it just changed, so
		// a pick returns the user to the results; the chip row (and the Tags
		// button) get them back here.
		b.closeSidebar()
		if len(in) == 0 {
			// Nothing narrows the wall any more: show every album (newest
			// first, minus any excluded tags) instead of an empty wall — a
			// cleared selection used to leave no way back to the full
			// listing.
			b.refreshLatest()
		} else {
			b.refreshAlbums(in, ex)
		}
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
				err = b.sess().StarTag(tag)
			} else {
				err = b.sess().UnstarTag(tag)
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
// Tags / Files / Settings like tie-view's — in the regular layout. In the
// compact layout the persistent nav bar has its own Settings destination,
// so the drawer keeps just Tags / Files (syncSettingsTab).
func (b *browsePage) setSettingsTab(tab *container.TabItem) {
	b.settingsTab = tab
	b.syncSettingsTab()
}

// syncSettingsTab adds the Settings tab to the sidebar in the regular layout
// and removes it in the compact one: two tab bars both offering Settings,
// stacked above each other while the drawer is open, is one too many — and
// the full-screen settings view reuses the tab's content object, which must
// not have two on-screen parents.
func (b *browsePage) syncSettingsTab() {
	if b.settingsTab == nil || b.tabs == nil {
		return
	}
	present := false
	for _, it := range b.tabs.Items {
		if it == b.settingsTab {
			present = true
		}
	}
	switch {
	case b.compact && present:
		b.tabs.Remove(b.settingsTab)
	case !b.compact && !present:
		b.tabs.Append(b.settingsTab)
	}
}

// showSettingsTab switches the sidebar to the Settings tab (regular layout;
// a no-op while the tab is not in the sidebar).
func (b *browsePage) showSettingsTab() {
	if b.settingsTab == nil {
		return
	}
	for _, it := range b.tabs.Items {
		if it == b.settingsTab {
			b.tabs.Select(b.settingsTab)
			return
		}
	}
}

// refreshAlbums re-queries the album wall for the current tag selection.
func (b *browsePage) refreshAlbums(include, exclude []string) {
	b.resetSearchUI()
	// The wall no longer shows a directory listing, so a later "Reload
	// albums" must not resurrect one over the tag results.
	b.fsTree.mu.Lock()
	b.fsTree.currentDir = ""
	b.fsTree.mu.Unlock()
	b.feed = feedTags
	gen := b.nextWallGen()
	b.viewer.ReadCustomAsync(func() []gallery.CustomReader {
		albums, err := b.sess().QueryAlbums(include, exclude)
		if err != nil {
			fmt.Println("Error querying albums:", err)
			return nil
		}
		stale := true
		fyne.Do(func() {
			if gen == b.wallGen {
				stale = false
				b.setWallAlbums(albums)
			}
		})
		// Superseded fetches must not hand the gallery readers either: the
		// readers ReadCustomAsync returns get applied regardless of any UI
		// bookkeeping.
		if stale {
			return nil
		}
		return b.readers(albums)
	})
	b.showWall()
}

// nextWallGen stamps the start of a wall feed fetch (UI goroutine only).
// The fetch closure then re-checks gen == b.wallGen inside fyne.Do (fyne.Do
// blocks until the UI goroutine has run it, so the decision is safely
// published): a newer feed, search or session swap has taken over the wall.
func (b *browsePage) nextWallGen() int {
	b.wallGen++
	return b.wallGen
}

// ensureSearchBar builds the desktop search row once. The compact layout has
// no decorator, so it uses the ☰ menu's "Search…" dialog instead; both paths
// land in startSearch.
func (b *browsePage) ensureSearchBar() fyne.CanvasObject {
	if b.searchBox != nil {
		return b.searchBox
	}
	b.searchEntry = widget.NewEntry()
	b.searchEntry.SetPlaceHolder("Search albums, artists, tracks…")
	b.searchEntry.OnSubmitted = func(s string) { b.startSearch(s) }
	b.searchScope = widget.NewCheck("Current selection", nil)
	b.searchStatus = widget.NewLabel("")
	b.searchStatus.Hide()
	row := container.NewBorder(nil, nil, b.searchScope, nil, b.searchEntry)
	b.searchBox = container.NewVBox(row, b.searchStatus)
	return b.searchBox
}

// promptSearch is the compact/layout-independent entry point: a one-field
// dialog that feeds startSearch.
func (b *browsePage) promptSearch() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Artist, album or track")
	entry.SetText(b.searchQuery)
	dialog.ShowCustomConfirm("Search", "Search", "Cancel", entry, func(ok bool) {
		if ok {
			b.startSearch(entry.Text)
		}
	}, b.win)
}

// startSearch runs a free-text search and shows the result as an album wall.
// It is also the feed handler for reloadWall. "Current selection" restricts
// the results to the albums already on a tag wall.
func (b *browsePage) startSearch(query string) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 {
		b.clearSearch()
		return
	}
	if b.feed != feedSearch {
		b.searchPrevFeed = b.feed
	}
	b.albumOpen = false
	b.fsTree.mu.Lock()
	b.fsTree.currentDir = ""
	b.fsTree.mu.Unlock()
	b.feed = feedSearch
	b.searchQuery = query
	gen := b.nextWallGen()
	if b.searchEntry != nil && b.searchEntry.Text != query {
		b.searchEntry.SetText(query)
	}

	var allowed map[string]bool
	if b.searchScope != nil && b.searchScope.Checked && b.searchPrevFeed == feedTags {
		allowed = make(map[string]bool, len(b.albums))
		for _, a := range b.albums {
			allowed[a.UID] = true
		}
	}

	b.viewer.ReadCustomAsync(func() []gallery.CustomReader {
		result, err := b.sess().Search(query, 0)
		if err != nil {
			fmt.Println("Error searching albums:", err)
			fyne.Do(func() {
				if gen == b.wallGen && b.searchStatus != nil {
					b.searchStatus.SetText("Search failed: " + err.Error())
					b.searchStatus.Show()
				}
			})
			return nil
		}
		if allowed != nil {
			result = data.SearchWithin(result, allowed)
		}
		albums := result.Albums
		stale := true
		fyne.Do(func() {
			if gen != b.wallGen || b.feed != feedSearch || b.searchQuery != query {
				return // superseded by a newer query or feed
			}
			stale = false
			b.setWallAlbums(albums)
			if b.searchStatus != nil {
				text := fmt.Sprintf("%d albums", len(albums))
				if result.Total > len(albums) {
					text += fmt.Sprintf(" · %d matches", result.Total)
				}
				b.searchStatus.SetText(text + " for “" + query + "”")
				b.searchStatus.Show()
			}
		})
		if stale {
			return nil // the readers would replace a newer wall
		}
		return b.readers(albums)
	})
	b.showWall()
}

// resetSearchUI clears the search row and status when the wall leaves the
// search feed (a tag selection, Latest, a directory). startSearch owns the
// opposite transition and does not call it.
func (b *browsePage) resetSearchUI() {
	b.searchQuery = ""
	b.searchPrevFeed = feedNone
	if b.searchEntry != nil {
		b.searchEntry.SetText("")
	}
	if b.searchStatus != nil {
		b.searchStatus.SetText("")
		b.searchStatus.Hide()
	}
}

// clearSearch leaves the result wall and returns to the feed that started the
// search (tag selection or latest) when possible.
func (b *browsePage) clearSearch() {
	if b.feed != feedSearch {
		return
	}
	if b.searchEntry != nil {
		b.searchEntry.SetText("")
	}
	if b.searchStatus != nil {
		b.searchStatus.SetText("")
		b.searchStatus.Hide()
	}
	b.searchQuery = ""
	prev := b.searchPrevFeed
	b.searchPrevFeed = feedNone
	if prev == feedTags {
		if include, exclude := b.ts.SelectedTags(); len(include) > 0 || len(exclude) > 0 {
			b.refreshAlbums(include, exclude)
			return
		}
	}
	b.refreshLatest()
}

// refreshLatest feeds the wall with every album in the collection, most
// recently imported first (the startup "latest" page, and the wall for an
// empty tag selection). Excluded tags in the current selection still apply.
func (b *browsePage) refreshLatest() {
	b.resetSearchUI()
	_, exclude := b.ts.SelectedTags()
	b.fsTree.mu.Lock()
	b.fsTree.currentDir = ""
	b.fsTree.mu.Unlock()
	b.feed = feedLatest
	gen := b.nextWallGen()
	b.viewer.ReadCustomAsync(func() []gallery.CustomReader {
		albums, err := b.sess().LatestAlbumsExcluding(exclude)
		if err != nil {
			fmt.Println("Error querying latest albums:", err)
			return nil
		}
		stale := true
		fyne.Do(func() {
			if gen == b.wallGen {
				stale = false
				b.setWallAlbums(albums)
			}
		})
		if stale {
			return nil
		}
		return b.readers(albums)
	})
	b.showWall()
}

// showLatest returns the wall to every album, newest first: the tag selection
// is cleared (its chips too) and the latest feed runs.
func (b *browsePage) showLatest() {
	b.ts.SetSelected(nil)
	b.updateFilterChips()
	b.albumOpen = false
	b.refreshLatest()
	go b.refineTags(nil, nil)
}

// reloadWall re-runs whatever the browse page is currently showing: an open
// album's track list (re-fetched and re-rendered in place), or the cover
// wall's feed — a Files-tab directory listing re-read from the server, the
// latest-albums page, or the current tag selection's query. The Files-tab
// tree's cached listings are dropped too, so content imported while the app
// runs (e.g. via tie-fm) appears; likewise the decoded-cover cache, so
// re-imported artwork shows up. Bound to the ☰ menu's "Reload albums", the
// compact nav bar's Refresh button, and pull-to-refresh on the grid.
func (b *browsePage) reloadWall() {
	if b.albumOpen {
		b.openAlbum(b.album)
		return
	}
	switch b.feed {
	case feedHome:
		if b.home != nil {
			b.home.reload()
		}
		return
	case feedList:
		b.covers.Clear()
		b.viewer.ChangeGallery()
		return
	}
	b.covers.Clear()
	switch b.feed {
	case feedDir:
		// Drops the tree's cached listings and re-reads the shown directory.
		b.fsTree.reload()
	case feedLatest:
		b.fsTree.reset()
		b.refreshLatest()
	case feedTags:
		b.fsTree.reset()
		in, ex := b.ts.SelectedTags()
		b.refreshAlbums(in, ex)
	case feedSearch:
		b.fsTree.reset()
		b.startSearch(b.searchQuery)
	}
}

// applyStartupPage feeds the cover wall per the configured startup page
// (Settings → startup page). StartupHome (the default) shows the Home page;
// StartupBlank leaves the wall empty until the user picks a tag or a folder. The tag-based pages select their tag in the
// sidebar so the selection — and in the compact layout the filter chips —
// reflect what the wall shows; the co-tag refinement is deliberately not run,
// so the sidebar keeps the full tag list until the user changes the
// selection. Called at launch and after a collection switch.
func (b *browsePage) applyStartupPage() {
	tag := ""
	switch b.sess().Cfg.StartupPage {
	case config.StartupHome:
		b.showHome()
		return
	case config.StartupLatest:
		b.refreshLatest()
		return
	case config.StartupFavorites:
		tag = "favorite"
	case config.StartupPlaylists:
		tag = data.PlaylistTag
	case config.StartupTag:
		tag = strings.TrimSpace(b.sess().Cfg.StartupTag)
	}
	if tag == "" {
		return
	}
	b.ts.SetSelected([]string{tag})
	b.updateFilterChips()
	b.refreshAlbums([]string{tag}, nil)
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
	if b.home != nil {
		b.home.reset()
	}
	b.feed = feedNone
	b.wallStale = true
	b.setWallAlbums(nil)
	b.viewer.ReadCustomAsync(func() []gallery.CustomReader {
		return []gallery.CustomReader{}
	})
}

// loadTags fetches the tag lists (all tags + starred favorites) and populates
// the sidebar. It clears the selection and tag state first, so it also serves
// as the reload after a collection switch (matching tie-view's reloadTags);
// the network fetch runs in a goroutine. Only the latest call's result is
// applied.
func (b *browsePage) loadTags() {
	b.ts.ClearSelected()
	b.ts.ClearAllTags()
	b.ts.ClearFavorites()
	b.ts.SetStarred(nil)
	b.allTags = nil
	b.starred = nil
	b.ts.SetListLabel("Loading…")
	b.tagGen++
	gen, session := b.tagGen, b.sess()
	go func() {
		all, favorites, err := session.TagSets()
		fyne.Do(func() {
			if gen != b.tagGen {
				return // superseded by a later loadTags
			}
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
	gen := b.coTagGen.Add(1)
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
	coTags, err := b.sess().CoTags(include, exclude)
	if err != nil {
		fmt.Println("Error getting co-tags:", err)
		return
	}
	fyne.Do(func() {
		if gen != b.coTagGen.Load() {
			return // a newer selection's co-tags already landed
		}
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
