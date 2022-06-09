package main

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
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
	defaultWidth   float32
	defaultHeight  float32
	hotkeys        []Hotkey
	loadingDir     sync.WaitGroup
	config         Config
}

type Config struct {
	Image   ImageConfig
	Gallery GalleryConfig
}

type ImageConfig struct {
	NextImage     []fyne.KeyName
	PreviousImage []fyne.KeyName
	RotateLeft    []fyne.KeyName
	RotateRight   []fyne.KeyName
	OriginalSize  []fyne.KeyName
	FillWindow    []fyne.KeyName
	Escape        []fyne.KeyName
	Quit          []fyne.KeyName
	FullScreen    []fyne.KeyName
}

type GalleryConfig struct {
	Quit       []fyne.KeyName
	ScrollDown []fyne.KeyName
	ScrollUp   []fyne.KeyName
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
	for _, x := range bindings.Escape {
		add(Hotkey{x, func() {
			if viewer.scroll == nil {
				viewer.loadingDir.Wait()
				go viewer.layout.AddTiles(viewer.imageFiles)
				viewer.scroll = container.NewScroll(viewer.gallery)
			}
			viewer.window.SetTitle("imgview")
			viewer.window.SetContent(viewer.scroll)
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
