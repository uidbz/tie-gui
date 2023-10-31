package main

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"git.sr.ht/~uid/imgview/imgviewer"

	"github.com/pelletier/go-toml/v2"
	// "github.com/pkg/profile"
)

//go:embed config.toml
var configData []byte

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
		if imgviewer.IsArchiveFromPath(absolutePath) {
			return absolutePath, inputIsArchive, nil
		}
		if imgviewer.IsImageFromPath(absolutePath) {
			return absolutePath, inputIsImage, nil
		}
		return absolutePath, inputIsNotSupported, errors.New("Unknown input type")
	} else {
		absolutePath = "."
		return absolutePath, inputIsNothing, nil
	}
}

func LoadConfig(window fyne.Window) (config imgviewer.Config) {
	if err := toml.Unmarshal(configData, &config); err != nil {
		panic("Bundled config is not valid TOML: " + err.Error())
	}

	if dir, err := os.UserConfigDir(); err == nil {
		imgviewConfig := filepath.Join(dir, "imgview", "config.toml")
		if _, err2 := os.Stat(imgviewConfig); !os.IsNotExist(err2) {
			configFile, errRead := os.ReadFile(imgviewConfig)
			if errRead != nil {
				dialog.ShowError(errors.New("Error reading config file: "+errRead.Error()), window)
				window.ShowAndRun()
			}
			if err3 := toml.Unmarshal(configFile, &config); err3 != nil {
				dialog.ShowError(errors.New("Config file error: "+err3.Error()+"\nin: "+imgviewConfig), window)
				window.ShowAndRun()
			}
		}
	}

	return config
}

func main() {
	// defer profile.Start(profile.MemProfile).Stop()
	// defer profile.Start().Stop()
	myApp := app.New()
	myApp.SetIcon(fyne.NewStaticResource("icon", icon))
	myWindow := myApp.NewWindow("imgview")

	config := LoadConfig(myWindow)

	viewer := imgviewer.NewImageViewer(myApp, myWindow, config, func(t *imgviewer.Tile) {
		switch true {
		case t.Info.InputIsDir:
			t.Viewer.ShowImageDir(filepath.Dir(t.Info.Path))
		case t.Info.ShowArchive:
			t.Viewer.ShowImageArchive(t.Info.FullPath)
		default:
			t.Viewer.ChangeImage(t.Info)
			t.Viewer.SetImage()
		}
	})

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	var selected *imgviewer.ImageInfo
	loadingImage := false
	directory := "."
	absolutePath, inputType, err := ParseInput(os.Args)

	switch inputType {
	case inputError:
		dialog.ShowError(err, myWindow)
		myWindow.ShowAndRun()
		return

	case inputIsDirectory:
		directory = absolutePath
		go viewer.ReadImageDir(directory, nil)

	case inputIsImage:
		directory = filepath.Dir(absolutePath)
		selected = imgviewer.NewImageInfo(-1, absolutePath)
		go viewer.ReadImageDir(directory, selected)
		loadingImage = true

	case inputIsArchive:
		directory = absolutePath
		go viewer.ReadImageArchive(directory)

	case inputIsNothing: // Use current working directory
		directory = absolutePath
		viewer.ReadImageDir(directory, nil)

	default:
		panic("Input is not understood")
	}

	if loadingImage {
		viewer.ChangeImage(selected)
		viewer.SetImage()
	} else {
		viewer.LoadGallery()
		myWindow.SetContent(viewer.Gallery)
	}
	myWindow.Resize(fyne.NewSize(config.General.DefaultWidth, config.General.DefaultHeight))

	myWindow.ShowAndRun()
}
