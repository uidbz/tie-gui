package main

import (
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
}

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

func (viewer *ImageViewer) NextImage() ImageInfo {
	nextImg := viewer.currentIndex + 1
	if nextImg == len(viewer.imageFiles) {
		nextImg = len(viewer.imageFiles) - 1
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *ImageViewer) PrevImage() ImageInfo {
	nextImg := viewer.currentIndex - 1
	if nextImg < 0 {
		nextImg = 0
	}
	return viewer.imageFiles[nextImg]
}

func (viewer *ImageViewer) InitHotkeys() {
	viewer.hotkeys = []Hotkey{
		Hotkey{fyne.KeyRight, func() {
			SetImage(viewer, viewer.NextImage())
		}},
		Hotkey{fyne.KeyJ, func() {
			SetImage(viewer, viewer.NextImage())
		}},
		Hotkey{fyne.KeySpace, func() {
			SetImage(viewer, viewer.NextImage())
		}},
		Hotkey{fyne.KeyLeft, func() {
			SetImage(viewer, viewer.PrevImage())
		}},
		Hotkey{fyne.KeyK, func() {
			SetImage(viewer, viewer.PrevImage())
		}},
		Hotkey{fyne.KeyUp, func() {
			viewer.currentImage.RotateLeft()
		}},
		Hotkey{fyne.KeyDown, func() {
			viewer.currentImage.RotateRight()
		}},
		Hotkey{fyne.KeyS, func() {
			viewer.currentImage.OriginalSize()
		}},
		Hotkey{fyne.KeyEscape, func() {
			if viewer.scroll == nil {
				go viewer.layout.AddTiles(viewer.imageFiles)
				viewer.scroll = container.NewScroll(viewer.gallery)
			}
			viewer.window.SetTitle("imgview")
			viewer.window.SetContent(viewer.scroll)
		}},
		Hotkey{fyne.KeyQ, func() {
			viewer.app.Quit()
		}},
		Hotkey{fyne.KeyX, func() {
			viewer.currentImage.fillWindow = true
			viewer.imageContainer.Refresh()
		}},
		Hotkey{fyne.KeyF, func() {
			viewer.currentImage.fillWindow = false
			viewer.window.SetFullScreen(!viewer.window.FullScreen())
			viewer.currentImage.fillWindow = true
			viewer.imageContainer.Refresh()
		}},
	}

}

func SetImage(viewer *ImageViewer, info ImageInfo) {
	img := viewer.LoadImageToCache(info.path)
	viewer.currentImage = img
	go func() {
		viewer.LoadImageToCache(viewer.NextImage().path)
	}()
	img.fillWindow = true
	img.container = viewer.imageContainer
	img.hotkeys = viewer.hotkeys
	viewer.imageContainer.Objects = []fyne.CanvasObject{img}
	viewer.currentIndex = info.order
	viewer.window.SetContent(viewer.imageContainer)
	viewer.window.Canvas().Focus(img)
	viewer.imageContainer.Refresh()
	img.changeFn()
}
