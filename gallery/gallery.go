package gallery

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mholt/archives"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/mpvplayer"
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
	// OnTileSecondaryTapped is called when a gallery tile receives a secondary
	// tap (right-click). Set by the caller to implement context actions such as
	// de-import. Receives the full Tile so the caller can inspect Info.
	OnTileSecondaryTapped func(*Tile)

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
	// Thumbnailer, when non-nil, supplies thumbnails for all items instead
	// of the local thumbnail directory (see GeneralConfig.ThumbnailDir).
	Thumbnailer Thumbnailer
	// Sidebar, when non-nil, is shown left of the gallery (e.g. a tag
	// selector) instead of a plain full-width gallery.
	Sidebar fyne.CanvasObject

	// ═══════════════════════════════════════════════════════════════════════
	// Internal Wiring — Do Not Access Directly
	// ═══════════════════════════════════════════════════════════════════════

	gallery      *fyne.Container
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
	loading           sync.WaitGroup
	galleryLoaded     bool
	config            Config
	bottomBar         *fyne.Container
	sidebarStored     fyne.CanvasObject // saved sidebar when hidden by the toggle
	sidebarToggle     *widget.Button    // ◀/▶ button in the bottom bar; nil when no sidebar
	menuButton        *widget.Button    // ☰ menu button in the bottom bar (right side)
	refreshThumbs     bool
	tileOnclick       func(*Tile)
	currentPage       int
	maxPages          int
	isFullscreen      bool
	currentVideo      *mpvplayer.Video // non-nil while a video plays in the main window
	videoOnClose      func()           // cleanup (e.g. temp-file removal) run after the video closes
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
	}

	return iv
}

// Platform returns the gallery's platform abstraction for mobile vs desktop behavior.
func (viewer *Gallery) Platform() *Platform {
	return viewer.platform
}

func (viewer *Gallery) KeyPress(key *fyne.KeyEvent) {
	// While a video plays it fills the main window; route keys to playback
	// controls and skip the grid/image hotkeys (which reference no video state).
	if viewer.currentVideo != nil {
		bindings := viewer.config.Image
		if key.Name == fyne.KeySpace {
			viewer.currentVideo.TogglePlay()
			return
		}
		// Android/iOS hardware back returns to the grid.
		if key.Name == "Back" {
			viewer.showGallery()
			return
		}
		for _, x := range bindings.FullScreen {
			if key.Name == fyne.KeyName(x) {
				viewer.toggleVideoFullscreen()
				return
			}
		}
		for _, x := range bindings.ShowGallery {
			if key.Name == fyne.KeyName(x) {
				viewer.showGallery()
				return
			}
		}
		for _, x := range bindings.Quit {
			if key.Name == fyne.KeyName(x) {
				viewer.showGallery()
				return
			}
		}
		return
	}
	for _, x := range viewer.layout.hotkeys {
		if key.Name == x.Name {
			x.Function()
		}
	}
	// On mobile the image view is not focused (to suppress the soft keyboard),
	// so image-viewer hotkeys (including the Back key) never reach TypedKey.
	// Handle them here instead.
	if viewer.platform.ShouldHandleHotkeysAtWindowLevel() {
		for _, x := range viewer.hotkeys {
			if key.Name == x.Name {
				x.Function()
			}
		}
	}
}

