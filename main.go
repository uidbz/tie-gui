package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"git.sr.ht/~uid/conf"

	"git.sr.ht/~uid/tie/client"

	// "fyne.io/fyne/v2/container"
	// "fyne.io/fyne/v2/widget"

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
	inputIsTieMode
)

func ParseInput(args []string) (absolutePath string, inputType int, err error) {
	tiePtr := flag.Bool("tie", false, "Start in tie mode")
	tieTag := flag.String("tag", "favorite", "Show images with tag")
	flag.Parse()
	if *tiePtr {
		absolutePath = *tieTag
		return absolutePath, inputIsTieMode, nil
	}
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
	if config.General.ThumbnailDir == "" {
		config.General.ThumbnailDir = filepath.Join(os.TempDir(), "imgview")
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
	if config.General.ThumbnailDir == "" {
		config.General.ThumbnailDir = filepath.Join(os.TempDir(), "imgview")
	}

	return config
}

func main() {
	// defer profile.Start(profile.CPUProfile).Stop()
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
		}
	})

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	var selected *imgviewer.ImageInfo
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
		selected = imgviewer.NewImageInfo(-1, absolutePath)
		viewer.ReadImageDir(filepath.Dir(absolutePath), selected)
		loadingImage = true

	case inputIsArchive:
		viewer.ReadImageArchive(absolutePath)

	case inputIsNothing: // Use current working directory
		viewer.ReadImageDir(".", nil)

	case inputIsTieMode:
		viewer.TieMode = true
		config := client.Config{}
		if _, err := conf.LoadFromUserConfigDir("tie", "config.toml", &config); err != nil {
			fmt.Println("Error reading tie config:", err)
		}
		viewer.Tie = client.NewTieClient(config)
		viewer.ReadFromTie([]string{absolutePath}, nil, "tag")

	default:
		panic("Input is not understood")
	}

	viewer.OnImageChange = func(info *imgviewer.ImageInfo) {
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
