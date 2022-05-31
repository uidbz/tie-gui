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

const (
	inputIsNothing = iota
	inputError
	inputIsNotSupported
	inputIsDirectory
	inputIsImage
	inputIsZip
)

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
	imageFiles := []ImageInfo{}
	i := 0
	for _, x := range dir {
		if IsImage(x) {
			imageFiles = append(imageFiles, ImageInfo{filepath.Join(directory, x.Name()), i})
			i++
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
	viewer.InitHotkeys()

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
		SetImage(viewer, t.info)
	}

	viewer.layout = NewTileLayout(300, 5, 8, tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)
	viewer.layout.grid = viewer.gallery

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
		for _, x := range imageFiles {
			if x.path == absolutePath {
				SetImage(viewer, x)
				break
			}
		}
	} else {
		go viewer.layout.AddTiles(imageFiles)
		viewer.scroll = container.NewScroll(viewer.gallery)
		myWindow.SetContent(viewer.scroll)
	}
	myWindow.Resize(fyne.NewSize(viewer.defaultWidth, viewer.defaultHeight))

	myWindow.ShowAndRun()
}