func (viewer *Gallery) ShowImageDir(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.currentPage = 0
	viewer.layout.offset = 0
	viewer.openedInfo = nil
	viewer.ReadImageDir(path, nil)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) ShowImageArchive(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.currentPage = 0
	viewer.layout.offset = 0
	viewer.openedInfo = nil
	viewer.ReadImageArchive(path)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

// Init completes Gallery initialization by wiring hotkeys and creating the
// layout. Must be called after NewGallery and after setting optional fields
// (Sidebar, Thumbnailer, callbacks). See NewGallery documentation for the
// two-step construction pattern rationale.
func (viewer *Gallery) Init() {
	viewer.layout = NewTileLayout(viewer.config, viewer.window, viewer.app, viewer, viewer.tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)
	viewer.layout.grid = viewer.gallery
	viewer.layout.InitHotkeys()
	viewer.InitHotkeys()
	// The floating image-view menu button opens the same popup menu as the
	// bottom-bar button. It is stacked over the image by ChangeImage (the
	// bottom bar is not on screen then); a separate instance is required
	// because Fyne objects cannot be shared between two parents.
	viewer.imageMenuButton = widget.NewButton("☰", func() {
		viewer.showGalleryMenu()
	})
	viewer.imageMenuOverlay = container.NewBorder(nil,
		container.NewHBox(layout.NewSpacer(), viewer.imageMenuButton), nil, nil)
}

// ToggleSidebar hides the sidebar when it is visible, and restores it when
// it is hidden. It rebuilds the gallery layout immediately so the change
// takes effect without a restart.
func (viewer *Gallery) ToggleSidebar() {
	if viewer.Sidebar != nil {
		viewer.sidebarStored = viewer.Sidebar
		viewer.Sidebar = nil
	} else {
		viewer.Sidebar = viewer.sidebarStored
	}
	viewer.CreateView()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) CreateView() {
	var mainPage fyne.CanvasObject
	if viewer.scroll == nil {
		viewer.scroll = container.NewScroll(viewer.gallery)
	}
	// The size watcher sits below the scroll container (never intercepting
	// pointer events) and reports grid-width changes so the pagination links
	// can be rebuilt for the new window width.
	if viewer.sizeWatcher == nil {
		viewer.sizeWatcher = newSizeWatcher(func(width float32) {
			if viewer.galleryLoaded && viewer.bottomBar != nil {
				viewer.buildPagination()
			}
		})
	}
	// On mobile, overlay the grid with a horizontal-swipe catcher so the app can
	// page to an adjacent view; it forwards vertical drags back to the scroller.
	// The callbacks are read late (they may be wired after CreateView), so the
	// overlay is added whenever the platform uses drag gestures.
	grid := fyne.CanvasObject(container.NewStack(viewer.sizeWatcher, viewer.scroll))
	if viewer.platform != nil && viewer.platform.UsesMobileDragGestures() {
		grid = container.NewStack(viewer.sizeWatcher, viewer.scroll, newGridSwipeOverlay(viewer))
	}
	if viewer.Sidebar != nil {
		split := container.NewHSplit(viewer.Sidebar, grid)
		split.SetOffset(0.2)
		mainPage = split
	} else {
		mainPage = grid
	}

	// Lazily create the sidebar toggle button the first time a sidebar is
	// encountered. Update its label to reflect the current visibility state.
	hasSidebar := viewer.Sidebar != nil || viewer.sidebarStored != nil
	if hasSidebar && viewer.sidebarToggle == nil {
		viewer.sidebarToggle = widget.NewButton("◀", viewer.ToggleSidebar)
	}
	if viewer.sidebarToggle != nil {
		if viewer.Sidebar != nil {
			viewer.sidebarToggle.SetText("◀")
		} else {
			viewer.sidebarToggle.SetText("▶")
		}
	}

	// Create menu button if it doesn't exist yet
	if viewer.menuButton == nil {
		viewer.menuButton = widget.NewButton("☰", func() {
			viewer.showGalleryMenu()
		})
	}

	// Compose the bottom bar: sidebar toggle on the left, menu on the right,
	// pagination in the center. The buttons are placed outside bottomBar.Objects
	// so the existing Objects[:2] (Prev/Next) trimming in LoadGallery is unaffected.
	var bottom fyne.CanvasObject = viewer.bottomBar
	if viewer.bottomBar != nil {
		var left, right fyne.CanvasObject
		if viewer.sidebarToggle != nil {
			left = viewer.sidebarToggle
		}
		right = viewer.menuButton
		bottom = container.NewBorder(nil, nil, left, right, viewer.bottomBar)
	}

	viewer.Content.Objects = []fyne.CanvasObject{container.NewBorder(nil, bottom, nil, nil, mainPage)}
}

// showGalleryMenu displays a popup menu with gallery options.
func (viewer *Gallery) showGalleryMenu() {
	// Build menu items
	var items []*fyne.MenuItem

	// Toggle filenames option
	filenameLabel := "Show filenames"
	if viewer.layout != nil && viewer.layout.showLabels {
		filenameLabel = "Hide filenames"
	}
	items = append(items, fyne.NewMenuItem(filenameLabel, func() {
		if viewer.layout != nil {
			viewer.layout.ToggleLabels()
		}
	}))

	// Image info overlay: only meaningful while a single image is displayed.
	if viewer.imageViewActive() {
		infoLabel := "Image info"
		if viewer.infoOverlay != nil && viewer.infoOverlay.Visible() {
			infoLabel = "Hide image info"
		}
		items = append(items, fyne.NewMenuItem(infoLabel, func() {
			viewer.ToggleInfoOverlay()
		}))
	}

	// Future options can be added here:
	// - Sort order
	// - Grid size
	// - Refresh thumbnails
	// - Settings

	// Create and show popup menu
	menu := fyne.NewMenu("", items...)
	popUpMenu := widget.NewPopUpMenu(menu, viewer.window.Canvas())

	// Position the menu at the bottom-right, near the button that opened it:
	// the floating button while the image view is on screen, otherwise the
	// bottom-bar button.
	anchor := viewer.menuButton
	if viewer.imageViewActive() && viewer.imageMenuButton != nil {
		anchor = viewer.imageMenuButton
	}
	buttonPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	buttonSize := anchor.Size()

	// Position menu above the button, aligned to the right edge
	menuPos := fyne.NewPos(
		buttonPos.X+buttonSize.Width-popUpMenu.Size().Width,
		buttonPos.Y-popUpMenu.Size().Height,
	)
	popUpMenu.ShowAtPosition(menuPos)
}

// ToggleLabels shows or hides the name label under each tile, the programmatic
// equivalent of the bottom-bar menu's "Show filenames" item. Embedding apps
// can call it once after Init to default labels on (e.g. an album cover wall,
// where the title is essential). Safe to call before any tiles exist: the flag
// is flipped and subsequently created tiles honor it.
func (viewer *Gallery) ToggleLabels() {
	if viewer.layout != nil {
		viewer.layout.ToggleLabels()
	}
}

func (viewer *Gallery) LoadGallery() {
	viewer.loading.Wait()
	// Snapshot the slice header on the calling (UI) goroutine: a later tag
	// query swaps viewer.imageFiles from its own goroutine, and reading the
	// header inside the spawned goroutine would race with that swap.
	imageFiles := viewer.imageFiles
	viewer.layout.placement.Add(1)
	go func() {
		defer viewer.layout.placement.Done()
		viewer.layout.PlaceTiles(imageFiles)
	}()
	viewer.buildPagination()
	viewer.galleryLoaded = true
	// A fresh gallery view (new page, new directory, new query) starts at
	// the top; showGallery's tile-tracking restore is the only path that
	// scrolls anywhere else.
	if viewer.scroll != nil {
		viewer.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	}
}

func (viewer *Gallery) CurrentImageInfo() *ImageInfo {
	return viewer.CurrentImageView.info
}

// ImageCount reports how many images the viewer currently holds.
func (viewer *Gallery) ImageCount() int {
	return len(viewer.imageFiles)
}

// RemoveItem removes info from the viewer's image list and fixes the order
// indices of remaining items. Call ChangeGallery afterwards to refresh the UI.
func (viewer *Gallery) RemoveItem(info *ImageInfo) {
	for i, item := range viewer.imageFiles {
		if item == info {
			viewer.imageFiles = append(viewer.imageFiles[:i], viewer.imageFiles[i+1:]...)
			for j := i; j < len(viewer.imageFiles); j++ {
				viewer.imageFiles[j].order = j
			}
			return
		}
	}
}

func (viewer *Gallery) ChangeGallery() {
	// empty channel before changing page, then wait for workers to finish.
	// Each drained item was Add()ed in PlaceTiles, so it must be Done here
	// (workers never see it).
	for len(viewer.layout.imagesToLoad) > 0 {
		<-viewer.layout.imagesToLoad
		viewer.layout.currentlyLoading.Done()
	}
	viewer.layout.currentlyLoading.Wait()

	viewer.currentPage = 0
	viewer.layout.offset = 0
	// The image list was replaced; any remembered open entry no longer
	// applies to it.
	viewer.openedInfo = nil
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) ChangePage(page int) {
	if page < 0 || page > viewer.maxPages-1 {
		return
	}
	// empty channel before changing page, then wait for workers to finish.
	// Each drained item was Add()ed in PlaceTiles, so it must be Done here
	// (workers never see it).
	for len(viewer.layout.imagesToLoad) > 0 {
		<-viewer.layout.imagesToLoad
		viewer.layout.currentlyLoading.Done()
	}
	viewer.layout.currentlyLoading.Wait()

	viewer.currentPage = page
	viewer.layout.offset = page * viewer.config.General.ImagesPerPage
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) LoadImageToCache(info *ImageInfo) *ImageView {
	viewer.cacheMu.Lock()
	defer viewer.cacheMu.Unlock()
	if x, ok := viewer.cache[info.Path]; ok == false {
		if viewer.OnTapped != nil {
			info.OnTapped = viewer.OnTapped
		}
		if viewer.OnSwipeUp != nil {
			info.OnSwipeUp = viewer.OnSwipeUp
		}
		if viewer.OnDoubleTapped != nil {
			info.OnDoubleTapped = viewer.OnDoubleTapped
		}
		if info.OnDoubleTapped != nil {
			info.OnDoubleTapped = viewer.ToggleFullscreen
		}
		img := NewImageView(info, viewer.window.Canvas().Size(), true, viewer.window, viewer.window.Canvas().Focus, viewer.platform)
		// Update window title when image size changes (zoom, window resize).
		// Called from ImageLayout.Layout and ImageView zoom methods, which
		// already run on the UI thread.
		img.changeFn = func() {
			viewer.window.SetTitle("imgview - " + img.GetImageInfo())
		}
		// Wire fullscreen toggle for mobile tap-to-hide-system-bars behavior.
		img.toggleFullscreen = viewer.ToggleFullscreen
		viewer.cache[info.Path] = img
		return img
	} else {
		// A cached view may have had its bitmap released when returning to
		// the gallery (showGallery frees it to cut memory). Reload it,
		// otherwise the previously seen image comes back blank.
		if x.fyneImage == nil || x.fyneImage.Image == nil {
			x.loadOrPlaceholder()
		}
		return x
	}
}

func (viewer *Gallery) ToggleFullscreen() {
	viewer.isFullscreen = !viewer.isFullscreen
	viewer.CurrentImageView.fillWindow = false
	viewer.window.SetFullScreen(viewer.isFullscreen)
	viewer.CurrentImageView.fillWindow = true
	viewer.Content.Refresh()
}

func (viewer *Gallery) RunCmdA() {
	cmd := strings.ReplaceAll(viewer.config.Image.CmdA, "$FILE", viewer.CurrentImageView.info.Path)
	c := exec.Command("/bin/sh", "-c", cmd)
	go func() {
		// Command runs in background; configure command to show its own output if needed
		c.Run()
	}()
}

func (viewer *Gallery) SaveImage() {
	if viewer.config.Image.SaveDir != "" {
		info := viewer.CurrentImageView.info
		if r, err := info.GetReader(); err == nil {
			if data, err := io.ReadAll(r); err == nil {
				filename := filepath.Base(info.Path)
				var dest string
				if info.InputIsArchive {
					dest = filepath.Join(viewer.config.Image.SaveDir, info.archiveName+"-"+filename)
				} else {
					dest = filepath.Join(viewer.config.Image.SaveDir, filename)
				}
				// Write file; silently ignore errors (user will notice if file doesn't appear)
				os.WriteFile(dest, data, 0755)
			}
		}
	}
}

func (viewer *Gallery) NextImage() *ImageInfo {
	viewer.loading.Wait()
	if len(viewer.imageFiles) == 0 {
		return nil
	}
	nextImg := viewer.currentIndex + 1
	if nextImg >= len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *Gallery) PrevImage() *ImageInfo {
	viewer.loading.Wait()
	if len(viewer.imageFiles) == 0 {
		return nil
	}
	nextImg := viewer.currentIndex - 1
	if nextImg < 0 {
		nextImg = 0
	}
	if nextImg >= len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *Gallery) InitHotkeys() {
	viewer.hotkeys = []Hotkey{}
	bindings := viewer.config.Image

	add := func(h Hotkey) {
		viewer.hotkeys = append(viewer.hotkeys, h)
	}

	for _, x := range bindings.NextImage {
		add(Hotkey{x, func() {
			viewer.ChangeImage(viewer.NextImage())
		}})
	}
	for _, x := range bindings.PreviousImage {
		add(Hotkey{x, func() {
			viewer.ChangeImage(viewer.PrevImage())
		}})
	}
	for _, x := range bindings.RotateLeft {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.RotateLeft()
		}})
	}
	for _, x := range bindings.RotateRight {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.RotateRight()
		}})
	}
	for _, x := range bindings.OriginalSize {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.OriginalSize()
		}})
	}
	showGallery := viewer.showGallery
	for _, x := range bindings.ShowGallery {
		add(Hotkey{x, showGallery})
	}
	for _, x := range bindings.ToggleInfo {
		add(Hotkey{x, func() {
			viewer.ToggleInfoOverlay()
		}})
	}
	// Gallery navigation hotkeys (ScrollDown/ScrollUp/PathLevelUp) live in
	// TileLayout.InitHotkeys: KeyPress dispatches layout.hotkeys on every
	// platform, while viewer.hotkeys reach the desktop ImageView via focus
	// and the window handler only on mobile.
	// On mobile the Android/iOS back button sends key name "Back" to the
	// focused widget (ImageView). Map it to the same show-gallery action so
	// the user can return to the grid without a keyboard.
	if viewer.platform.ShouldRegisterBackButton() {
		add(Hotkey{"Back", showGallery})
	}
	for _, x := range bindings.Quit {
		add(Hotkey{x, func() {
			viewer.app.Quit()
		}})
	}
	for _, x := range bindings.FillWindow {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.fillWindow = true
			viewer.Content.Refresh()
		}})
	}
	for _, x := range bindings.Filtering {
		add(Hotkey{x, func() {
			if viewer.CurrentImageView.fyneImage.ScaleMode == canvas.ImageScaleFastest {
				viewer.CurrentImageView.fyneImage.ScaleMode = canvas.ImageScalePixels
			} else {
				viewer.CurrentImageView.fyneImage.ScaleMode = canvas.ImageScaleFastest
			}
			viewer.Content.Refresh()
		}})
	}
	for _, x := range bindings.FullScreen {
		add(Hotkey{x, func() {
			viewer.ToggleFullscreen()
		}})
	}
	for _, x := range bindings.RunCmda {
		add(Hotkey{x, func() {
			viewer.RunCmdA()
		}})
	}
	for _, x := range bindings.SaveImage {
		add(Hotkey{x, func() {
			viewer.SaveImage()
		}})
	}
}

