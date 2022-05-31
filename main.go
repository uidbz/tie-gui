package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"fyne.io/fyne/v2/container"
)

type ImageViewer struct {
	gallery        *fyne.Container
	imageFiles     []string
	imageContainer *fyne.Container
	layout         *TileLayout
	window         fyne.Window
	app            fyne.App
	currentImage   int
	cache          map[string]*ImageView
	scroll         *container.Scroll
	defaultWidth   float32
	defaultHeight  float32
}

const (
	inputIsNothing = iota
	inputError
	inputIsNotSupported
	inputIsDirectory
	inputIsImage
	inputIsZip
)

func (viewer *ImageViewer) LoadImageToCache(path string) *ImageView {
	if x, ok := viewer.cache[path]; ok == false {
		img := NewImageView(path, viewer.window.Canvas().Size(), true, true, true, viewer.window, viewer.window.Canvas().Focus)
		img.changeFn = func() {
			go func() {
				viewer.window.SetTitle("imgview - " + img.GetImageInfo())
			}()
		}
		viewer.cache[path] = img
		return img
	} else {
		return x
	}
}

func (viewer *ImageViewer) NextImage() (path string, number int) {
	nextImg := viewer.currentImage + 1
	if nextImg == len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg], nextImg
}

func (viewer *ImageViewer) PrevImage() (path string, number int) {
	nextImg := viewer.currentImage - 1
	if nextImg < 0 {
		nextImg = 0
	}
	return viewer.imageFiles[nextImg], nextImg
}

func SetImage(viewer *ImageViewer, path string, imageNumber int) {
	img := viewer.LoadImageToCache(path)
	go func() {
		path, _ := viewer.NextImage()
		viewer.LoadImageToCache(path)
	}()
	img.fillWindow = true
	img.container = viewer.imageContainer
	viewer.imageContainer.Objects = []fyne.CanvasObject{img}
	viewer.currentImage = imageNumber
	img.hotkeys = []Hotkey{
		Hotkey{fyne.KeyRight, func() {
			path, number := viewer.NextImage()
			SetImage(viewer, path, number)
		}},
		Hotkey{fyne.KeyJ, func() {
			path, number := viewer.NextImage()
			SetImage(viewer, path, number)
		}},
		Hotkey{fyne.KeySpace, func() {
			path, number := viewer.NextImage()
			SetImage(viewer, path, number)
		}},
		Hotkey{fyne.KeyLeft, func() {
			path, number := viewer.PrevImage()
			SetImage(viewer, path, number)
		}},
		Hotkey{fyne.KeyK, func() {
			path, number := viewer.PrevImage()
			SetImage(viewer, path, number)
		}},
		Hotkey{fyne.KeyUp, func() {
			img.RotateLeft()
		}},
		Hotkey{fyne.KeyDown, func() {
			img.RotateRight()
		}},
		Hotkey{fyne.KeyS, func() {
			img.OriginalSize()
		}},
		Hotkey{fyne.KeyEscape, func() {
			if viewer.scroll == nil {
				go viewer.layout.AddTilesFromPath(viewer.imageFiles, viewer.gallery, viewer.window)
				viewer.scroll = container.NewScroll(viewer.gallery)
			}
			viewer.window.SetTitle("imgview")
			viewer.window.SetContent(viewer.scroll)
		}},
		Hotkey{fyne.KeyQ, func() {
			viewer.app.Quit()
		}},
		Hotkey{fyne.KeyX, func() {
			img.fillWindow = true
			viewer.imageContainer.Refresh()
		}},
		Hotkey{fyne.KeyF, func() {
			img.fillWindow = false
			viewer.window.SetFullScreen(!viewer.window.FullScreen())
			img.fillWindow = true
			viewer.imageContainer.Refresh()
		}},
	}
	viewer.window.SetContent(viewer.imageContainer)
	viewer.window.Canvas().Focus(img)
	viewer.imageContainer.Refresh()
	img.changeFn()
}

func ParseInput(args []string) (absolutePath string, inputType int, err error) {
	if len(os.Args) > 1 {
		path := os.Args[1]
		fi, err := os.Stat(path)
		if err != nil {
			absolutePath = ""
			return absolutePath, inputError, err
		}
		absolutePath, err = filepath.Abs(path)
		if err != nil {
			absolutePath = ""
			return absolutePath, inputError, err
		}
		if fi.IsDir() {
			return absolutePath, inputIsDirectory, nil
		}
		if filepath.Ext(absolutePath) == ".zip" {
			return "zip://" + absolutePath, inputIsZip, nil
		}
		if IsImageFromPath(absolutePath) {
			return absolutePath, inputIsImage, nil
		}
		return absolutePath, inputIsNotSupported, errors.New("Unknown input type")
	} else {
		absolutePath = "."
		return absolutePath, inputIsNothing, nil
	}
}

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("imgview")

	loadingImage := false
	directory := "."
	// isZip := false
	absolutePath, inputType, err := ParseInput(os.Args)

	switch inputType {
	case inputError:
		panic(err)
	case inputIsDirectory:
		directory = absolutePath
	case inputIsImage:
		directory = filepath.Dir(absolutePath)
		loadingImage = true
	case inputIsZip:
		directory = absolutePath
		// isZip = true
	case inputIsNothing:
		// Use defaults
	default:
		panic("Input is not understood")
	}

	dir, _ := os.ReadDir(directory)
	imageFiles := []string{}
	for _, x := range dir {
		if IsImage(x) {
			imageFiles = append(imageFiles, filepath.Join(directory, x.Name()))
		}
	}

	viewer := &ImageViewer{
		app:            myApp,
		window:         myWindow,
		imageFiles:     imageFiles,
		imageContainer: container.New(&ImageLayout{}, []fyne.CanvasObject{}...),
		cache:          make(map[string]*ImageView),
		defaultWidth:   1024,
		defaultHeight:  1024,
	}

	if len(os.Args) > 2 {
		if f, err := strconv.ParseFloat(os.Args[2], 32); err == nil {
			viewer.defaultWidth = float32(f)
			viewer.defaultHeight = float32(f)
		}
		if len(os.Args) > 3 {
			if f, err := strconv.ParseFloat(os.Args[3], 32); err == nil {
				viewer.defaultHeight = float32(f)
			}
		}
	}

	tileOnclick := func(t *Tile) {
		SetImage(viewer, t.context.path, t.context.order)
	}

	viewer.layout = NewTileLayout(300, 5, 8, tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)

	myWindow.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		if key.Name == fyne.KeyQ || key.Name == fyne.KeyEscape {
			myApp.Quit()
		}
		if key.Name == fyne.KeyDown || key.Name == fyne.KeyJ {
			viewer.scroll.Offset.Y = viewer.scroll.Offset.Y + 300
			viewer.scroll.Refresh()
		}
		if key.Name == fyne.KeyUp || key.Name == fyne.KeyK {
			viewer.scroll.Offset.Y = viewer.scroll.Offset.Y - 300
			viewer.scroll.Refresh()
		}
	})
	if loadingImage {
		for i, x := range imageFiles {
			if x == absolutePath {
				SetImage(viewer, absolutePath, i)
				break
			}
		}
	} else {
		go viewer.layout.AddTilesFromPath(imageFiles, viewer.gallery, myWindow)
		viewer.scroll = container.NewScroll(viewer.gallery)
		myWindow.SetContent(viewer.scroll)
	}
	myWindow.Resize(fyne.NewSize(viewer.defaultWidth, viewer.defaultHeight))

	myWindow.ShowAndRun()
}
