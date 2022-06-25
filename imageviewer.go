package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/mholt/archiver/v4"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type ImageViewer struct {
	gallery        *fyne.Container
	imageFiles     []ImageInfo
	imageContainer *fyne.Container
	layout         *TileLayout
	window         fyne.Window
	app            fyne.App
	currentIndex   int
	currentImage   *ImageView
	cache          map[string]*ImageView
	scroll         *container.Scroll
	hotkeys        []Hotkey
	loadingDir     sync.WaitGroup
	config         Config
	object         fyne.CanvasObject
	bottomBar      *fyne.Container
	currentPage    int
	maxPages       int
}

type Config struct {
	Image   ImageConfig
	Gallery GalleryConfig
	General GeneralConfig
}

type GeneralConfig struct {
	DefaultWidth  float32
	DefaultHeight float32
	TileWidth     float32
	TileGap       float32
	Workers       int
	ImagesPerPage int
}

type ImageConfig struct {
	NextImage     []fyne.KeyName
	PreviousImage []fyne.KeyName
	RotateLeft    []fyne.KeyName
	RotateRight   []fyne.KeyName
	OriginalSize  []fyne.KeyName
	FillWindow    []fyne.KeyName
	Filtering     []fyne.KeyName
	ShowGallery   []fyne.KeyName
	Quit          []fyne.KeyName
	FullScreen    []fyne.KeyName
}

type GalleryConfig struct {
	Quit       []fyne.KeyName
	ScrollDown []fyne.KeyName
	ScrollUp   []fyne.KeyName
}

func (viewer *ImageViewer) Init() {
	tileOnclick := func(t *Tile) {
		SetImage(viewer, t.info)
	}
	viewer.layout = NewTileLayout(viewer.config, viewer.window, viewer.app, viewer, tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)
	viewer.layout.grid = viewer.gallery
	viewer.layout.InitHotkeys()
	viewer.InitHotkeys()
}

func (viewer *ImageViewer) Load() {
	go func() {
		viewer.loadingDir.Wait()
		viewer.layout.PlaceTiles(viewer.imageFiles)
	}()
	prevPage := widget.NewHyperlink("Prev", nil)
	prevPage.OnTapped = func() { viewer.ChangePage(viewer.currentPage - 1) }
	nextPage := widget.NewHyperlink("Next", nil)
	nextPage.OnTapped = func() { viewer.ChangePage(viewer.currentPage + 1) }

	viewer.bottomBar = container.NewHBox(prevPage, nextPage)
	imagesPerPage := viewer.config.General.ImagesPerPage
	viewer.maxPages = len(viewer.imageFiles)/imagesPerPage + 1
	for i := 0; i < viewer.maxPages; i++ {
		i := i
		start := i * imagesPerPage
		page := widget.NewHyperlink(strconv.Itoa(start)+"-"+strconv.Itoa(start+imagesPerPage-1), nil)
		page.OnTapped = func() {
			viewer.ChangePage(i)
		}
		if i == viewer.currentPage {
			page.TextStyle.Bold = true
		}
		viewer.bottomBar.AddObject(page)
	}
	viewer.scroll = container.NewScroll(viewer.gallery)
	viewer.object = container.NewBorder(nil, viewer.bottomBar, nil, nil, viewer.scroll)
}

func (viewer *ImageViewer) ChangePage(page int) {
	if page < 0 || page > viewer.maxPages-1 {
		return
	}
	// empty channel before changing page, then wait for workers to finish
	for len(viewer.layout.imagesToLoad) > 0 {
		<-viewer.layout.imagesToLoad
	}
	viewer.layout.currentlyLoading.Wait()

	viewer.currentPage = page
	viewer.layout.offset = page * viewer.config.General.ImagesPerPage
	viewer.Load()
	viewer.window.SetContent(viewer.object)
}

func (viewer *ImageViewer) LoadImageToCache(info ImageInfo) *ImageView {
	if x, ok := viewer.cache[info.path]; ok == false {
		img := NewImageView(info, viewer.window.Canvas().Size(), true, true, true, viewer.window, viewer.window.Canvas().Focus)
		img.changeFn = func() {
			go func() {
				viewer.window.SetTitle("imgview - " + img.GetImageInfo())
			}()
		}
		viewer.cache[info.path] = img
		return img
	} else {
		return x
	}
}

