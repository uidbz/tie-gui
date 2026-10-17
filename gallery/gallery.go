package gallery

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/mpvplayer"
	"github.com/uidbz/tie-gui/tielog"
)

type Gallery struct {
	// ═══════════════════════════════════════════════════════════════════════
	// Extension API — Callbacks
	// ═══════════════════════════════════════════════════════════════════════
	// Set by applications to customize behavior. See gallery/extension.go for
	// full documentation.

	// OnImageChange is called after ChangeImage displays a new image.
	OnImageChange func(info *ImageInfo)
	// OnTapped is called when the user taps the image view (desktop: single click).
	OnTapped func()
	// OnDoubleTapped is called on double-tap/double-click of the image view.
	OnDoubleTapped func()
	// OnTappedSecondary is called on right-click (desktop) or long-press (mobile).
	OnTappedSecondary func()
	// OnSwipeUp is called when the user swipes upward on the image view.
	// Preferred over OnTapped on mobile to avoid conflicts with pinch-zoom.
	OnSwipeUp func()
	// OnSwipeLeft / OnSwipeRight are called when the user swipes horizontally on
	// the gallery grid (mobile only): left = finger moves left, right = finger
	// moves right. They let the embedding app page between the grid and an
	// adjacent view (e.g. a play queue). Vertical scrolling is preserved.
	OnSwipeLeft  func()
	OnSwipeRight func()
	// OnPullRefresh is called when the user drags the gallery grid downward
	// past a threshold while it is scrolled to the top (mobile only — the
	// gesture rides the grid swipe overlay). Apps use it to re-query the
	// content feeding the grid. Ordinary scrolling is unaffected: the pull
	// only charges while the grid is already at the top, where a downward
	// drag cannot scroll.
	OnPullRefresh func()
	// OnTileSecondaryTapped is called when a gallery tile receives a secondary
	// tap (right-click). Set by the caller to implement context actions such as
	// de-import. Receives the full Tile so the caller can inspect Info.
	OnTileSecondaryTapped func(*Tile)

	// OnSidebarToggled is called after the sidebar drawer opens or closes,
	// including when the user dismisses it by tapping the scrim. Apps use it to
	// track whether a Back press should close the drawer rather than navigate.
	OnSidebarToggled func(open bool)

	// OnTileDragStart / OnTileDragged / OnTileDragEnd, when OnTileDragged is set,
	// enable dragging a tile onto another widget (e.g. dropping an album cover
	// into a play queue). They fire only on desktop: setting OnTileDragged makes
	// each non-preview tile carry a transparent drag catcher that forwards taps
	// (so tapping still opens the entry) but turns a drag into these callbacks.
	// Dragged/DragEnd receive the pointer's absolute position for cross-widget
	// hit-testing (Fyne has no cross-widget drop target). The consumer draws its
	// own drag cue and decides what a drop means.
	OnTileDragStart func(*Tile)
	OnTileDragged   func(*Tile, fyne.Position)
	OnTileDragEnd   func(*Tile, fyne.Position)

	// ═══════════════════════════════════════════════════════════════════════
	// Extension API — Public Fields
	// ═══════════════════════════════════════════════════════════════════════

	// Content is the root Fyne container for the gallery. Set this as the
	// window's content to display the gallery.
	Content *fyne.Container
	// CurrentImage is the container for the single-image view.
	CurrentImage *fyne.Container
	// CurrentImageView is the currently displayed ImageView widget (nil when
	// showing the gallery grid).
	CurrentImageView *ImageView
	// CustomReaders holds the current list of content sources (populated via
	// ReadCustom).
	CustomReaders []CustomReader
	// Thumbnailer, when non-nil, supplies thumbnails for reader-backed
	// items (CustomReader != nil) instead of the local thumbnail directory
	// (see GeneralConfig.ThumbnailDir). Plain local files always use the
	// local thumbnail directory.
	Thumbnailer Thumbnailer
	// Logger receives everything the gallery cannot show on screen: a
	// thumbnail that failed to load, a listing fetch that threw, a panic on a
	// loader goroutine. Defaults to discard — the gallery is a library and must
	// not write to stdout or stderr, which on Android do not exist. Apps pass
	// their tielog logger with SetLogger.
	Logger *slog.Logger
	// Sidebar, when non-nil, is shown left of the gallery (e.g. a tag
	// selector) instead of a plain full-width gallery.
	Sidebar fyne.CanvasObject
	// SidebarDrawer selects how Sidebar is presented. When false (the default)
	// it is an HSplit pane beside the grid. When true it is a slide-over drawer
	// above the grid, opened with the bottom-bar button and dismissed by
	// tapping the scrim — the usable layout on a phone, where a split leaves
	// neither side wide enough. Set it before CreateView; changing it later
	// takes effect on the next CreateView.
	SidebarDrawer bool
	// MenuItems, when non-nil, returns extra items appended to the gallery's
	// ☰ popup menu. Embedding apps set it to expose their own toggles (e.g.
	// tie-view's "Show hidden directories").
	MenuItems func() []*fyne.MenuItem
	// HideSidebarToggle drops the sidebar toggle from the bottom bar, for
	// embedding apps whose own navigation already opens the sidebar (e.g.
	// tie-audio's compact nav bar "Tags" tab). ToggleSidebar / OpenSidebar
	// keep working. Takes effect on the next CreateView.
	HideSidebarToggle bool
	// HideMenuButton drops the ☰ button from the bottom bar, for embedding
	// apps that host the gallery menu elsewhere (ShowMenuAt). Takes effect
	// on the next CreateView.
	HideMenuButton bool

	// ═══════════════════════════════════════════════════════════════════════
	// Internal Wiring — Do Not Access Directly
	// ═══════════════════════════════════════════════════════════════════════

	gallery *fyne.Container
	// filesMu guards imageFiles. Reads come from the UI goroutine (page turns,
	// next/prev, pagination) and from placement goroutines; writes come from
	// the UI goroutine (ShowImageDir) and from ReadCustomAsync's fetch
	// goroutine. The listing is only ever replaced, never edited in place, so
	// a snapshot taken under filesMu is safely immutable.
	filesMu      sync.RWMutex
	imageFiles   []*ImageInfo
	layout       *TileLayout
	window       fyne.Window
	app          fyne.App
	platform     *Platform
	currentIndex int
	currentPath  string
	// cacheMu guards cache: LoadImageToCache runs on the UI goroutine (via
	// ChangeImage) and on background prefetch goroutines concurrently.
	cacheMu           sync.Mutex
	cache             map[string]*ImageView
	scroll            *container.Scroll
	savedScrollOffset fyne.Position
	hotkeys           []Hotkey
	// fetchMu guards fetchCount and fetchIdle, which track in-flight listing
	// reads (ReadCustom, ReadImageDir, ReadImageArchive and ReadCustomAsync's
	// fetch). A sync.WaitGroup would express the same thing, but its rule that
	// Add must not race with a Wait at a zero counter is easy to break here:
	// the waits now run on background goroutines while the Adds stay on the UI
	// goroutine.
	fetchMu    sync.Mutex
	fetchCount int
	fetchIdle  []chan struct{}
	// placeSeq numbers page placements. A LoadGallery that has already been
	// superseded by a newer one skips its placement entirely instead of
	// laying out a page the user has moved past.
	placeSeq atomic.Uint64
	// syncPlace runs page placement on the caller's goroutine. See
	// SetSyncPlacement.
	syncPlace     bool
	galleryLoaded bool
	config        Config
	bottomBar     *fyne.Container
	// bottomRow is the bottom bar composed with its side buttons, and
	// mainBorder the Border giving it its space: a single-page gallery hides
	// the pagination links, and the whole row when no side buttons remain,
	// which has to re-layout mainBorder (see syncBottomRow).
	bottomRow     *fyne.Container
	mainBorder    *fyne.Container
	sidebarStored fyne.CanvasObject // saved sidebar when hidden by the toggle
	sidebarToggle *widget.Button    // ◀/▶ button in the bottom bar; nil when no sidebar
	// drawer holds the sidebar when SidebarDrawer is set; nil in split mode.
	// It owns the sidebar object while it exists, so CreateView must drop it
	// when switching back to the split layout (Fyne objects cannot have two
	// parents).
	drawer *sidebarDrawer
	// filterChips is the summary row above the grid (see SetFilterChips);
	// filterChipRow is its container and filterChipParent the Border that
	// gives it its space — showing or hiding the row has to re-layout that
	// parent, not just the row.
	filterChips      []FilterChip
	filterChipsOnTap func()
	filterChipRow    *fyne.Container
	filterChipParent *fyne.Container
	menuButton       *widget.Button // ☰ menu button in the bottom bar (right side)
	refreshThumbs    bool
	tileOnclick      func(*Tile)
	currentPage      int
	maxPages         int
	isFullscreen     bool
	currentVideo     *mpvplayer.Video // non-nil while a video plays in the main window
	videoOnClose     func()           // cleanup (e.g. temp-file removal) run after the video closes
	// openedInfo records the gallery entry whose single-image view is (or was
	// last) open; showGallery switches to its page and scrolls it into view.
	openedInfo *ImageInfo
	// infoOverlay, when non-nil, is the metadata panel laid over the
	// single-image view (toggled with the I key or the ☰ menu).
	infoOverlay *infoOverlay
	// sizeWatcher reports grid-width changes so the pagination links are
	// rebuilt when the window is resized (2-6 page slots by width).
	sizeWatcher *sizeWatcher
	// imageMenuButton/imageMenuOverlay float a ☰ menu button over the
	// bottom-right of the single-image view so the gallery menu (image
	// info, filename toggle) is reachable while an image is open; the
	// bottom bar (and its own menu button) is not on screen then.
	imageMenuButton  *widget.Button
	imageMenuOverlay *fyne.Container
	// infoMetadataFn loads the info overlay's byte-level metadata (size,
	// format, EXIF); nil selects asyncInfoMetadata. Tests substitute a
	// synchronous implementation because the test driver runs fyne.Do
	// inline, which would race with UI-thread text shaping.
	infoMetadataFn func(info *ImageInfo, apply func(imageMetadata))
}

