package main

import (
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
	tiles        []*Tile
	wg           sync.WaitGroup
	tileWidth    float32
	gap          float32
	minHeight    float32
	imagesToLoad chan ImageInfo
	tabFn        func(t *Tile)
	grid         *fyne.Container
	hotkeys      []Hotkey
	config       Config
	window       fyne.Window
	app          fyne.App
	viewer       *ImageViewer
}

type Tile struct {
	widget.BaseWidget
	content   *canvas.Image
	width     float32
	height    float32
	landscape bool
	info      ImageInfo
	tabFn     func(t *Tile)
}

type ImageInfo struct {
	archiveFile    fs.FS
	inputIsArchive bool
	path           string
	order          int
}

func NewTileLayout(config Config, window fyne.Window, app fyne.App, viewer *ImageViewer, tileWidth float32, gap float32, workers int, tabFn func(t *Tile)) *TileLayout {
	batchSize := 1024
	tiles := make([]*Tile, 0)
	imagesToLoad := make(chan ImageInfo, batchSize)
	layout := &TileLayout{
		tiles:        tiles,
		wg:           sync.WaitGroup{},
		tileWidth:    tileWidth,
		gap:          gap,
		minHeight:    0,
		imagesToLoad: imagesToLoad,
		tabFn:        tabFn,
		config:       config,
		window:       window,
		app:          app,
		viewer:       viewer,
	}

	for i := 0; i < workers; i++ {
		go layout.imageLoader()
	}

	return layout
}

func (layout *TileLayout) AddTiles(imageFiles []ImageInfo) {
	loadingImg := bytes.NewReader(loading)
	for _, x := range imageFiles {
		t := x
		tile := layout.NewImageTile(loadingImg, t, nil)
		layout.tiles = append(layout.tiles, tile)
		layout.grid.AddObject(tile)
		layout.imagesToLoad <- t
	}
}

func (d *TileLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w := float32(d.tileWidth + d.gap)
	return fyne.NewSize(w, d.minHeight)
}

func (d *TileLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	tilesPerRow := int(containerSize.Width / d.tileWidth)
	bottom := make([]float32, tilesPerRow+1)

	peakLandscape := func(i int) bool {
		if i < len(d.tiles)-1 {
			return d.tiles[i+1].landscape
		}
		return false
	}
	if containerSize.Width < d.tileWidth+d.gap {
		return
	}
	for i := 0; i < len(objects); {
		prevLeft := float32(int(containerSize.Width)%int(d.tileWidth)) / 3
		for j := 0; j < tilesPerRow && i < len(objects); j++ {
			o := objects[i]
			tile := d.tiles[i]
			newWidth := d.tileWidth
			scale := newWidth / tile.width
			newHeight := tile.height * scale
			// fmt.Println("Scale portrait:", scale)
			top := bottom[j]
			if j < len(bottom) && top < bottom[j+1] {
				top = bottom[j+1]
			}

			if tile.landscape {
				newWidth = newWidth*2 + d.gap
				scale = newWidth / tile.width
				newHeight = tile.height * scale
				// fmt.Println("Scale landscape:", scale)
			}

			o.Resize(fyne.NewSize(newWidth, newHeight))
			o.Move(fyne.NewPos(prevLeft, top))

			bottom[j] = top + newHeight + d.gap
			if tile.landscape && j < len(bottom) {
				j++
				bottom[j] = bottom[j-1]
			}
			prevLeft = prevLeft + newWidth + d.gap
			d.minHeight = bottom[j]

			if tilesPerRow-j == 2 && peakLandscape(i) {
				j++
			}
			i++
		}
	}
}

func (layout *TileLayout) imageLoader() {
	i := 0
	refreshTimer := time.NewTimer(500 * time.Millisecond)

	first := true
	for tc := range layout.imagesToLoad {
		reader, err := tc.GetReader()
		if err != nil {
			fmt.Println(err)
			continue
		}
		tile := layout.NewImageTile(reader, tc, layout.tabFn)
		layout.tiles[tc.order] = tile
		layout.grid.Objects[tc.order] = tile
		if first {
			go func() {
				<-refreshTimer.C
				layout.grid.Refresh()
			}()
		}
		if i == 10 {
			layout.grid.Refresh()
			i = 0
		} else {
			refreshTimer.Reset(500 * time.Millisecond)
		}
	}
}

func (layout *TileLayout) NewImageTile(imgReader io.ReadSeeker, context ImageInfo, tabFn func(t *Tile)) *Tile {
	t := &Tile{}
	decoded, _, _ := Decode(imgReader)
	if decoded == nil {
		na := bytes.NewReader(loading)
		decoded2, _, _ := Decode(na)
		decoded = decoded2
	}
	tileWidth := int(layout.tileWidth)
	if decoded.Bounds().Max.X > decoded.Bounds().Max.Y {
		tileWidth = int(layout.tileWidth * 2)
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
	t.info = context
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
}

func (ta *Tile) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(ta.content)
}