// showGallery returns to the gallery grid from the single-image or video view,
// releasing playback/image resources and restoring the prior scroll position.
func (viewer *Gallery) showGallery() {
	if viewer.platform.ShouldExitFullscreenOnGalleryView() && viewer.isFullscreen {
		viewer.isFullscreen = false
		viewer.window.SetFullScreen(false)
	}
	// Stop and release a playing video before returning to the grid.
	if viewer.currentVideo != nil {
		viewer.currentVideo.Close()
		viewer.currentVideo = nil
		if viewer.videoOnClose != nil {
			viewer.videoOnClose()
			viewer.videoOnClose = nil
		}
	}
	// Release the current full-size image to free memory when returning to gallery
	if viewer.CurrentImageView != nil && viewer.CurrentImageView.fyneImage != nil {
		viewer.CurrentImageView.fullImage = nil
		viewer.CurrentImageView.fyneImage.Image = nil
		viewer.CurrentImageView.fyneImage.Refresh()
	}
	// The info overlay belongs to the image view; close it when leaving.
	if viewer.infoOverlay != nil {
		viewer.infoOverlay.Hide()
	}
	// Switch back to the page that holds the opened entry before the tiles
	// are placed, so the grid ends up showing the image the user came from.
	// The entry may have moved to a different page while it was open (next/
	// prev navigation, or a resized window re-slicing the pages).
	pageBefore := viewer.currentPage
	if viewer.openedInfo != nil && len(viewer.imageFiles) > 0 {
		idx := viewer.openedInfo.order
		if idx < 0 || idx >= len(viewer.imageFiles) || viewer.imageFiles[idx] != viewer.openedInfo {
			idx = -1
			for i, item := range viewer.imageFiles {
				if item == viewer.openedInfo {
					idx = i
					break
				}
			}
		}
		if idx >= 0 {
			if ipp := viewer.config.General.ImagesPerPage; ipp > 0 {
				viewer.currentPage = idx / ipp
				viewer.layout.offset = viewer.currentPage * ipp
			}
		}
	}
	reloaded := !viewer.galleryLoaded || viewer.currentPage != pageBefore
	if reloaded {
		// The page is about to be replaced: hand the tile's page-relative
		// index to PlaceTiles, which scrolls it into view once the new
		// page's tiles are positioned. Must be set before LoadGallery
		// spawns the placement goroutine.
		if viewer.openedInfo != nil && viewer.layout != nil {
			if idx := viewer.openedInfo.order - viewer.layout.offset; idx >= 0 {
				viewer.layout.pendingReveal = idx
			}
		}
		viewer.LoadGallery()
	} else {
		viewer.buildPagination()
	}
	viewer.CreateView()
	// On an unchanged page the tiles are already positioned: scroll the
	// opened tile into view directly (re-laying out first in case the
	// window was resized while the image was open).
	if !reloaded && viewer.scroll != nil && viewer.openedInfo != nil && viewer.layout != nil {
		idx := viewer.openedInfo.order - viewer.layout.offset
		if idx >= 0 && idx < len(viewer.layout.tiles) {
			viewer.layout.relayoutGrid()
			tile := viewer.layout.tiles[idx]
			viewer.layout.scrollToOffset(tile.Position().Y, tile.Size().Height)
		} else {
			viewer.scroll.ScrollToOffset(viewer.savedScrollOffset)
		}
	} else if !reloaded && viewer.scroll != nil {
		viewer.scroll.ScrollToOffset(viewer.savedScrollOffset)
	}
	viewer.window.SetTitle("imgview")
	viewer.window.SetContent(viewer.Content)
}

