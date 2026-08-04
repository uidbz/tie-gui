package main

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"

	// "fyne.io/fyne/v2/container"
	// "fyne.io/fyne/v2/widget"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"git.sr.ht/~uid/imgview/gallery"
	// "github.com/pkg/profile"
)

//go:embed Icon.png
var icon []byte

const (
	inputIsNothing = iota
	inputError
	inputIsNotSupported
	inputIsDirectory
	inputIsImage
	inputIsArchive
)

func ParseInput(args []string) (absolutePath string, inputType int, err error) {
	if len(os.Args) > 1 {
		path := os.Args[1]
		absolutePath, err = filepath.Abs(path)
		fi, err := os.Stat(absolutePath)
		if err != nil {
			absolutePath = ""
			return absolutePath, inputError, err
		}
		if err != nil {
			absolutePath = ""
			return absolutePath, inputError, err
		}
		if fi.IsDir() {
			return absolutePath, inputIsDirectory, nil
		}
		if gallery.IsArchiveFromPath(absolutePath) {
			return absolutePath, inputIsArchive, nil
		}
		if gallery.IsImageFromPath(absolutePath) {
			return absolutePath, inputIsImage, nil
		}
		return absolutePath, inputIsNotSupported, errors.New("Unknown input type")
	} else {
		absolutePath = "."
		return absolutePath, inputIsNothing, nil
	}
}

func main() {
	// defer profile.Start(profile.CPUProfile).Stop()
	// defer profile.Start().Stop()
	myApp := app.NewWithID("sr.ht.uid.imgview")
	myApp.SetIcon(fyne.NewStaticResource("icon", icon))
	myWindow := myApp.NewWindow("imgview")

	config := gallery.LoadConfig(myWindow)

	viewer := gallery.NewViewer(myApp, myWindow, config, func(t *gallery.Tile) {
		switch true {
		case t.Info.InputIsDir:
			t.Viewer.ShowImageDir(filepath.Dir(t.Info.Path))
		case t.Info.ShowArchive:
			t.Viewer.ShowImageArchive(t.Info.FullPath)
		default:
			t.Viewer.ChangeImage(t.Info)
		}
	})

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	var selected *gallery.ImageInfo
	loadingImage := false
	absolutePath, inputType, err := ParseInput(os.Args)

	switch inputType {
	case inputError:
		dialog.ShowError(err, myWindow)
		myWindow.ShowAndRun()
		return

	case inputIsDirectory:
		viewer.ReadImageDir(absolutePath, nil)

	case inputIsImage:
		selected = gallery.NewImageInfo(-1, absolutePath)
		viewer.ReadImageDir(filepath.Dir(absolutePath), selected)
		loadingImage = true

	case inputIsArchive:
		viewer.ReadImageArchive(absolutePath)

	case inputIsNothing: // Use current working directory
		viewer.ReadImageDir(".", nil)

	default:
		panic("Input is not understood")
	}

	viewer.OnImageChange = func(info *gallery.ImageInfo) {
		myWindow.Canvas().Focus(viewer.CurrentImageView)
	}

	myWindow.SetContent(viewer.Content)

	if loadingImage {
		viewer.ChangeImage(selected)
	} else {
		viewer.LoadGallery()
		viewer.CreateView()
	}

	myWindow.Resize(fyne.NewSize(config.General.DefaultWidth, config.General.DefaultHeight))

	myWindow.ShowAndRun()
}
