package main

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

type ImageViewer struct {
	gallery        *fyne.Container
	imageFiles     []ImageInfo
	imageContainer *fyne.Container
	layout         *TileLayout
	window         fyne.Window
	app            fyne.App
	currentIndex   int
	currentImage   *ImageView
	currentPath    string
	cache          map[string]*ImageView
	scroll         *container.Scroll
	hotkeys        []Hotkey
	loadingDir     sync.WaitGroup
	config         Config
	object         fyne.CanvasObject
	bottomBar      *fyne.Container
	currentPage    int
	maxPages       int
	isFullscreen   bool
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
	RunCmda       []fyne.KeyName
	CmdA          string
	SaveImage     []fyne.KeyName
	SaveDir       string
}

type GalleryConfig struct {
	Quit        []fyne.KeyName
	ScrollDown  []fyne.KeyName
	ScrollUp    []fyne.KeyName
	PathLevelUp []fyne.KeyName
}

func (viewer *ImageViewer) ShowImageDir(path string) {
	viewer.loadingDir.Add(1)
	viewer.imageFiles = make([]ImageInfo, 0)
	go viewer.ReadImageDir(path, nil)
	viewer.Init()
	viewer.Load()
	viewer.window.SetContent(viewer.object)
}

func (viewer *ImageViewer) ShowImageArchive(path string) {
	viewer.loadingDir.Add(1)
	viewer.imageFiles = make([]ImageInfo, 0)
	go viewer.ReadImageArchive(path)
	viewer.Init()
	viewer.Load()
	viewer.window.SetContent(viewer.object)
}

func (viewer *ImageViewer) Init() {
	tileOnclick := func(t *Tile) {
		switch true {
		case t.info.inputIsDir:
			viewer.ShowImageDir(filepath.Dir(t.info.path))
		case t.info.showArchive:
			viewer.ShowImageArchive(t.info.fullPath)
		default:
			SetImage(viewer, t.info)
		}
	}
	viewer.layout = NewTileLayout(viewer.config, viewer.window, viewer.app, viewer, tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)
	viewer.layout.grid = viewer.gallery
	viewer.layout.InitHotkeys()
	viewer.InitHotkeys()
}

func (viewer *ImageViewer) Load() {
	viewer.loadingDir.Wait()
	go func() {
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
		img.OnDoubleClicked = viewer.ToggleFullscreen
		viewer.cache[info.path] = img
		return img
	} else {
		return x
	}
}

func (viewer *ImageViewer) ToggleFullscreen() {
	viewer.isFullscreen = !viewer.isFullscreen
	viewer.currentImage.fillWindow = false
	viewer.window.SetFullScreen(viewer.isFullscreen)
	viewer.currentImage.fillWindow = true
	viewer.imageContainer.Refresh()
}

func (viewer *ImageViewer) RunCmdA() {
	cmd := strings.ReplaceAll(viewer.config.Image.CmdA, "$FILE", viewer.currentImage.info.path)
	c := exec.Command("/bin/sh", append([]string{"-c", cmd})...)
	go func() {
		if output, err := c.CombinedOutput(); err != nil {
			fmt.Println(err)
		} else {
			fmt.Println(output)
		}
	}()
}

func (viewer *ImageViewer) SaveImage() {
	if viewer.config.Image.SaveDir != "" {
		info := viewer.currentImage.info
		if r, err := info.GetReader(); err == nil {
			if data, err := io.ReadAll(r); err == nil {
				filename := filepath.Base(info.path)
				var dest string
				if info.inputIsArchive {
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

func SetImage(viewer *ImageViewer, info ImageInfo) {
	img := viewer.LoadImageToCache(info)
	viewer.currentPath = filepath.Dir(info.path)
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

	viewer.currentPath = absolutePath
	dir, _ := os.ReadDir(absolutePath)
	i := 0
	for _, x := range dir {
		fullPath := filepath.Join(absolutePath, x.Name())
		switch true {
		case x.IsDir():
			subDirAbsPath := filepath.Join(absolutePath, x.Name())
			subDir, _ := os.ReadDir(subDirAbsPath)
			for _, y := range subDir {
				subFilePath := filepath.Join(subDirAbsPath, y.Name())
				if !y.IsDir() && IsImageFromPath(subFilePath) {
					viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
						path:       subFilePath,
						order:      i,
						inputIsDir: true,
					})
					i++
					break
				}
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
					viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
						inputIsArchive: true,
						showArchive:    true,
						archiveName:    filepath.Base(fullPath),
						path:           path,
						fullPath:       fullPath,
						archiveFile:    fsys,
						order:          i,
					})
					i++
					return fs.SkipDir
				}
				return nil
			})

		case IsImageFromPath(fullPath):
			if selected != nil && selected.path == fullPath {
				selected.order = i
				viewer.currentIndex = i
				viewer.imageFiles = append(viewer.imageFiles, *selected)
			} else {
				viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
					path:  fullPath,
					order: i,
				})
			}
			i++
		}
	}
}

func (viewer *ImageViewer) ReadImageArchive(zipFile string) {
	defer viewer.loadingDir.Done()

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
			viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
				inputIsArchive: true,
				archiveName:    filepath.Base(zipFile),
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