// ShowVideo plays a video in the main window (mirroring ChangeImage's in-window
// swap) instead of spawning a separate window. On mobile it auto-enters
// fullscreen. onClose, if non-nil, runs after the player is closed (temp-file
// cleanup). Must be called on the UI goroutine.
func (viewer *Gallery) ShowVideo(player *mpvplayer.MPVPlayer, displayName string, onClose func()) {
	v := mpvplayer.NewVideo(player)
	viewer.currentVideo = v
	viewer.videoOnClose = onClose
	v.OnFullscreen = viewer.toggleVideoFullscreen
	if viewer.scroll != nil {
		viewer.savedScrollOffset = viewer.scroll.Offset
	}
	viewer.window.SetTitle("Video: " + displayName)
	viewer.Content.Objects = []fyne.CanvasObject{v}
	viewer.Content.Refresh()
	if viewer.platform.ShouldAutoFullscreen() && !viewer.isFullscreen {
		viewer.isFullscreen = true
		viewer.window.SetFullScreen(true)
		v.SetFullscreen(true)
	}
}

// toggleVideoFullscreen toggles OS window fullscreen for the video view and
// informs the widget so tap-to-toggle-controls activates. Kept separate from
// ToggleFullscreen, which dereferences CurrentImageView (nil during video).
func (viewer *Gallery) toggleVideoFullscreen() {
	if viewer.currentVideo == nil {
		return
	}
	viewer.isFullscreen = !viewer.isFullscreen
	viewer.window.SetFullScreen(viewer.isFullscreen)
	viewer.currentVideo.SetFullscreen(viewer.isFullscreen)
}

