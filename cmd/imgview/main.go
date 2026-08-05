package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// "fyne.io/fyne/v2/container"
	// "fyne.io/fyne/v2/widget"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"git.sr.ht/~uid/imgview/gallery"
	"git.sr.ht/~uid/imgview/mpvplayer"
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
	if len(args) > 0 {
		path := args[0]
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
	configPath := flag.String("config", "", "imgview config file to load (default: config.toml in user config dir)")
	flag.StringVar(configPath, "c", "", "Shorthand for -config")
	flag.Parse()

	// Append .toml extension when the caller omitted it.
	if *configPath != "" && !strings.HasSuffix(*configPath, ".toml") {
		*configPath += ".toml"
	}

	myApp := app.NewWithID("sr.ht.uid.imgview")
	myApp.SetIcon(fyne.NewStaticResource("icon", icon))
	myWindow := myApp.NewWindow("imgview")

	config := gallery.LoadConfig(myWindow, *configPath)

	viewer := gallery.NewViewer(myApp, myWindow, config, func(t *gallery.Tile) {
		switch true {
		case t.Info.InputIsDir:
			t.Viewer.ShowImageDir(filepath.Dir(t.Info.Path))
		case t.Info.ShowArchive:
			t.Viewer.ShowImageArchive(t.Info.FullPath)
		case t.Info.InputIsVideo:
			go func() {
				player, err := mpvplayer.NewMPVPlayer(t.Info.Path)
				if err != nil {
					fmt.Println("Error starting video player:", err)
					return
				}
				fyne.Do(func() {
					videoWindow := myApp.NewWindow("Video: " + filepath.Base(t.Info.Path))
					video := mpvplayer.NewVideo(player)
					videoWindow.SetCloseIntercept(func() {
						video.Close()
						videoWindow.Close()
					})
					videoWindow.SetContent(video)
					videoWindow.Resize(fyne.NewSize(800, 520))
					videoWindow.Show()
				})
			}()
		default:
			t.Viewer.ChangeImage(t.Info)
		}
	})

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	var selected *gallery.ImageInfo
	loadingImage := false
	absolutePath, inputType, err := ParseInput(flag.Args())

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
