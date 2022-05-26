package main

import (
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"fyne.io/fyne/v2/container"
)

type ImageViewer struct {
	gallery        *fyne.Container
	imageFiles     []string
	imageContainer *fyne.Container
	window         fyne.Window
	app            fyne.App
	currentImage   int
	cache          map[string]*ImageView
	scroll         fyne.CanvasObject
}

func (viewer *ImageViewer) LoadImageToCache(path string) *ImageView {
	if x, ok := viewer.cache[path]; ok == false {
		img := NewImageView(path, viewer.window.Canvas().Size(), true, true, true, viewer.window, viewer.window.Canvas().Focus)
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
		Hotkey{fyne.KeyLeft, func() {
			nextImg := viewer.currentImage - 1
			if nextImg < 0 {
				nextImg = 0
			}
			SetImage(viewer, viewer.imageFiles[nextImg], nextImg)
		}},
		Hotkey{fyne.KeyEscape, func() {
			if viewer.scroll == nil {
				viewer.scroll = container.NewScroll(viewer.gallery)
			}
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
		}},
	}
	viewer.window.SetContent(viewer.imageContainer)
	viewer.window.Canvas().Focus(img)
	viewer.imageContainer.Refresh()
}

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("imgview")

	loadingImage := false
	path := "."
	directory := "."

	if len(os.Args) > 1 {
		if fi, err := os.Stat(os.Args[1]); err != nil {
			panic(err)
		} else {
			if abspath, err := filepath.Abs(os.Args[1]); err != nil {
				panic(err)
			} else {
				path = abspath
				if fi.IsDir() {
					directory = path
				} else {
					directory = filepath.Dir(path)
				}
				if IsImageFromPath(path) {
					loadingImage = true
				}
			}
		}
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
	}

	tileOnclick := func(t *Tile) {
		SetImage(viewer, t.context.path, t.context.order)
	}

	layout := NewTileLayout(300, 5, 8, tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(layout, empty...)

	go layout.AddTilesFromPath(imageFiles, viewer.gallery, myWindow)

	myWindow.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		if key.Name == fyne.KeyQ || key.Name == fyne.KeyEscape {
			myApp.Quit()
		}
	})
	if loadingImage {
		for i, x := range imageFiles {
			if y, err := filepath.Abs(path); err == nil && x == y {
				SetImage(viewer, path, i)
			}
		}
	} else {
		viewer.scroll = container.NewScroll(viewer.gallery)
		myWindow.SetContent(viewer.scroll)
	}

	myWindow.ShowAndRun()
}