func (viewer *Gallery) ChangeImage(info *ImageInfo) {
	if info == nil {
		return
	}
	if info.OnOpen != nil {
		info.OnOpen()
		return
	}
	// Video files cannot be decoded as images; the caller's tile-onclick
	// handler (e.g. in cmd/imgview) is responsible for starting the player.
	if info.InputIsVideo {
		return
	}
	img := viewer.LoadImageToCache(info)
	viewer.currentPath = filepath.Dir(info.Path)
	viewer.CurrentImageView = img
	// Remember which gallery entry is open so showGallery can switch back to
	// its page and scroll it into view, even across pages.
	viewer.openedInfo = info
	// Save the gallery scroll position so it can be restored when the user
	// navigates back from the single-image view.
	if viewer.scroll != nil {
		viewer.savedScrollOffset = viewer.scroll.Offset
	}
	img.fillWindow = true
	img.container = viewer.Content
	img.hotkeys = viewer.hotkeys
	img.nextFn = func() { viewer.ChangeImage(viewer.NextImage()) }
	img.prevFn = func() { viewer.ChangeImage(viewer.PrevImage()) }
	viewer.CurrentImage.Objects = []fyne.CanvasObject{img}
	if info.order != -1 {
		viewer.currentIndex = info.order
	}
	// Prefetch the next image off-thread. NextImage is evaluated here, on
	// the calling (UI) goroutine, after currentIndex is updated: reading
	// imageFiles/currentIndex from a background goroutine races with tag
	// queries swapping viewer.imageFiles and can index out of range.
	if next := viewer.NextImage(); next != nil && !next.InputIsVideo {
		go func() { viewer.LoadImageToCache(next) }()
	}
	viewer.Content.Objects = []fyne.CanvasObject{viewer.CurrentImage}
	// Float the ☰ menu button over the image so the gallery menu (image
	// info, filename toggle) stays reachable while an image is open.
	if viewer.imageMenuOverlay != nil {
		viewer.Content.Objects = append(viewer.Content.Objects, viewer.imageMenuOverlay)
	}
	// Keep the info overlay across next/prev navigation: re-stack it above
	// the new image and retarget its contents.
	if viewer.infoOverlay != nil && viewer.infoOverlay.Visible() {
		viewer.Content.Objects = append(viewer.Content.Objects, viewer.infoOverlay)
		viewer.infoOverlay.setInfo(info)
	}
	viewer.Content.Refresh()
	if viewer.platform.ShouldAutoFullscreen() && !viewer.isFullscreen {
		viewer.isFullscreen = true
		viewer.window.SetFullScreen(true)
	}
	if viewer.OnImageChange != nil {
		viewer.OnImageChange(info)
	}
	img.changeFn()
}

