package main

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"git.sr.ht/~uid/imgview/gallery"
	"git.sr.ht/~uid/imgview/mpvplayer"
	// "github.com/pkg/profile"
)

// uriReader adapts a fyne.URI (e.g. an Android content:// document) to the
// gallery.CustomReader interface, so picked folders load through the same path
// as tie/archive entries. The content is read once and served from memory
// because GetReader must return a seekable stream and content URIs are not
// re-openable as os files.
type uriReader struct {
	uri  fyne.URI
	data []byte
}

func (r *uriReader) Path() string { return r.uri.String() }

func (r *uriReader) GetReader() (io.ReadSeeker, error) {
	if r.data == nil {
		rc, err := storage.Reader(r.uri)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, err
		}
		r.data = data
	}
	return bytes.NewReader(r.data), nil
}

// imageExtensions are the file suffixes we treat as images when scanning a
// picked folder (content URIs don't let us sniff bytes cheaply up front).
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true,
	".webp": true, ".tiff": true, ".tif": true, ".heic": true, ".heif": true,
}

// readersFromFolder lists a picked folder and returns a CustomReader for each
// image child, sorted by name.
func readersFromFolder(dir fyne.ListableURI) ([]gallery.CustomReader, error) {
	uris, err := dir.List()
	if err != nil {
		return nil, err
	}
	sort.Slice(uris, func(i, j int) bool { return uris[i].Name() < uris[j].Name() })

	var readers []gallery.CustomReader
	for _, u := range uris {
		if imageExtensions[strings.ToLower(u.Extension())] {
			readers = append(readers, &uriReader{uri: u})
		}
	}
	return readers, nil
}

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
	configPath := gallery.ConfigFlag("imgview config file to load (default: config.toml in user config dir)")
	flag.Parse()

	myApp, myWindow := gallery.NewApp("sr.ht.uid.imgview", "imgview", icon)

	config := gallery.LoadConfig(myWindow, gallery.NormalizeConfigPath(*configPath))

	viewer := gallery.NewGallery(myApp, myWindow, config, func(t *gallery.Tile) {
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
		// Focusing the image view drives keyboard navigation on desktop, but on
		// mobile it summons the soft keyboard (the view is Focusable). Skip it.
		if !fyne.CurrentDevice().IsMobile() {
			myWindow.Canvas().Focus(viewer.CurrentImageView)
		}
	}

	myWindow.SetContent(viewer.Content)

	if loadingImage {
		viewer.ChangeImage(selected)
	} else {
		viewer.LoadGallery()
		viewer.CreateView()
	}

	// On mobile there are no command-line args and no accessible working
	// directory, so the gallery starts empty. Offer a folder picker (Android's
	// document chooser) so the user can point imgview at a photo folder.
	if fyne.CurrentDevice().IsMobile() && viewer.ImageCount() == 0 {
		showFolderPicker(myWindow, viewer)
	}

	myWindow.Resize(fyne.NewSize(config.General.DefaultWidth, config.General.DefaultHeight))

	myWindow.ShowAndRun()
}

// showFolderPicker presents an empty-state screen with a button that opens the
// platform folder chooser. The chosen folder's images are loaded into the
// viewer and the normal gallery view is shown.
func showFolderPicker(win fyne.Window, viewer *gallery.Gallery) {
	var content fyne.CanvasObject
	pick := widget.NewButtonWithIcon("Select folder", nil, func() {
		dialog.ShowFolderOpen(func(dir fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, win)
				return
			}
			if dir == nil {
				return // cancelled
			}
			readers, err := readersFromFolder(dir)
			if err != nil {
				dialog.ShowError(err, win)
				return
			}
			if len(readers) == 0 {
				dialog.ShowInformation("No images", "That folder contains no supported images.", win)
				return
			}
			viewer.ReadCustom(readers)
			viewer.LoadGallery()
			viewer.CreateView()
			win.SetContent(viewer.Content)
		}, win)
	})

	label := widget.NewLabel("No images loaded.")
	label.Alignment = fyne.TextAlignCenter
	content = container.NewCenter(container.NewVBox(label, pick))
	win.SetContent(content)
}
