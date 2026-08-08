package gallery

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mholt/archiver/v4"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type Viewer struct {
	Content       *fyne.Container
	CurrentImage  *fyne.Container
	CustomReaders []CustomReader
	// Thumbnailer, when non-nil, supplies thumbnails for all items instead
	// of the local thumbnail directory (see GeneralConfig.ThumbnailDir).
	Thumbnailer Thumbnailer
	// Sidebar, when non-nil, is shown left of the gallery (e.g. a tag
	// selector) instead of a plain full-width gallery.
	Sidebar fyne.CanvasObject

	gallery          *fyne.Container
	imageFiles       []*ImageInfo
	layout           *TileLayout
	window           fyne.Window
	app              fyne.App
	currentIndex     int
	CurrentImageView *ImageView
	currentPath      string
	cache            map[string]*ImageView
	scroll           *container.Scroll
	hotkeys          []Hotkey
	loading          sync.WaitGroup
	galleryLoaded    bool
	config           Config
	bottomBar        *fyne.Container
	sidebarStored    fyne.CanvasObject // saved sidebar when hidden by the toggle
	sidebarToggle    *widget.Button    // ◀/▶ button in the bottom bar; nil when no sidebar
	refreshThumbs    bool

	tileOnclick       func(*Tile)
	OnTapped          func()
	OnDoubleTapped    func()
	OnTappedSecondary func()
	OnImageChange     func(info *ImageInfo)
	// OnTileSecondaryTapped is called when a gallery tile receives a secondary
	// tap (right-click). Set by the caller to implement context actions such as
	// de-import. Receives the full Tile so the caller can inspect Info.
	OnTileSecondaryTapped func(*Tile)

	currentPage  int
	maxPages     int
	isFullscreen bool
}

func NewViewer(app fyne.App, window fyne.Window, config Config, tileOnclick func(t *Tile)) *Viewer {
	iv := &Viewer{
		app:     app,
		window:  window,
		Content: container.NewStack([]fyne.CanvasObject{}...),
		// CurrentImage: container.NewBorder(nil, nil, nil, rect, container.New(&ImageLayout{}, []fyne.CanvasObject{}...)),
		CurrentImage:  container.New(&ImageLayout{}, []fyne.CanvasObject{}...),
		cache:         make(map[string]*ImageView),
		config:        config,
		tileOnclick:   tileOnclick,
		refreshThumbs: false,
	}

	return iv
}

func (viewer *Viewer) KeyPress(key *fyne.KeyEvent) {
	for _, x := range viewer.layout.hotkeys {
		if key.Name == x.Name {
			x.Function()
		}
	}
	// On mobile the image view is not focused (to suppress the soft keyboard),
	// so image-viewer hotkeys (including the Back key) never reach TypedKey.
	// Handle them here instead.
	if fyne.CurrentDevice().IsMobile() {
		for _, x := range viewer.hotkeys {
			if key.Name == x.Name {
				x.Function()
			}
		}
	}
}