// func (viewer *Gallery) SetImage() {
// 	viewer.window.SetContent(viewer.imageContainer)
// 	viewer.window.Canvas().Focus(viewer.currentImage)
// }

func (viewer *Gallery) ReadImageDir(absolutePath string, selected *ImageInfo) {
	viewer.loading.Add(1)
	defer viewer.loading.Done()

	viewer.currentPath = absolutePath
	dir, _ := os.ReadDir(absolutePath)
	i := 0
	for _, x := range dir {
		fullPath := filepath.Join(absolutePath, x.Name())
		switch true {
		case x.IsDir():
			subDirAbsPath := filepath.Join(absolutePath, x.Name())
			subDir, _ := os.ReadDir(subDirAbsPath)
			var previews []string
			for _, y := range subDir {
				if y.IsDir() {
					continue
				}
				subFilePath := filepath.Join(subDirAbsPath, y.Name())
				if IsImageFromPath(subFilePath) {
					previews = append(previews, subFilePath)
				}
			}
			if len(previews) > 0 {
				info := NewImageInfo(i, previews[0])
				info.InputIsDir = true
				info.DirPath = subDirAbsPath
				info.PreviewPaths = previews
				info.DisplayName = x.Name()
				viewer.imageFiles = append(viewer.imageFiles, info)
				i++
			}

		case IsArchiveFromPath(fullPath):
			// Pass only the filename (nil stream): the library then manages
			// its own file handles. Passing an *os.File and closing it here
			// leaves the returned FS unable to read members later ("file
			// already closed"), so member thumbnails never loaded.
			fsys, err := archives.FileSystem(context.Background(), fullPath, nil)
			if err != nil {
				continue
			}
			// Collect every image member so the archive tile can swipe
			// through them; the first image becomes the gallery entry.
			var previews []string
			fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
				if x.IsDir() {
					return nil
				}
				file, err := fsys.Open(path)
				if err != nil {
					return err
				}
				if IsImage(file) {
					previews = append(previews, path)
				}
				file.Close()
				return nil
			})
			if len(previews) > 0 {
				info := NewImageInfo(i, previews[0])
				info.InputIsDir = true
				info.DirPath = fullPath
				info.FullPath = fullPath
				info.archiveFile = fsys
				info.archiveName = filepath.Base(fullPath)
				info.InputIsArchive = true
				info.ShowArchive = true
				info.DisplayName = filepath.Base(fullPath)
				info.PreviewPaths = previews
				viewer.imageFiles = append(viewer.imageFiles, info)
				i++
			}

		case IsVideoFromPath(fullPath):
			info := NewImageInfo(i, fullPath)
			info.InputIsVideo = true
			info.DirPath = absolutePath
			info.DisplayName = x.Name()
			viewer.imageFiles = append(viewer.imageFiles, info)
			i++

		case IsImageFromPath(fullPath):
			if selected != nil && selected.Path == fullPath {
				selected.order = i
				selected.DirPath = absolutePath
				selected.DisplayName = x.Name()
				viewer.currentIndex = i
				viewer.imageFiles = append(viewer.imageFiles, selected)
			} else {
				info := NewImageInfo(i, fullPath)
				info.DirPath = absolutePath
				info.DisplayName = x.Name()
				viewer.imageFiles = append(viewer.imageFiles, info)
			}
			i++
		}
	}

	// Sort: primary key = DirPath (groups subdirs and archives together),
	// secondary key = filename (alphabetical within each group).
	sort.Slice(viewer.imageFiles, func(i, j int) bool {
		a, b := viewer.imageFiles[i], viewer.imageFiles[j]
		if a.DirPath != b.DirPath {
			return a.DirPath < b.DirPath
		}
		return filepath.Base(a.Path) < filepath.Base(b.Path)
	})
	for i := range viewer.imageFiles {
		viewer.imageFiles[i].order = i
	}
}