func (viewer *ImageViewer) NextImage() ImageInfo {
	viewer.loadingDir.Wait()
	nextImg := viewer.currentIndex + 1
	if nextImg == len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *ImageViewer) PrevImage() ImageInfo {
	viewer.loadingDir.Wait()
	nextImg := viewer.currentIndex - 1
	if nextImg < 0 {
		nextImg = 0
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *ImageViewer) InitHotkeys() {
	viewer.hotkeys = []Hotkey{}
	bindings := viewer.config.Image

	add := func(h Hotkey) {
		viewer.hotkeys = append(viewer.hotkeys, h)
	}

	for _, x := range bindings.NextImage {
		add(Hotkey{x, func() {
			SetImage(viewer, viewer.NextImage())
		}})
	}
	for _, x := range bindings.PreviousImage {
		add(Hotkey{x, func() {
			SetImage(viewer, viewer.PrevImage())
		}})
	}
	for _, x := range bindings.RotateLeft {
		add(Hotkey{x, func() {
			viewer.currentImage.RotateLeft()
		}})
	}
	for _, x := range bindings.RotateRight {
		add(Hotkey{x, func() {
			viewer.currentImage.RotateRight()
		}})
	}
	for _, x := range bindings.OriginalSize {
		add(Hotkey{x, func() {
			viewer.currentImage.OriginalSize()
		}})
	}
	for _, x := range bindings.ShowGallery {
		add(Hotkey{x, func() {
			if viewer.scroll == nil {
				viewer.Load()
			}
			viewer.window.SetTitle("imgview")
			viewer.window.SetContent(viewer.object)
		}})
	}
	for _, x := range bindings.Quit {
		add(Hotkey{x, func() {
			viewer.app.Quit()
		}})
	}
	for _, x := range bindings.FillWindow {
		add(Hotkey{x, func() {
			viewer.currentImage.fillWindow = true
			viewer.imageContainer.Refresh()
		}})
	}
	for _, x := range bindings.Filtering {
		add(Hotkey{x, func() {
			if viewer.currentImage.fyneImage.ScaleMode == canvas.ImageScaleFastest {
				viewer.currentImage.fyneImage.ScaleMode = canvas.ImageScalePixels
			} else {
				viewer.currentImage.fyneImage.ScaleMode = canvas.ImageScaleFastest
			}
			viewer.imageContainer.Refresh()
		}})
	}
	for _, x := range bindings.FullScreen {
		add(Hotkey{x, func() {
			viewer.currentImage.fillWindow = false
			viewer.window.SetFullScreen(!viewer.window.FullScreen())
			viewer.currentImage.fillWindow = true
			viewer.imageContainer.Refresh()
		}})
	}
}

func SetImage(viewer *ImageViewer, info ImageInfo) {
	img := viewer.LoadImageToCache(info)
	viewer.currentImage = img
	go func() {
		viewer.LoadImageToCache(viewer.NextImage())
	}()
	img.fillWindow = true
	img.container = viewer.imageContainer
	img.hotkeys = viewer.hotkeys
	viewer.imageContainer.Objects = []fyne.CanvasObject{img}
	if info.order != -1 {
		viewer.currentIndex = info.order
	}
	viewer.window.SetContent(viewer.imageContainer)
	viewer.window.Canvas().Focus(img)
	viewer.imageContainer.Refresh()
	img.changeFn()
}

func (viewer *ImageViewer) ReadImageDir(absolutePath string, selected *ImageInfo) {
	defer viewer.loadingDir.Done()

	dir, _ := os.ReadDir(absolutePath)
	i := 0
	for _, x := range dir {
		if x.IsDir() {
			continue
		}
		fullpath := filepath.Join(absolutePath, x.Name())
		if IsImageFromPath(fullpath) {
			if selected != nil && selected.path == fullpath {
				selected.order = i
				viewer.currentIndex = i
				viewer.imageFiles = append(viewer.imageFiles, *selected)
			} else {
				viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
					path:  fullpath,
					order: i,
				})
			}
			i++
		}
	}
}

func (viewer *ImageViewer) ReadImageArchive(zipFile string) {
	defer viewer.loadingDir.Done()

	fsys, err := archiver.FileSystem(zipFile)
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
			viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
				inputIsArchive: true,
				path:           path,
				archiveFile:    fsys,
				order:          i,
			})
			i++
		}
		return nil
	})
	sort.Slice(viewer.imageFiles, func(i, j int) bool {
		return viewer.imageFiles[i].path < viewer.imageFiles[j].path
	})
	for i, _ := range viewer.imageFiles {
		viewer.imageFiles[i].order = i
	}
}
