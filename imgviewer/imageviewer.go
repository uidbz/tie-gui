package imgviewer

import (
	"bytes"
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

	"git.sr.ht/~uid/tie/api"

	"git.sr.ht/~uid/imgview/imgviewer/tagselection"

	"git.sr.ht/~uid/tie/io/getlib"

	"github.com/mholt/archiver/v4"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"git.sr.ht/~uid/tie/client"
)

type ImageViewer struct {
	// Content       fyne.CanvasObject
	Content       fyne.CanvasObject
	CurrentImage  fyne.CanvasObject
	CustomReaders []CustomReader
	TieMode       bool
	Tie           *client.TieClient

	gallery        *fyne.Container
	imageFiles     []*ImageInfo
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
	loading        sync.WaitGroup
	config         Config
	bottomBar      *fyne.Container

	tileOnclick       func(*Tile)
	OnTapped          func()
	OnDoubleTapped    func()
	OnTappedSecondary func()

	currentPage  int
	maxPages     int
	isFullscreen bool
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

func NewImageViewer(app fyne.App, window fyne.Window, config Config, tileOnclick func(t *Tile)) *ImageViewer {
	return &ImageViewer{
		app:            app,
		window:         window,
		imageContainer: container.New(&ImageLayout{}, []fyne.CanvasObject{}...),
		cache:          make(map[string]*ImageView),
		config:         config,
		tileOnclick:    tileOnclick,
	}
}

func (viewer *ImageViewer) KeyPress(key *fyne.KeyEvent) {
	for _, x := range viewer.layout.hotkeys {
		if key.Name == x.Name {
			x.Functon()
		}
	}
}

func (viewer *ImageViewer) ShowImageDir(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.ReadImageDir(path, nil)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *ImageViewer) ShowImageArchive(path string) {
	viewer.imageFiles = make([]*ImageInfo, 0)
	viewer.ReadImageArchive(path)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *ImageViewer) Init() {
	viewer.layout = NewTileLayout(viewer.config, viewer.window, viewer.app, viewer, viewer.tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)
	viewer.layout.grid = viewer.gallery
	viewer.layout.InitHotkeys()
	viewer.InitHotkeys()
}

func (viewer *ImageViewer) MakeTieSidebar(mainPage fyne.CanvasObject) fyne.CanvasObject {
	var split *container.Split
	ts := tagselection.NewTagSelection(viewer.window)
	viewer.Tie.SimpleGet("tags", func(r client.GetReply) {
		if r.Success {
			r.Result.ForEachValue2(func(key, val1, val2 string) {
				switch val1 {
				case "all":
					ts.AddTag(val2)
				case "favorite":
					ts.AddFavorite(val2)
				}
			})
		}
	})
	// query := widget.NewEntry()
	ts.OnSelectedChanged = func() {
		in, ex := ts.SelectedTags()
		viewer.ReadFromTie(in, ex)
		viewer.ChangeGallery()
	}
	// border := container.NewBorder(query, nil, nil, nil, widget.NewButton("click 2", queryFunc))
	split = container.NewHSplit(ts, viewer.scroll)
	split.SetOffset(0.2)

	return split
}

func (viewer *ImageViewer) CreateView() {
	var mainPage fyne.CanvasObject
	viewer.scroll = container.NewScroll(viewer.gallery)
	if viewer.TieMode {
		mainPage = viewer.MakeTieSidebar(viewer.scroll)
	} else {
		mainPage = viewer.scroll
	}
	viewer.Content = container.NewBorder(nil, viewer.bottomBar, nil, nil, mainPage)
}

func (viewer *ImageViewer) LoadGallery() {
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
}

func (viewer *ImageViewer) ChangeGallery() {
	// empty channel before changing page, then wait for workers to finish
	for len(viewer.layout.imagesToLoad) > 0 {
		<-viewer.layout.imagesToLoad
	}
	viewer.layout.currentlyLoading.Wait()

	viewer.currentPage = 0
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
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
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *ImageViewer) LoadImageToCache(info *ImageInfo) *ImageView {
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
				viewer.window.SetTitle("imgview - " + img.GetImageInfo())
			}()
		}
		viewer.cache[info.Path] = img
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
	cmd := strings.ReplaceAll(viewer.config.Image.CmdA, "$FILE", viewer.currentImage.info.Path)
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