type CustomReader interface {
	GetReader() (io.ReadSeeker, error)
	Path() string // Used for caching and identification
}

// DisplayNamer is an optional CustomReader interface for entries that have a
// display-friendly name (e.g. a URI that knows its original filename). When
// not implemented, filepath.Base(Path()) is used as a fallback.
type DisplayNamer interface {
	DisplayName() string
}

// Subtitler is an optional CustomReader interface for entries that have a
// secondary label line (e.g. an album's artist under its title). When it
// returns a non-empty string, the tile shows two rows: DisplayName in bold on
// top and the subtitle in normal weight below.
type Subtitler interface {
	Subtitle() string
}

// Openable is an optional CustomReader behavior for entries that are not
// viewable images (e.g. a directory): Open replaces the default image
// display when the entry is opened.
type Openable interface {
	Open()
}

// VideoFile is an optional CustomReader interface. When a reader implements
// it and returns true, the gallery shows a video-placeholder thumbnail and
// the tile click handler is expected to open a video player instead of
// trying to decode the content as an image.
type VideoFile interface {
	IsVideo() bool
}

// VideoStreamer is an optional CustomReader interface for video entries that
// can be played or thumbnailed directly from an HTTP URL (no download
// required). When implemented, GetThumbnail passes StreamURL to the libmpv
// extractor instead of reading the full content through GetReader.
type VideoStreamer interface {
	StreamURL() string
}

// DimensionProvider is an optional CustomReader behavior for entries whose
// original pixel dimensions are known before the thumbnail blob is fetched.
// ReadCustom uses these to pre-populate ImageInfo.Width and ImageInfo.Height,
// so placeholder tiles carry the correct aspect ratio from the first layout
// pass and no reflow occurs as thumbnails arrive.
type DimensionProvider interface {
	Dimensions() (width, height int)
}