// NewGallery creates a Gallery instance but does not wire hotkeys or layout.
// Call Init() after setting optional fields (Sidebar, Thumbnailer, callbacks)
// to complete initialization.
//
// The two-step construction pattern exists because applications need to
// customize the Gallery between creation and initialization:
//   - tie-view sets Sidebar to the tag filter panel
//   - tie-view sets Thumbnailer to the filehost thumbnailer
//   - Both mains set OnImageChange, OnTapped, etc.
//
// These fields must be set before Init() is called because Init() wires hotkeys
// and layout, which may depend on the configuration being complete.
//
// Example usage:
//
//	viewer := gallery.NewGallery(app, window, config, tileOnclick)
//	viewer.Sidebar = makeSidebar(...)        // tie-view only
//	viewer.Thumbnailer = makeThumbnailer()   // tie-view only
//	viewer.OnImageChange = func(info) { ... }
//	viewer.Init()  // Wires hotkeys, creates layout
func NewGallery(app fyne.App, window fyne.Window, config Config, tileOnclick func(t *Tile)) *Gallery {
	iv := &Gallery{
		app:      app,
		window:   window,
		platform: NewPlatform(),
		Content:  container.NewStack([]fyne.CanvasObject{}...),
		// CurrentImage: container.NewBorder(nil, nil, nil, rect, container.New(&ImageLayout{}, []fyne.CanvasObject{}...)),
		CurrentImage:  container.New(&ImageLayout{}, []fyne.CanvasObject{}...),
		cache:         make(map[string]*ImageView),
		config:        config,
		tileOnclick:   tileOnclick,
		refreshThumbs: false,
		Logger:        tielog.Discard(),
	}

	return iv
}

// SetLogger installs the logger the gallery and everything it owns report to.
// Call it before Init so the layout and any thumbnailer pick it up.
func (viewer *Gallery) SetLogger(l *slog.Logger) {
	if l == nil {
		l = tielog.Discard()
	}
	viewer.Logger = l
	if viewer.layout != nil {
		viewer.layout.log = l
	}
}

// log returns the gallery's logger, so a Gallery built by hand without one is
// still safe to use.
func (viewer *Gallery) log() *slog.Logger {
	if viewer.Logger == nil {
		return tielog.Discard()
	}
	return viewer.Logger
}

// Platform returns the gallery's platform abstraction for mobile vs desktop behavior.
func (viewer *Gallery) Platform() *Platform {
	return viewer.platform
}

// ReleaseFocus unfocuses the currently focused widget so window-level hotkeys
// (gallery grid shortcuts) resume receiving typed keys. Embedding apps call
// this after a widget that captured keyboard focus (e.g. a filesystem tree)
// finishes navigating back to the grid — mirroring the tag sidebar's Unfocus.
func (viewer *Gallery) ReleaseFocus() {
	viewer.window.Canvas().Unfocus()
}
