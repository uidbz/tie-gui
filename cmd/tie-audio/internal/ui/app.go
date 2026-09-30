// Package ui builds the tie-audio Fyne interface: the album browser,
// album/track views, the now-playing queue, and settings.
package ui

import (
	"fmt"
	"io"
	"path"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// shellWindow wraps the real window so every SetContent keeps the transport
// pinned at the bottom. The imgview gallery owns the window and calls
// SetContent itself on navigation, so pinning the bar at the window level (via
// this wrapper) is the only way it survives moving between the cover wall,
// album, and settings views.
//
// In the regular layout the shell also pins the play queue as a resizable
// right-hand pane (an HSplit whose trailing child is queuePanel), so the queue
// sits beside every view. The split instance is kept across navigations so the
// user's divider position survives the gallery's own SetContent calls. In the
// compact layout there is no pane — the queue is a separate full-screen view
// reached by swiping or the bottom nav bar.
type shellWindow struct {
	fyne.Window
	bar        fyne.CanvasObject
	queuePanel fyne.CanvasObject
	compact    bool
	split      *container.Split
	// splitOffset remembers the divider position across a compact-layout
	// excursion, which discards the split.
	splitOffset float64
	// watcher reports window-width changes so the shell can switch layout
	// modes. It is part of every wrap, so it is always laid out.
	watcher *widthWatcher
	// content is the inner view currently wrapped, kept so a layout-mode
	// change can re-wrap it without the caller re-navigating.
	content fyne.CanvasObject
	// edges, when set, returns the compact layout's edge-swipe strips for the
	// view being wrapped (nil for none). Evaluated on every wrap, because the
	// gallery and the album view push their content through SetContent
	// without telling the App.
	edges func() fyne.CanvasObject
}

// SetBottom swaps the pinned bottom bar (compact: the mini bar with or without
// the nav bar underneath) without touching the window content.
func (w *shellWindow) SetBottom(bar fyne.CanvasObject) {
	w.bar = bar
}

// wrap composes o with the transport bar (and, in the regular layout, the queue
// pane) without pushing it to the window, so Root() can reuse the same layout.
func (w *shellWindow) wrap(o fyne.CanvasObject) fyne.CanvasObject {
	w.content = o
	var composed fyne.CanvasObject
	if !w.compact && w.queuePanel != nil {
		if w.split == nil {
			w.split = container.NewHSplit(o, w.queuePanel)
			if w.splitOffset == 0 {
				w.splitOffset = 0.72
			}
			w.split.SetOffset(w.splitOffset)
		} else {
			w.split.Leading = o
		}
		w.split.Refresh()
		composed = container.NewBorder(nil, w.bar, nil, nil, w.split)
	} else {
		center := o
		if w.compact && w.edges != nil {
			if strips := w.edges(); strips != nil {
				center = container.NewStack(o, strips)
			}
		}
		composed = container.NewBorder(nil, w.bar, nil, nil, center)
	}
	if w.watcher == nil {
		return composed
	}
	return container.NewStack(w.watcher, composed)
}

// SetContent wraps o with the transport bar (and the regular layout's queue
// pane).
func (w *shellWindow) SetContent(o fyne.CanvasObject) {
	w.Window.SetContent(w.wrap(o))
}

// rewrap re-composes the current content, applying a changed layout mode or
// bottom bar without the caller re-navigating.
func (w *shellWindow) rewrap() {
	if w.content == nil {
		return
	}
	w.Window.SetContent(w.wrap(w.content))
}

// dropSplit discards the queue pane split, remembering its divider position.
// Called when entering the compact layout, where the queue is full-screen.
func (w *shellWindow) dropSplit() {
	if w.split != nil {
		w.splitOffset = w.split.Offset
		w.split = nil
	}
}

// appView identifies the shell's current view, which decides the bottom bar and
// where Back goes.
type appView int

// backKeyName is the key name Android and iOS send for the system back button.
const backKeyName fyne.KeyName = "Back"

const (
	viewBrowse appView = iota
	viewQueue
	viewSettings
	viewNowPlaying
)

// App is the top-level UI controller. The gallery browser owns the window;
// sub-views (album track list, queue, Now Playing, settings) swap the window
// content and restore the cover wall via the gallery. A shellWindow pins the
// transport to the bottom of every view. Changing settings rebuilds the
// session.
//
// The shell renders one of two layouts, chosen from the window width (see
// gallery.Platform.CompactLayout): the regular layout with a sidebar split and
// a permanent queue pane, or the compact (phone) layout where the tag sidebar
// is a drawer, the queue is a full-screen album-grouped list, and the transport
// is a mini bar that opens a full-screen Now Playing page.
type App struct {
	win      fyne.Window
	shell    *shellWindow
	session  *data.Session
	covers   *coverStore
	platform *gallery.Platform
	player   *player
	browse   *browsePage
	queue    *queuePage
	media    *mediaBridge
	mpris    *mprisBridge // nil when MPRIS is off (mobile, no session bus)
	regular  *regularBar
	mini     *miniBar
	nav      *navBar
	playing  *nowPlayingPage
	// albumNow marks the playing track in an open album view.
	albumNow *albumNowPlaying
	compact  bool
	view     appView
	prevView appView
	// hotkeys is the resolved key-name → action map from the config file's
	// [Hotkeys] table (desktop only; nil on mobile). Installed on the window
	// by syncBackHandler.
	hotkeys map[fyne.KeyName]func()
	// lastWidth is the most recent window width the watcher reported, used to
	// re-resolve the layout when the user changes the layout preference.
	lastWidth float32
	// settingsContent is the Settings tab's content object, reused when the
	// settings view is opened full-screen (compact bottom nav bar).
	settingsContent fyne.CanvasObject
	// layoutInfo is the Settings hint describing the layout in effect (see
	// refreshLayoutInfo).
	layoutInfo *widget.Label
}

// NewApp builds the UI for the given window and session.
func NewApp(win fyne.Window, session *data.Session) *App {
	a := &App{session: session, platform: gallery.NewPlatform()}
	a.covers = newCoverStore(session)
	a.compact = compactForWidth(session.Cfg.Layout, a.platform, canvasWidth(win))

	a.player = newPlayer(session.Backend, a.covers)
	// Re-label queue entries whose metadata this app didn't register itself
	// (e.g. a queue that pwplay kept across an app restart): the queue URL's
	// last path segment is the tie content hash.
	a.player.SetResolver(func(url string) (data.Track, bool) {
		return session.TrackForHash(path.Base(url))
	})

	// Every transport view stays registered whether or not it is on screen: a
	// hidden view costs one widget update per poll, far less than re-syncing
	// (and re-resolving artwork for) whichever view the user navigates to.
	a.regular = newRegularBar(a.player, func() { a.queue.revealCurrentSoon() })
	a.mini = newMiniBar(a.player, a.showNowPlaying, a.onMiniCover)
	a.playing = newNowPlayingPage(a.player, a.leaveNowPlaying)
	a.nav = newNavBar(navActions{
		albums:   a.navAlbums,
		tags:     a.navTags,
		playlist: a.showQueueView,
		settings: a.showSettingsView,
		menu:     func(anchor fyne.CanvasObject) { a.browse.showMenuAt(anchor) },
	})
	a.player.AddView(a.regular)
	a.player.AddView(a.mini)
	a.player.AddView(a.playing)
	a.player.AddView(a.nav)

	// The media bridge feeds the Android media session / foreground service
	// (a no-op off Android). It is enabled only for local playback: phone
	// audio focus and lock-screen controls must never drive a remote server.
	a.media = newMediaBridge(a.player)
	a.media.SetEnabled(playback.IsLocal(session.Backend))
	a.player.AddView(a.media)

	// On the desktop the player is also published over MPRIS (D-Bus), so
	// media keys, playerctl and window-manager bindings control it
	// system-wide. Inert where there is no session bus.
	if enableMPRIS && !a.platform.IsMobile() {
		bridge, err := newMPRISBridge(a.player, win)
		if err != nil {
			fmt.Println("tie-audio: MPRIS unavailable:", err)
		} else {
			a.mpris = bridge
			a.player.AddView(bridge)
		}
	}

	a.shell = &shellWindow{Window: win, compact: a.compact}
	a.shell.watcher = newWidthWatcher(a.onWidth)
	a.shell.edges = a.edgeOverlay
	a.win = a.shell

	// Build the queue before the browse page: constructing the gallery triggers
	// the gallery's own SetContent, and the regular shell needs queuePanel set
	// by then so the very first render already includes the queue pane. The
	// browse callbacks are reached via closures over a.browse, which is
	// assigned below.
	a.queue = newQueuePage(a.shell, session, a.player, a.covers, a.compact,
		func() { a.showBrowseView() },
		func(keys []string) { a.browse.saveQueueColumns(keys) },
	)
	if !a.compact {
		a.shell.queuePanel = a.queue.Object()
	}

	a.browse = newBrowsePage(fyne.CurrentApp(), a.shell, session, a.covers, a.compact)
	a.browse.transport = a.player
	a.albumNow = &albumNowPlaying{page: a.browse}
	a.browse.onNowPlayingChip = func() {
		if a.compact {
			a.showNowPlaying()
			return
		}
		a.browse.revealPlayingRow()
	}
	a.player.AddView(a.albumNow)
	// Let the browse page reach the queue page regardless of layout, so
	// play/enqueue actions update the queue view optimistically even in the
	// compact layout (where enableAlbumDragToQueue is not wired).
	a.browse.queue = a.queue
	// The Settings tab lives in the sidebar (Tags / Files / Settings, like
	// tie-view); the same tab item is reused to open the settings view.
	settingsTab := a.buildSettingsTab()
	a.browse.setSettingsTab(settingsTab)
	a.settingsContent = settingsTab.Content

	// Feed the wall per the configured startup page (latest / favorites /
	// playlists / a tag); StartupNone leaves it empty until a tag is picked.
	a.browse.applyStartupPage()

	// Swipes mirror the compact nav bar's buttons, and work in either layout on
	// a touch screen: left opens the playlist, right pulls in the sidebar
	// drawer (the gesture a left-anchored drawer implies). In the regular
	// layout the sidebar is already a pane, so a right swipe just re-shows the
	// wall.
	a.browse.viewer.OnSwipeLeft = a.showQueueView
	a.browse.viewer.OnSwipeRight = func() {
		if a.compact {
			a.browse.openSidebar()
			return
		}
		a.showBrowseView()
	}

	a.applyBottomBar()
	if !a.compact {
		// The queue is always visible in the right pane, so it subscribes to the
		// status poll immediately rather than on navigation.
		a.queue.show()
		// A cover dragged onto that pane inserts the album at the drop point.
		a.browse.enableAlbumDragToQueue(a.queue)
	}

	// Back (Android) and Escape unwind the view stack: the drawer first, then
	// Now Playing / queue / settings back to the cover wall. The handler is
	// installed only while there is something to unwind — Fyne routes the
	// Android back button to the driver's own "leave the app" behavior when no
	// handler is set, and capturing it unconditionally would leave the user
	// unable to back out of tie-audio at all. Gallery hotkeys are deliberately
	// not dispatched here: tie-audio never shows a single image, and the
	// gallery's default bindings include Quit.
	a.browse.viewer.OnSidebarToggled = func(bool) {
		a.syncNav()
		a.syncBackHandler()
	}
	a.initHotkeys()
	a.syncBackHandler()

	win.SetOnClosed(func() {
		a.player.Stop()
		a.media.Stop()
		a.mpris.Close()
		// The local engine holds a sink and a decoder goroutine; close it.
		if c, ok := a.player.be().(io.Closer); ok {
			_ = c.Close()
		}
	})
	a.player.Start()
	return a
}

// enableMPRIS gates the desktop MPRIS server; tests turn it off so building
// an App never claims a name on the developer's session bus.
var enableMPRIS = true

// canvasWidth reports the window's current canvas width, or 0 before the first
// layout (or in tests with no canvas).
func canvasWidth(win fyne.Window) float32 {
	if win == nil {
		return 0
	}
	c := win.Canvas()
	if c == nil {
		return 0
	}
	return c.Size().Width
}

// Root returns the object to place as the window content: the cover wall with
// the transport pinned at the bottom (and, in the regular layout, the queue
// pane).
func (a *App) Root() fyne.CanvasObject {
	return a.shell.wrap(a.browse.Content())
}

// onWidth switches layout modes when the window crosses the compact threshold.
// It runs inside a layout pass, so the actual rebuild is deferred onto the next
// UI-goroutine turn rather than mutating the tree being laid out.
//
// lastWidth is recorded whatever the outcome, so a later change of the layout
// preference can be resolved against the real window width.
func (a *App) onWidth(width float32) {
	a.lastWidth = width
	compact := compactForWidth(a.session.Cfg.Layout, a.platform, width)
	if compact == a.compact {
		return
	}
	fyne.Do(func() { a.setCompact(compact) })
}

// SetLayoutPreference pins the layout (or returns it to width-based) and
// applies the result immediately. Called from the Settings tab.
func (a *App) SetLayoutPreference(pref string) {
	a.session.Cfg.Layout = pref
	if err := config.Save(a.session.Cfg); err != nil {
		dialog.ShowError(err, a.win)
	}
	a.setCompact(compactForWidth(pref, a.platform, a.layoutWidth()))
	// setCompact is a no-op when the resolved layout is unchanged (e.g. pinning
	// the mode already in use), so the hint is refreshed here regardless.
	a.refreshLayoutInfo()
}

// layoutWidth is the width the layout decision should use: the last width the
// watcher observed, falling back to the canvas (before the first layout pass).
func (a *App) layoutWidth() float32 {
	if a.lastWidth > 0 {
		return a.lastWidth
	}
	return canvasWidth(a.shell)
}

// AutoLayoutLabel describes what the "auto" setting currently resolves to, so
// the Settings tab can show why the layout looks the way it does — the width a
// device reports is not something the user can otherwise discover.
func (a *App) AutoLayoutLabel() string {
	width := a.layoutWidth()
	mode := "regular"
	if compactForWidth(config.LayoutAuto, a.platform, width) {
		mode = "compact"
	}
	if width <= 0 {
		return "automatic would pick " + mode
	}
	return fmt.Sprintf("automatic would pick %s — window %.0fdp, touch threshold %.0fdp",
		mode, width, gallery.CompactWidth)
}

// refreshLayoutInfo updates the Settings hint describing the layout in effect
// and what the automatic choice would be. Called when the settings view opens
// and whenever the layout changes, so the reported width is never stale.
func (a *App) refreshLayoutInfo() {
	if a.layoutInfo == nil {
		return
	}
	inUse := "regular"
	if a.compact {
		inUse = "compact"
	}
	a.layoutInfo.SetText("Now using " + inUse + "; " + a.AutoLayoutLabel())
}

// setCompact re-renders the whole shell in the other layout mode: the sidebar
// becomes a drawer (or a pane again), the queue becomes a grouped list (or a
// table in a pane), and the transport becomes the mini bar (or the wide bar).
// Every view keeps its state; only the containers around them are rebuilt.
func (a *App) setCompact(compact bool) {
	if a.compact == compact {
		return
	}
	a.compact = compact
	a.shell.compact = compact

	if compact {
		a.shell.dropSplit()
		a.shell.queuePanel = nil
	} else {
		a.shell.queuePanel = a.queue.Object()
	}

	a.queue.setCompact(compact)
	a.browse.setCompact(compact)

	// The queue pane is permanently on screen in the regular layout, so it must
	// be subscribed there regardless of the current view.
	if !compact {
		a.queue.show()
	} else if a.view != viewQueue {
		a.queue.hide()
	}

	a.applyBottomBar()
	a.showCurrentView()
	a.refreshLayoutInfo()
}

// navAlbums is the nav bar's Albums tab: back to the wall from any view (an
// open album closes, as its own "Albums" button does), dismissing the drawer.
func (a *App) navAlbums() {
	a.browse.closeSidebar()
	a.showBrowseView()
}

// navTags is the nav bar's Tags tab: the tag drawer over the cover wall,
// opened on its Tags page (the Settings tab may have been left selected), or
// closed again when it is already open.
func (a *App) navTags() {
	if a.browse.sidebarOpen() {
		a.browse.closeSidebar()
		return
	}
	if a.view != viewBrowse || a.browse.albumOpen {
		a.showBrowseView()
	}
	a.browse.showTagsTab()
	a.browse.openSidebar()
}

// onMiniCover handles a tap on the mini bar's cover: the playlist (scrolled to
// the playing track) from every view, and Now Playing when the playlist is
// already on screen — the cover is the way "into" the music either way.
func (a *App) onMiniCover() {
	if a.view == viewQueue {
		a.showNowPlaying()
		return
	}
	a.showQueueView()
	a.queue.revealCurrentSoon()
}

// currentTab maps the view to the nav bar tab it belongs to.
func (a *App) currentTab() navTab {
	switch a.view {
	case viewQueue:
		return tabPlaylist
	case viewSettings:
		return tabSettings
	case viewNowPlaying:
		return tabNone
	}
	if a.browse != nil && a.browse.sidebarOpen() {
		return tabTags
	}
	return tabAlbums
}

// syncNav re-highlights the nav bar for the current view, and enables its ☰
// slot only where the library menu applies (the albums views).
func (a *App) syncNav() {
	a.nav.setActive(a.currentTab())
	a.nav.setMenuEnabled(a.view == viewBrowse)
}

// edgeOverlay returns the compact layout's edge-swipe strips for the current
// view, so a horizontal swipe from a screen edge moves to the neighbouring
// tab (Albums ← Playlist → Settings) on every full-screen view. The cover
// grid has none: the gallery's own swipe overlay already pages from there
// (left → playlist, right → drawer), and a left strip would sit on top of the
// drawer panel.
func (a *App) edgeOverlay() fyne.CanvasObject {
	if a.browse == nil {
		return nil
	}
	var left, right fyne.CanvasObject
	switch a.view {
	case viewBrowse:
		switch {
		case a.browse.albumOpen:
			left = newEdgeSwipe(a.showBrowseView)
		case a.browse.tableMode():
			// The table view has no gallery swipe overlay; the right edge leads
			// on to the playlist (closing the drawer first if it is open).
			right = newEdgeSwipeLeft(func() {
				if a.browse.sidebarOpen() {
					a.browse.closeSidebar()
					return
				}
				a.showQueueView()
			})
		default:
			return nil
		}
	case viewQueue:
		left = newEdgeSwipe(a.showBrowseView)
		right = newEdgeSwipeLeft(a.showSettingsView)
	case viewSettings:
		left = newEdgeSwipe(a.showQueueView)
	default:
		return nil
	}
	return container.NewBorder(nil, nil, left, right, nil)
}

// showBrowseView restores the cover wall. It is the back target of the queue,
// settings and Now Playing views.
func (a *App) showBrowseView() {
	if a.compact {
		a.queue.hide()
	}
	a.setView(viewBrowse)
	a.browse.showBrowse()
}

// showQueueView opens the play queue: full-screen in the compact layout, and in
// the regular layout a no-op beyond focus, since the queue pane is already on
// screen beside the current view.
func (a *App) showQueueView() {
	if !a.compact {
		a.queue.show()
		return
	}
	a.browse.closeSidebar()
	a.setView(viewQueue)
	a.win.SetContent(a.queue.Object())
	a.queue.show()
}

// showSettingsView opens the settings page full-screen (compact), mirroring
// the sidebar's own tab selection.
func (a *App) showSettingsView() {
	a.browse.closeSidebar()
	a.setView(viewSettings)
	a.refreshLayoutInfo()
	a.win.SetContent(a.settingsContent)
	a.browse.showSettingsTab()
}

// showNowPlaying opens the full-screen player, where the seek and volume
// sliders get the whole window width. It is reached by tapping or swiping up
// the mini bar, so it only exists in the compact layout; the regular bar
// already shows both sliders.
func (a *App) showNowPlaying() {
	if !a.compact {
		return
	}
	a.setView(viewNowPlaying)
	a.win.SetContent(a.playing.Object())
}

// leaveNowPlaying returns from the Now Playing page to the view that opened it.
func (a *App) leaveNowPlaying() {
	switch a.prevView {
	case viewQueue:
		a.showQueueView()
	case viewSettings:
		a.showSettingsView()
	default:
		a.showBrowseView()
	}
}

// setView records the current view (remembering the previous one for Back) and
// re-pins the bottom bar the new view needs.
func (a *App) setView(v appView) {
	if v != a.view {
		a.prevView = a.view
	}
	a.view = v
	a.applyBottomBar()
}

// showCurrentView re-pushes the current view's content, used after a layout
// mode change.
func (a *App) showCurrentView() {
	switch a.view {
	case viewQueue:
		if a.compact {
			a.win.SetContent(a.queue.Object())
			return
		}
		// The queue has become a pane; fall back to the cover wall behind it.
		a.setView(viewBrowse)
		a.browse.showBrowse()
	case viewSettings:
		a.win.SetContent(a.settingsContent)
	case viewNowPlaying:
		if a.compact {
			a.win.SetContent(a.playing.Object())
			return
		}
		a.setView(viewBrowse)
		a.browse.showBrowse()
	default:
		a.browse.showBrowse()
	}
}

// applyBottomBar pins the bar the current view and layout need: the wide
// transport bar in the regular layout; in the compact layout one bottom panel
// on every view — the mini bar (with its always-visible volume row) above the
// nav bar — except Now Playing, which carries its own full-width controls and
// keeps only the nav bar. Both sit on one shared background so the stack
// reads as a single surface.
func (a *App) applyBottomBar() {
	switch {
	case !a.compact:
		a.shell.SetBottom(a.regular.Object())
	case a.view == viewNowPlaying:
		a.shell.SetBottom(bottomPanel(a.nav.Object()))
	default:
		a.shell.SetBottom(bottomPanel(a.mini.Object(), a.nav.Object()))
	}
	a.syncNav()
	a.shell.rewrap()
	a.syncBackHandler()
}

// bottomPanel stacks the compact bottom rows on the shared panel background,
// under a separator from the content.
func bottomPanel(rows ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewStack(newPanelBackground(),
		container.NewVBox(append([]fyne.CanvasObject{widget.NewSeparator()}, rows...)...))
}

// syncBackHandler installs the window-level key handler while it has work to
// do: a Back press with something to unwind (an open drawer, or a view other
// than the cover wall), or the configured hotkeys (desktop — they must fire
// from every view, including the cover wall). With neither, the handler is
// removed so the platform's own back behavior — leaving the app on Android —
// still works.
func (a *App) syncBackHandler() {
	c := a.shell.Canvas()
	if c == nil {
		return
	}
	if a.view != viewBrowse || a.browse.sidebarOpen() || len(a.hotkeys) > 0 {
		c.SetOnTypedKey(a.keyPress)
		return
	}
	c.SetOnTypedKey(nil)
}

// keyPress unwinds the view stack on Back (Android) and Escape — an open
// sidebar drawer closes first, then Now Playing / queue / settings return to
// the cover wall — and dispatches the configured hotkeys (desktop) for every
// other key.
//
// Android and iOS deliver the system back button as the key name "Back" (there
// is no fyne.Key constant for it).
func (a *App) keyPress(ev *fyne.KeyEvent) {
	switch ev.Name {
	case fyne.KeyEscape, backKeyName:
		if a.browse.sidebarOpen() {
			a.browse.closeSidebar()
			return
		}
		switch a.view {
		case viewNowPlaying:
			a.leaveNowPlaying()
		case viewQueue, viewSettings:
			a.showBrowseView()
		}
		return
	}
	if fn, ok := a.hotkeys[ev.Name]; ok {
		fn()
	}
}
