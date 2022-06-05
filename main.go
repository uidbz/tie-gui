package main

import (
	"sort"
	// "archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mholt/archiver/v4"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"fyne.io/fyne/v2/container"
)

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
		if IsArchiveFromPath(absolutePath) {
			return absolutePath, inputIsArchive, nil
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

func (viewer *ImageViewer) ReadImageDir(absolutePath string, selected *ImageInfo) {
	dir, _ := os.ReadDir(absolutePath)
	i := 0
	for _, x := range dir {
		if x.IsDir() {
			continue
		}
		fullpath := filepath.Join(absolutePath, x.Name())
		if IsImageFromPath(fullpath) {
			if selected != nil && selected.path == fullpath {
				selected.order = i
				viewer.currentIndex = i
				viewer.imageFiles = append(viewer.imageFiles, *selected)
			} else {
				viewer.imageFiles = append(viewer.imageFiles, ImageInfo{
					path:  fullpath,
					order: i,
				})
			}
			i++
		}
	}

	viewer.loadingDir.Done()
}

func ReadImageZip(zipFile string) (imageFiles []ImageInfo) {
	fsys, err := archiver.FileSystem(zipFile)
	if err != nil {
		fmt.Println(err)
		return []ImageInfo{}
	}
	i := 0
	fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
		if x.IsDir() {
			return nil
		}
		file, err := fsys.Open(path)
		if err != nil {
			fmt.Println("Error opening:", x.Name())
			return nil
		}
		if IsImage(file) {
			imageFiles = append(imageFiles, ImageInfo{
				inputIsArchive: true,
				path:           path,
				archiveFile:    fsys,
				order:          i,
			})
			i++
		}
		return nil
	})
	sort.Slice(imageFiles, func(i, j int) bool {
		return imageFiles[i].path < imageFiles[j].path
	})
	for i, _ := range imageFiles {
		imageFiles[i].order = i
	}
	return imageFiles
}

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("imgview")

	viewer := &ImageViewer{
		app:            myApp,
		window:         myWindow,
		imageContainer: container.New(&ImageLayout{}, []fyne.CanvasObject{}...),
		cache:          make(map[string]*ImageView),
		defaultWidth:   1024,
		defaultHeight:  1024,
	}

	var selected *ImageInfo
	loadingImage := false
	directory := "."
	absolutePath, inputType, err := ParseInput(os.Args)

	viewer.loadingDir.Add(1)

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
		selected = &ImageInfo{
			path:  absolutePath,
			order: -1,
		}
		go viewer.ReadImageDir(directory, selected)
		loadingImage = true

	case inputIsArchive:
		directory = absolutePath
		viewer.imageFiles = ReadImageZip(directory)
		viewer.loadingDir.Done()

	case inputIsNothing: // Use current working directory
		directory = absolutePath
		viewer.ReadImageDir(directory, nil)

	default:
		panic("Input is not understood")
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
		SetImage(viewer, *selected)
	} else {
		go func() {
			viewer.loadingDir.Wait()
			viewer.layout.AddTiles(viewer.imageFiles)
		}()
		viewer.scroll = container.NewScroll(viewer.gallery)
		myWindow.SetContent(viewer.scroll)
	}
	myWindow.Resize(fyne.NewSize(viewer.defaultWidth, viewer.defaultHeight))

	myWindow.ShowAndRun()
}