func (viewer *Viewer) ShowImageDir(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.currentPage = 0
	viewer.layout.offset = 0
	viewer.ReadImageDir(path, nil)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Viewer) ShowImageArchive(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.currentPage = 0
	viewer.layout.offset = 0
	viewer.ReadImageArchive(path)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Viewer) Init() {
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
func (viewer *Viewer) ToggleSidebar() {
	if viewer.Sidebar != nil {
		viewer.sidebarStored = viewer.Sidebar
		viewer.Sidebar = nil
	} else {
		viewer.Sidebar = viewer.sidebarStored
	}
	viewer.CreateView()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Viewer) CreateView() {
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

	// Compose the bottom bar: toggle on the left edge, pagination filling the
	// rest. The toggle is placed outside bottomBar.Objects so the existing
	// Objects[:2] (Prev/Next) trimming in LoadGallery is unaffected.
	var bottom fyne.CanvasObject = viewer.bottomBar
	if viewer.sidebarToggle != nil && viewer.bottomBar != nil {
		bottom = container.NewBorder(nil, nil, viewer.sidebarToggle, nil, viewer.bottomBar)
	}

	viewer.Content.Objects = []fyne.CanvasObject{container.NewBorder(nil, bottom, nil, nil, mainPage)}
}

func (viewer *Viewer) LoadGallery() {
	viewer.loading.Wait()
	go func() {
		viewer.layout.PlaceTiles(viewer.imageFiles)
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
		viewer.bottomBar.AddObject(page)
	}
	viewer.bottomBar.Refresh()
	viewer.galleryLoaded = true
}

func (viewer *Viewer) CurrentImageInfo() *ImageInfo {
	return viewer.CurrentImageView.info
}

// ImageCount reports how many images the viewer currently holds.
func (viewer *Viewer) ImageCount() int {
	return len(viewer.imageFiles)
}

// RemoveItem removes info from the viewer's image list and fixes the order
// indices of remaining items. Call ChangeGallery afterwards to refresh the UI.
func (viewer *Viewer) RemoveItem(info *ImageInfo) {
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

func (viewer *Viewer) ChangeGallery() {
	// empty channel before changing page, then wait for workers to finish
	for len(viewer.layout.imagesToLoad) > 0 {
		<-viewer.layout.imagesToLoad
	}
	viewer.layout.currentlyLoading.Wait()

	viewer.currentPage = 0
	viewer.layout.offset = 0
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Viewer) ChangePage(page int) {
	if page < 0 || page > viewer.maxPages-1 {
		return
	}
	// empty channel before changing page, then wait for workers to finish
	for len(viewer.layout.imagesToLoad) > 0 {
		<-viewer.layout.imagesToLoad
	}
	fmt.Println("Currently waiting")
	viewer.layout.currentlyLoading.Wait()
	fmt.Println("finished waiting")

	viewer.currentPage = page
	viewer.layout.offset = page * viewer.config.General.ImagesPerPage
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Viewer) LoadImageToCache(info *ImageInfo) *ImageView {
	if x, ok := viewer.cache[info.Path]; ok == false {
		if viewer.OnTapped != nil {
			info.OnTapped = viewer.OnTapped
		}
		if viewer.OnDoubleTapped != nil {
			info.OnDoubleTapped = viewer.OnDoubleTapped
		}
		if info.OnDoubleTapped != nil {
			info.OnDoubleTapped = viewer.ToggleFullscreen
		}
		img := NewImageView(info, viewer.window.Canvas().Size(), true, viewer.window, viewer.window.Canvas().Focus)
		img.changeFn = func() {
			go func() {
				fyne.Do(func() {
					viewer.window.SetTitle("imgview - " + img.GetImageInfo())
				})
			}()
		}
		viewer.cache[info.Path] = img
		return img
	} else {
		return x
	}
}

func (viewer *Viewer) ToggleFullscreen() {
	viewer.isFullscreen = !viewer.isFullscreen
	viewer.CurrentImageView.fillWindow = false
	viewer.window.SetFullScreen(viewer.isFullscreen)
	viewer.CurrentImageView.fillWindow = true
	viewer.Content.Refresh()
}

func (viewer *Viewer) RunCmdA() {
	cmd := strings.ReplaceAll(viewer.config.Image.CmdA, "$FILE", viewer.CurrentImageView.info.Path)
	c := exec.Command("/bin/sh", "-c", cmd)
	go func() {
		if output, err := c.CombinedOutput(); err != nil {
			fmt.Println(err)
		} else {
			fmt.Println(output)
		}
	}()
}

func (viewer *Viewer) SaveImage() {
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
				if err := os.WriteFile(dest, data, 0755); err != nil {
					fmt.Println(err)
				}
			}
		}
	}
}