// PreviewProvider is an optional CustomReader behavior for entries that
// represent a browsable collection of images (e.g. a tie directory or an
// archive blob). Previews returns readers for the collection's images; the
// gallery tile shows one as its thumbnail (badged with a folder icon) and
// horizontal swipes cycle through them. It is called lazily from loader
// goroutines, so network I/O is acceptable. An error or empty result falls
// back to the caller's Thumbnailer (e.g. a static folder icon).
type PreviewProvider interface {
	Previews() ([]CustomReader, error)
}

// CoverProvider is an optional CustomReader behavior for collection entries
// (directories, archives) that can supply a ready-made cover thumbnail
// WITHOUT enumerating the collection — e.g. a server-cached cover for a tie
// archive, which avoids downloading the whole archive blob just to thumbnail
// its tile. The gallery uses the cover only for previewIndex 0 (the tile's
// initial view); swipe cycling still goes through PreviewProvider.
//
// CoverThumbnail returns a ready-scaled thumbnail JPEG or an error. On error
// the gallery falls back to the PreviewProvider path, and after generating
// the first preview itself it calls StoreCoverThumbnail with the plain
// (pre-badge) JPEG so the cover is cached for next time.
type CoverProvider interface {
	CoverThumbnail() (io.ReadSeeker, error)
	StoreCoverThumbnail(jpegBytes []byte)
}

func (viewer *Gallery) ReadCustom(readers []CustomReader) {
	viewer.loading.Add(1)
	defer viewer.loading.Done()

	// Build into a local slice and swap, so overlapping tie queries can't
	// interleave appends into viewer.imageFiles (last query wins).
	imageFiles := make([]*ImageInfo, 0, len(readers))
	for i, r := range readers {
		info := NewImageInfoCustomReader(i, r)
		info.Path = r.Path()
		// Set display name: prefer DisplayNamer interface, fallback to basename.
		// For URIs, filepath.Base may not work correctly, so DisplayNamer is important.
		if dn, ok := r.(DisplayNamer); ok {
			info.DisplayName = dn.DisplayName()
		} else {
			// Fallback: extract basename from path
			// This works for file paths but may show full URI for content:// schemes
			base := filepath.Base(info.Path)
			// If base is still very long (e.g. a URI), try to extract just the filename component
			if len(base) > 50 && strings.Contains(base, "%2F") {
				// URI-encoded path separator - decode and take last component
				parts := strings.Split(base, "%2F")
				if len(parts) > 0 {
					base = parts[len(parts)-1]
				}
			}
			info.DisplayName = base
		}
		if st, ok := r.(Subtitler); ok {
			info.Subtitle = st.Subtitle()
		}
		if o, ok := r.(Openable); ok {
			info.OnOpen = o.Open
		}
		if vf, ok := r.(VideoFile); ok && vf.IsVideo() {
			info.InputIsVideo = true
		}
		if dp, ok := r.(DimensionProvider); ok {
			info.Width, info.Height = dp.Dimensions()
		}
		imageFiles = append(imageFiles, info)
	}
	viewer.imageFiles = imageFiles
}

// ReadCustomAsync replaces the viewer's images with the readers produced by
// fetch, which runs in a goroutine. LoadGallery blocks until fetch and the
// swap have completed. A nil result from fetch (e.g. after it logged an
// error) leaves the current images untouched.
func (viewer *Gallery) ReadCustomAsync(fetch func() []CustomReader) {
	// Add before spawning the goroutine so LoadGallery's loading.Wait()
	// actually blocks until the query results have been turned into images.
	viewer.loading.Add(1)
	go func() {
		defer viewer.loading.Done()
		readers := fetch()
		if readers == nil {
			return
		}
		viewer.CustomReaders = readers
		viewer.ReadCustom(readers)
	}()
}

func (viewer *Gallery) ReadImageArchive(zipFile string) {
	viewer.loading.Add(1)
	defer viewer.loading.Done()

	// Pass only the filename (nil stream): the library manages its own file
	// handles, so the returned FS stays usable after this function returns.
	fsys, err := archives.FileSystem(context.Background(), zipFile, nil)
	if err != nil {
		// Archive cannot be opened; imageFiles stays empty
		return
	}
	i := 0
	fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
		if x.IsDir() {
			return nil
		}
		file, err := fsys.Open(path)
		if err != nil {
			// Skip files that can't be opened
			return nil
		}
		if IsImage(file) {
			info := NewImageInfo(i, path)
			info.InputIsArchive = true
			info.archiveName = filepath.Base(zipFile)
			info.archiveFile = fsys
			viewer.imageFiles = append(viewer.imageFiles, info)
			i++
		}
		return nil
	})
	sort.Slice(viewer.imageFiles, func(i, j int) bool {
		return viewer.imageFiles[i].Path < viewer.imageFiles[j].Path
	})
	for i, _ := range viewer.imageFiles {
		viewer.imageFiles[i].order = i
	}
}