func (viewer *ImageViewer) NextImage() *ImageInfo {
	viewer.loading.Wait()
	nextImg := viewer.currentIndex + 1
	if nextImg == len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *ImageViewer) PrevImage() *ImageInfo {
	viewer.loading.Wait()
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
			viewer.ChangeImage(viewer.NextImage())
			viewer.SetImage()
		}})
	}
	for _, x := range bindings.PreviousImage {
		add(Hotkey{x, func() {
			viewer.ChangeImage(viewer.PrevImage())
			viewer.SetImage()
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
			if viewer.Content == nil {
				viewer.LoadGallery()
				viewer.CreateView()
			}
			viewer.window.SetTitle("imgview")
			viewer.window.SetContent(viewer.Content)
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

func (viewer *ImageViewer) ChangeImage(info *ImageInfo) {
	img := viewer.LoadImageToCache(info)
	viewer.currentPath = filepath.Dir(info.Path)
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
	viewer.CurrentImage = viewer.imageContainer
	viewer.imageContainer.Refresh()
	img.changeFn()
}

func (viewer *ImageViewer) SetImage() {
	viewer.window.SetContent(viewer.imageContainer)
	viewer.window.Canvas().Focus(viewer.currentImage)
}

func (viewer *ImageViewer) ReadImageDir(absolutePath string, selected *ImageInfo) {
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
			for _, y := range subDir {
				subFilePath := filepath.Join(subDirAbsPath, y.Name())
				if !y.IsDir() && IsImageFromPath(subFilePath) {
					info := NewImageInfo(i, subFilePath)
					info.InputIsDir = true
					viewer.imageFiles = append(viewer.imageFiles, info)
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
					info := NewImageInfo(i, path)
					info.InputIsDir = true
					info.FullPath = fullPath
					info.archiveFile = fsys
					info.archiveName = filepath.Base(fullPath)
					info.InputIsArchive = true
					info.ShowArchive = true
					viewer.imageFiles = append(viewer.imageFiles, info)
					i++
					return fs.SkipDir
				}
				return nil
			})

		case IsImageFromPath(fullPath):
			if selected != nil && selected.Path == fullPath {
				selected.order = i
				viewer.currentIndex = i
				viewer.imageFiles = append(viewer.imageFiles, selected)
			} else {
				info := NewImageInfo(i, fullPath)
				viewer.imageFiles = append(viewer.imageFiles, info)
			}
			i++
		}
	}
}

type CustomReader interface {
	GetReader() (io.ReadSeeker, error)
	Path() string // Used for caching and identification
}

func (viewer *ImageViewer) ReadCustom() {
	viewer.loading.Add(1)
	defer viewer.loading.Done()

	viewer.imageFiles = make([]*ImageInfo, 0)
	for i, r := range viewer.CustomReaders {
		if reader, err := r.GetReader(); err == nil {
			if IsImage(reader) {
				info := NewImageInfoCustomReader(i, r)
				info.Path = r.Path()
				viewer.imageFiles = append(viewer.imageFiles, info)
			}
		} else {
			fmt.Println("Error getting reader:", err)
		}
	}
}

type tieReader struct {
	seeker io.ReadSeeker
	data   []byte
	host   string
	hash   string
}

func (t *tieReader) Path() string {
	return t.hash
}

func (t *tieReader) GetReader() (io.ReadSeeker, error) {
	var err error
	var r io.Reader
	if t.seeker == nil {
		r, err = getlib.ReadFile(t.host, t.hash)
		if err == nil {
			t.data, err = io.ReadAll(r)
			if err == nil {
				t.seeker = bytes.NewReader(t.data)
			}
		}
	}
	return t.seeker, err
}

func (viewer *ImageViewer) ReadFromTie(include, exclude []string) {
	if len(include) == 0 {
		return
	}
	intersect := make([]api.Transform, len(include)-1)
	for i := 1; i < len(include); i++ {
		intersect[i-1] = api.Transform{
			Key:     include[i],
			Reverse: true,
		}
	}
	var ex []api.Transform
	if exclude != nil {
		ex = make([]api.Transform, len(exclude))
		for i := 0; i < len(exclude); i++ {
			ex[i] = api.Transform{
				Key:     exclude[i],
				Reverse: true,
			}
		}
	}
	o := client.GetOptions{
		Intersect: intersect,
		Exclude:   ex,
		Reverse:   true,
		Filter:    "tag",
	}
	viewer.Tie.Get(include[0], o, func(r client.GetReply) {
		if r.Success {
			viewer.CustomReaders = make([]CustomReader, 0)
			r.Result.ForEachKey(func(hash string) {
				viewer.CustomReaders = append(viewer.CustomReaders, &tieReader{host: viewer.Tie.Config.FileHosts["fast"], hash: hash})
			})
			viewer.ReadCustom()
		} else {
			fmt.Println("Error happened quering tie:", r.Message)
		}
	})
}

func (viewer *ImageViewer) ReadImageArchive(zipFile string) {
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