func (viewer *Viewer) NextImage() *ImageInfo {
	viewer.loading.Wait()
	nextImg := viewer.currentIndex + 1
	if nextImg == len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *Viewer) PrevImage() *ImageInfo {
	viewer.loading.Wait()
	nextImg := viewer.currentIndex - 1
	if nextImg < 0 {
		nextImg = 0
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *Viewer) InitHotkeys() {
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
		if fyne.CurrentDevice().IsMobile() && viewer.isFullscreen {
			viewer.isFullscreen = false
			viewer.window.SetFullScreen(false)
		}
		if !viewer.galleryLoaded {
			viewer.LoadGallery()
		}
		viewer.CreateView()
		viewer.window.SetTitle("imgview")
		viewer.window.SetContent(viewer.Content)
	}
	for _, x := range bindings.ShowGallery {
		add(Hotkey{x, showGallery})
	}
	// On mobile the Android/iOS back button sends key name "Back" to the
	// focused widget (ImageView). Map it to the same show-gallery action so
	// the user can return to the grid without a keyboard.
	if fyne.CurrentDevice().IsMobile() {
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

func (viewer *Viewer) ChangeImage(info *ImageInfo) {
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
	go func() {
		if next := viewer.NextImage(); !next.InputIsVideo {
			viewer.LoadImageToCache(next)
		}
	}()
	img.fillWindow = true
	img.container = viewer.Content
	img.hotkeys = viewer.hotkeys
	img.nextFn = func() { viewer.ChangeImage(viewer.NextImage()) }
	img.prevFn = func() { viewer.ChangeImage(viewer.PrevImage()) }
	viewer.CurrentImage.Objects = []fyne.CanvasObject{img}
	if info.order != -1 {
		viewer.currentIndex = info.order
	}
	viewer.Content.Objects = []fyne.CanvasObject{viewer.CurrentImage}
	viewer.Content.Refresh()
	if fyne.CurrentDevice().IsMobile() && !viewer.isFullscreen {
		viewer.isFullscreen = true
		viewer.window.SetFullScreen(true)
	}
	if viewer.OnImageChange != nil {
		viewer.OnImageChange(info)
	}
	img.changeFn()
}

// func (viewer *Viewer) SetImage() {
// 	viewer.window.SetContent(viewer.imageContainer)
// 	viewer.window.Canvas().Focus(viewer.currentImage)
// }

func (viewer *Viewer) ReadImageDir(absolutePath string, selected *ImageInfo) {
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
				if y.IsDir() || len(previews) >= 4 {
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
			fsys, err := archiver.FileSystem(context.Background(), fullPath)
			if err != nil {
				continue
			}
			fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
				if x.IsDir() {
					return nil
				}
				file, err := fsys.Open(path)
				if err != nil {
					return err
				}
				if IsImage(file) {
					info := NewImageInfo(i, path)
					info.InputIsDir = true
					info.DirPath = fullPath
					info.FullPath = fullPath
					info.archiveFile = fsys
					info.archiveName = filepath.Base(fullPath)
					info.InputIsArchive = true
					info.ShowArchive = true
					info.DisplayName = filepath.Base(fullPath)
					viewer.imageFiles = append(viewer.imageFiles, info)
					i++
					return fs.SkipDir
				}
				return nil
			})

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

func (viewer *Viewer) ReadCustom(readers []CustomReader) {
	viewer.loading.Add(1)
	defer viewer.loading.Done()

	// Build into a local slice and swap, so overlapping tie queries can't
	// interleave appends into viewer.imageFiles (last query wins).
	imageFiles := make([]*ImageInfo, 0, len(readers))
	for i, r := range readers {
		info := NewImageInfoCustomReader(i, r)
		info.Path = r.Path()
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
func (viewer *Viewer) ReadCustomAsync(fetch func() []CustomReader) {
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

func (viewer *Viewer) ReadImageArchive(zipFile string) {
	viewer.loading.Add(1)
	defer viewer.loading.Done()

	fsys, err := archiver.FileSystem(context.Background(), zipFile)
	if err != nil {
		fmt.Println(err)
		return
	}
	i := 0
	fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
		if x.IsDir() {
			return nil
		}
		file, err := fsys.Open(path)
		if err != nil {
			fmt.Println("Error opening:", x.Name())
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
