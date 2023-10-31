package imgviewer

import (
	"path/filepath"
	// "path/filepath"
	// "archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"time"

	_ "embed"

	"fyne.io/fyne/v2"

	"sync"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

//go:embed loading.png
var loading []byte

type TileLayout struct {
	tiles            []*Tile
	wg               sync.WaitGroup
	minHeight        float32
	imagesToLoad     chan *ImageInfo
	tabFn            func(t *Tile)
	grid             *fyne.Container
	hotkeys          []Hotkey
	config           Config
	window           fyne.Window
	app              fyne.App
	viewer           *ImageViewer
	offset           int
	currentlyLoading sync.WaitGroup
	cachedTiles      map[string]*Tile
	cacheLock        sync.Mutex
}

type Tile struct {
	widget.BaseWidget
	content   *canvas.Image
	width     float32
	height    float32
	landscape bool
	Viewer    *ImageViewer
	Info      *ImageInfo
	tabFn     func(t *Tile)
}

type ImageInfo struct {
	InputIsArchive    bool
	InputIsDir        bool
	InputIsReader     bool
	IsZoomable        bool
	IsDraggable       bool
	Path              string
	FullPath          string // Used to get path of zipFile
	ShowArchive       bool
	CustomReader      CustomReader
	OnTapped          func()
	OnDoubleTapped    func()
	OnTappedSecondary func()

	archiveName string
	archiveFile fs.FS
	order       int
}

func NewImageInfo(order int, path string) *ImageInfo {
	return &ImageInfo{
		Path:        path,
		IsDraggable: true,
		IsZoomable:  true,
		order:       order,
	}
}

func NewImageInfoCustomReader(order int, r CustomReader) *ImageInfo {
	return &ImageInfo{
		InputIsReader: true,
		CustomReader:  r,
		IsDraggable:   true,
		IsZoomable:    true,
		order:         order,
	}
}

func NewTileLayout(config Config, window fyne.Window, app fyne.App, viewer *ImageViewer, tabFn func(t *Tile)) *TileLayout {
	batchSize := 1024
	tiles := make([]*Tile, 0)
	imagesToLoad := make(chan *ImageInfo, batchSize)
	layout := &TileLayout{
		tiles:        tiles,
		wg:           sync.WaitGroup{},
		minHeight:    0,
		imagesToLoad: imagesToLoad,
		tabFn:        tabFn,
		config:       config,
		window:       window,
		app:          app,
		viewer:       viewer,
		cachedTiles:  make(map[string]*Tile),
	}

	for i := 0; i < config.General.Workers; i++ {
		go layout.imageLoader()
	}

	return layout
}

func (layout *TileLayout) PlaceTiles(imageFiles []*ImageInfo) {
	loadingImg := bytes.NewReader(loading)
	end := layout.offset + layout.config.General.ImagesPerPage
	if end > len(imageFiles) {
		end = len(imageFiles)
	}
	layout.grid.Objects = make([]fyne.CanvasObject, 0)
	for i := layout.offset; i < end; i++ {
		tile := layout.NewImageTile(loadingImg, imageFiles[i], func(t *Tile) {})
		layout.tiles = append(layout.tiles, tile)
		layout.grid.AddObject(tile)
		layout.imagesToLoad <- imageFiles[i]
	}
}

func (layout *TileLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w := float32(layout.config.General.TileWidth + layout.config.General.TileGap)
	return fyne.NewSize(w, layout.minHeight)
}

func (layout *TileLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	tileWidth := layout.config.General.TileWidth
	gap := layout.config.General.TileGap
	tilesPerRow := int(containerSize.Width / tileWidth)
	bottom := make([]float32, tilesPerRow+1)

	peakLandscape := func(i int) bool {
		if i < len(layout.tiles)-1 {
			return layout.tiles[i+1].landscape
		}
		return false
	}
	if containerSize.Width < tileWidth+gap {
		return
	}
	for i := 0; i < len(objects); {
		prevLeft := float32(int(containerSize.Width)%int(tileWidth)) / 3
		for j := 0; j < tilesPerRow && i < len(objects); j++ {
			o := objects[i]
			tile := layout.tiles[i]
			newWidth := tileWidth
			scale := newWidth / tile.width
			newHeight := tile.height * scale
			// fmt.Println("Scale portrait:", scale)
			top := bottom[j]

			if tile.landscape {
				if j < len(bottom) && top < bottom[j+1] { // Avoid overlapping next img in above row
					top = bottom[j+1]
				}
				newWidth = newWidth*2 + gap
				scale = newWidth / tile.width
				newHeight = tile.height * scale
				// fmt.Println("Scale landscape:", scale)
			}

			o.Resize(fyne.NewSize(newWidth, newHeight))
			o.Move(fyne.NewPos(prevLeft, top))

			bottom[j] = top + newHeight + gap
			if tile.landscape && j < len(bottom) {
				j++
				bottom[j] = bottom[j-1]
			}
			prevLeft = prevLeft + newWidth + gap
			layout.minHeight = bottom[j]

			if tilesPerRow-j == 2 && peakLandscape(i) {
				j++
			}
			i++
		}
	}
}

func (layout *TileLayout) tileFromCache(path string) (*Tile, bool) {
	layout.cacheLock.Lock()
	defer layout.cacheLock.Unlock()

	t, ok := layout.cachedTiles[path]

	return t, ok
}

func (layout *TileLayout) tileToCache(path string, tile *Tile) {
	layout.cacheLock.Lock()
	defer layout.cacheLock.Unlock()

	layout.cachedTiles[path] = tile
}

func (layout *TileLayout) imageLoader() {
	i := 0
	refreshTimer := time.NewTimer(500 * time.Millisecond)
	go func() {
		for {
			<-refreshTimer.C
			layout.grid.Refresh()
		}
	}()

	for tc := range layout.imagesToLoad {
		layout.currentlyLoading.Add(1)

		var tile *Tile
		if t, ok := layout.tileFromCache(tc.Path); ok {
			tile = t
		} else {
			reader, err := tc.GetReader()
			if err != nil {
				fmt.Println(err)
				continue
			}
			tile = layout.NewImageTile(reader, tc, layout.tabFn)
			layout.tileToCache(tc.Path, tile)
		}
		layout.tiles[tc.order-layout.offset] = tile
		layout.grid.Objects[tc.order-layout.offset] = tile

		if i == 10 { // Refresh grid every 10 images
			layout.grid.Refresh()
			i = 0
		} else {
			refreshTimer.Reset(500 * time.Millisecond) // Refresh grid 500 ms after last loaded image
		}

		layout.currentlyLoading.Done()
		i++
	}
}

func (layout *TileLayout) NewImageTile(imgReader io.ReadSeeker, context *ImageInfo, tabFn func(t *Tile)) *Tile {
	t := &Tile{
		Viewer: layout.viewer,
	}
	decoded, _, _ := Decode(imgReader)
	if decoded == nil {
		na := bytes.NewReader(loading)
		decoded2, _, _ := Decode(na)
		decoded = decoded2
	}
	tileWidth := int(layout.config.General.TileWidth)
	if decoded.Bounds().Max.X > decoded.Bounds().Max.Y {
		tileWidth = int(layout.config.General.TileWidth * 2)
	}
	var img *canvas.Image
	if tileWidth > decoded.Bounds().Max.X {
		img = canvas.NewImageFromImage(decoded) // do not resize if picture is smaller than tile
		decoded = nil
	} else {
		scaled := scaleImage(decoded, tileWidth)
		decoded = nil
		img = canvas.NewImageFromImage(scaled)
	}
	img.ScaleMode = canvas.ImageScaleFastest
	img.FillMode = canvas.ImageFillContain
	t.Info = context
	t.width = float32(img.Image.Bounds().Max.X)
	t.height = float32(img.Image.Bounds().Max.Y)
	t.landscape = t.width > t.height
	t.content = img
	t.tabFn = tabFn

	t.ExtendBaseWidget(t)

	return t
}

func (t *Tile) Tapped(_ *fyne.PointEvent) {
	t.tabFn(t)
}

func (layout *TileLayout) InitHotkeys() {
	layout.hotkeys = []Hotkey{}
	bindings := layout.config.Gallery

	add := func(h Hotkey) {
		layout.hotkeys = append(layout.hotkeys, h)
	}

	for _, x := range bindings.Quit {
		add(Hotkey{x, func() {
			layout.app.Quit()
		}})
	}
	for _, x := range bindings.ScrollDown {
		add(Hotkey{x, func() {
			layout.viewer.scroll.Offset.Y = layout.viewer.scroll.Offset.Y + 300
			layout.viewer.scroll.Refresh()
		}})
	}
	for _, x := range bindings.ScrollUp {
		add(Hotkey{x, func() {
			layout.viewer.scroll.Offset.Y = layout.viewer.scroll.Offset.Y - 300
			layout.viewer.scroll.Refresh()
		}})
	}
	for _, x := range bindings.PathLevelUp {
		add(Hotkey{x, func() {
			layout.viewer.ShowImageDir(filepath.Dir(layout.viewer.currentPath))
		}})
	}
}

func (ta *Tile) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(ta.content)
}
