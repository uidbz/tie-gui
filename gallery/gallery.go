package gallery

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mholt/archives"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
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
	// OnTileSecondaryTapped is called when a gallery tile receives a secondary
	// tap (right-click). Set by the caller to implement context actions such as
	// de-import. Receives the full Tile so the caller can inspect Info.
	OnTileSecondaryTapped func(*Tile)

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

	gallery           *fyne.Container
	imageFiles        []*ImageInfo
	layout            *TileLayout
	window            fyne.Window
	app               fyne.App
	platform          *Platform
	currentIndex      int
	currentPath       string
	cache             map[string]*ImageView
	scroll            *container.Scroll
	savedScrollOffset fyne.Position
	hotkeys       []Hotkey
	loading       sync.WaitGroup
	galleryLoaded bool
	config        Config
	bottomBar     *fyne.Container
	sidebarStored fyne.CanvasObject // saved sidebar when hidden by the toggle
	sidebarToggle *widget.Button    // ◀/▶ button in the bottom bar; nil when no sidebar
	menuButton    *widget.Button    // ☰ menu button in the bottom bar (right side)
	refreshThumbs bool
	tileOnclick   func(*Tile)
	currentPage   int
	maxPages      int
	isFullscreen  bool
}

// NewGallery creates a Gallery instance but does not wire hotkeys or layout.
// Call Init() after setting optional fields (Sidebar, Thumbnailer, callbacks)
// to complete initialization.
//
// The two-step construction pattern exists because applications need to
// customize the Gallery between creation and initialization:
//   - tieview sets Sidebar to the tag filter panel
//   - tieview sets Thumbnailer to the filehost thumbnailer
//   - Both mains set OnImageChange, OnTapped, etc.
//
// These fields must be set before Init() is called because Init() wires hotkeys
// and layout, which may depend on the configuration being complete.
//
// Example usage:
//
//	viewer := gallery.NewGallery(app, window, config, tileOnclick)
//	viewer.Sidebar = makeSidebar(...)        // tieview only
//	viewer.Thumbnailer = makeThumbnailer()   // tieview only
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
	viewer.ReadImageDir(path, nil)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) ShowImageArchive(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.currentPage = 0
	viewer.layout.offset = 0
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
	if viewer.Sidebar != nil {
		split := container.NewHSplit(viewer.Sidebar, viewer.scroll)
		split.SetOffset(0.2)
		mainPage = split
	} else {
		mainPage = viewer.scroll
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

	// Future options can be added here:
	// - Sort order
	// - Grid size
	// - Refresh thumbnails
	// - Settings

	// Create and show popup menu
	menu := fyne.NewMenu("", items...)
	popUpMenu := widget.NewPopUpMenu(menu, viewer.window.Canvas())

	// Position the menu at the bottom-right, near the menu button
	// Get the button position and size
	buttonPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(viewer.menuButton)
	buttonSize := viewer.menuButton.Size()

	// Position menu above the button, aligned to the right edge
	menuPos := fyne.NewPos(
		buttonPos.X+buttonSize.Width-popUpMenu.Size().Width,
		buttonPos.Y-popUpMenu.Size().Height,
	)
	popUpMenu.ShowAtPosition(menuPos)
}

func (viewer *Gallery) LoadGallery() {
	viewer.loading.Wait()
	// Snapshot the slice header on the calling (UI) goroutine: a later tag
	// query swaps viewer.imageFiles from its own goroutine, and reading the
	// header inside the spawned goroutine would race with that swap.
	imageFiles := viewer.imageFiles
	go func() {
		viewer.layout.PlaceTiles(imageFiles)
	}()
	if viewer.bottomBar == nil {
		prevPage := widget.NewHyperlink("Prev", nil)
		prevPage.OnTapped = func() { viewer.ChangePage(viewer.currentPage - 1) }
		nextPage := widget.NewHyperlink("Next", nil)
		nextPage.OnTapped = func() { viewer.ChangePage(viewer.currentPage + 1) }
		viewer.bottomBar = container.NewHBox(prevPage, nextPage)
	}
	imagesPerPage := viewer.config.General.ImagesPerPage
	viewer.maxPages = len(viewer.imageFiles)/imagesPerPage + 1
	viewer.bottomBar.Objects = viewer.bottomBar.Objects[:2]
	for i := 0; i < viewer.maxPages; i++ {
		i := i
		start := i*imagesPerPage + 1
		end := start + imagesPerPage - 1
		if i == viewer.maxPages-1 {
			end = len(viewer.imageFiles)
		}
		page := widget.NewHyperlink(strconv.Itoa(start)+"-"+strconv.Itoa(end), nil)
		page.OnTapped = func() {
			viewer.ChangePage(i)
		}
		if i == viewer.currentPage {
			page.TextStyle.Bold = true
		}
		viewer.bottomBar.Add(page)
	}
	viewer.bottomBar.Refresh()
	viewer.galleryLoaded = true
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
	showGallery := func() {
		if viewer.platform.ShouldExitFullscreenOnGalleryView() && viewer.isFullscreen {
			viewer.isFullscreen = false
			viewer.window.SetFullScreen(false)
		}
		// Release the current full-size image to free memory when returning to gallery
		if viewer.CurrentImageView != nil && viewer.CurrentImageView.fyneImage != nil {
			viewer.CurrentImageView.fullImage = nil
			viewer.CurrentImageView.fyneImage.Image = nil
			viewer.CurrentImageView.fyneImage.Refresh()
		}
		if !viewer.galleryLoaded {
			viewer.LoadGallery()
		}
		viewer.CreateView()
		// Restore the scroll position that was active before entering the
		// single-image view.
		if viewer.scroll != nil {
			viewer.scroll.ScrollToOffset(viewer.savedScrollOffset)
		}
		viewer.window.SetTitle("imgview")
		viewer.window.SetContent(viewer.Content)
	}
	for _, x := range bindings.ShowGallery {
		add(Hotkey{x, showGallery})
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
