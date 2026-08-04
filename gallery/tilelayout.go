package gallery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	stdraw "image/draw"
	"image/jpeg"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	// "path/filepath"
	// "archive/zip"
	"bytes"
	"fmt"
	"time"

	_ "embed"

	"fyne.io/fyne/v2"

	"sync"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/disintegration/imaging"
)

//go:embed loading.png
var loading []byte

// labelHeight is the fixed pixel height reserved below each tile for the name
// label when filenames are shown.
const labelHeight = float32(22)

// Thumbnailer supplies scaled thumbnails for gallery items. When the viewer's
// Thumbnailer field is nil, thumbnails are generated from the image content
// and cached in the directory given by GeneralConfig.ThumbnailDir.
type Thumbnailer interface {
	GetThumbnail(info *ImageInfo) (io.ReadSeeker, error)
}

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
	viewer           *Viewer
	offset           int
	currentlyLoading sync.WaitGroup
	cachedTiles      map[string]*Tile
	cacheLock        sync.Mutex
	showLabels       bool
}

type Tile struct {
	widget.BaseWidget
	Content   *canvas.Image
	width     float32
	height    float32
	landscape bool
	Viewer    *Viewer
	Info      *ImageInfo
	tabFn     func(t *Tile)
	nameLabel *widget.Label
	layout    *TileLayout // reference for showLabels state
}

type ImageInfo struct {
	Path        string
	FullPath    string // Used to get path of zipFile
	// DirPath is the primary sort key: the directory or container that this
	// entry logically belongs to (subdir path, archive path, or parent dir).
	DirPath     string
	// DisplayName is the text shown below the tile (dirname for dirs, filename otherwise).
	DisplayName string
	// PreviewPaths holds up to 4 absolute image paths used to generate the
	// directory composite thumbnail.
	PreviewPaths      []string
	ShowArchive       bool
	CustomReader      CustomReader
	OnTapped          func()
	OnDoubleTapped    func()
	OnTappedSecondary func()
	// OnOpen, when non-nil, replaces the default image display when the
	// entry is opened (tile tap, next/prev navigation) — e.g. to browse
	// into a directory the entry represents. Wired automatically from
	// CustomReader when it implements Openable.
	OnOpen func()

	archiveName       string
	archiveFile       fs.FS
	order             int
	InputIsArchive    bool
	InputIsDir        bool
	InputIsReader     bool
	InputIsVideo      bool
	IsZoomable        bool
	IsDraggable       bool
	ThumbnailIsScaled bool
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

func NewTileLayout(config Config, window fyne.Window, app fyne.App, viewer *Viewer, tabFn func(t *Tile)) *TileLayout {
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
		showLabels:   true,
	}

	for i := 0; i < config.General.Workers; i++ {
		go layout.imageLoader()
	}

	return layout
}

func (layout *TileLayout) Clear() {
	layout.tiles = make([]*Tile, 0)
	layout.offset = 0
}

func (layout *TileLayout) PlaceTiles(imageFiles []*ImageInfo) {
	loadingImg := bytes.NewReader(loading)
	end := layout.offset + layout.config.General.ImagesPerPage
	if end > len(imageFiles) {
		end = len(imageFiles)
	}
	// Start each page with a fresh tile list, indexed relative to
	// layout.offset (same indexing imageLoader and Layout use).
	layout.tiles = make([]*Tile, 0, end-layout.offset)
	fyne.Do(func() {
		layout.grid.Objects = make([]fyne.CanvasObject, 0)
	})
	for i := layout.offset; i < end; i++ {
		tile, err := layout.NewImageTile(loadingImg, imageFiles[i], func(t *Tile) {})
		if err != nil {
			fmt.Println("Error loading tile:", err)
		} else {
			layout.tiles = append(layout.tiles, tile)
			tileCopy := tile // Capture tile in closure
			fyne.Do(func() {
				layout.grid.AddObject(tileCopy)
			})
			layout.imagesToLoad <- imageFiles[i]
		}
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

	// Extra height added below each tile image for the name label.
	extraH := float32(0)
	if layout.showLabels {
		extraH = labelHeight
	}

	peakLandscape := func(i int) bool {
		if i < len(layout.tiles)-1 {
			return layout.tiles[i+1].landscape
		}
		return false
	}
	if containerSize.Width < tileWidth+gap {
		return
	}
	// layout.tiles is reset and refilled by PlaceTiles independently of the
	// grid's objects; only lay out the indices that exist in both.
	n := len(objects)
	if len(layout.tiles) < n {
		n = len(layout.tiles)
	}
	for i := 0; i < n; {
		prevLeft := float32(int(containerSize.Width)%int(tileWidth)) / 3
		for j := 0; j < tilesPerRow && i < n; j++ {
			o := objects[i]
			tile := layout.tiles[i]
			newWidth := tileWidth
			scale := newWidth / tile.width
			newHeight := tile.height*scale + extraH
			// fmt.Println("Scale portrait:", scale)
			top := bottom[j]

			if tile.landscape {
				if j < len(bottom) && top < bottom[j+1] { // Avoid overlapping next img in above row
					top = bottom[j+1]
				}
				newWidth = newWidth*2 + gap
				scale = newWidth / tile.width
				newHeight = tile.height*scale + extraH
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

// ToggleLabels flips the filename label visibility for all current tiles and
// refreshes the grid layout.
func (layout *TileLayout) ToggleLabels() {
	layout.showLabels = !layout.showLabels
	for _, t := range layout.tiles {
		if t.nameLabel == nil {
			continue
		}
		if layout.showLabels {
			t.nameLabel.Show()
		} else {
			t.nameLabel.Hide()
		}
		t.Refresh()
	}
	fyne.Do(func() {
		layout.grid.Refresh()
	})
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
			fyne.Do(func() {
				layout.grid.Refresh()
			})
		}
	}()

	for tc := range layout.imagesToLoad {
		layout.currentlyLoading.Add(1)

		var tile *Tile
		if t, ok := layout.tileFromCache(tc.Path); ok {
			tile = t
		} else {
			thumb, err := layout.GetThumbnail(tc)
			if err != nil {
				fmt.Println("Error creating thumbnail:", err)
				layout.currentlyLoading.Done()
				continue
			}
			tile, err = layout.NewImageTile(thumb, tc, layout.tabFn)
			if err != nil {
				fmt.Println("Error creating tile:", err)
				layout.currentlyLoading.Done()
				continue
			}
			layout.tileToCache(tc.Path, tile)
		}
		tileCopy := tile
		idx := tc.order - layout.offset
		// tc may belong to a page that has since been replaced; only write
		// back when its slot still exists in the current page.
		if idx >= 0 && idx < len(layout.tiles) {
			layout.tiles[idx] = tile
		}
		fyne.Do(func() {
			if idx >= 0 && idx < len(layout.grid.Objects) {
				layout.grid.Objects[idx] = tileCopy
			}
		})

		if i == 20 { // Refresh grid every 10 images
			fyne.Do(func() {
				layout.grid.Refresh()
			})
			i = 0
		} else {
			refreshTimer.Reset(500 * time.Millisecond) // Refresh grid 500 ms after last loaded image
		}

		layout.currentlyLoading.Done()
		i++
	}
}

func (layout *TileLayout) GetThumbnail(context *ImageInfo) (io.ReadSeeker, error) {
	// Video files: extract a frame thumbnail for both local and
	// network-backed entries (the reader is seekable in both cases).
	if context.InputIsVideo {
		return layout.videoThumbnail(context)
	}

	// Directory tiles: generate a 2×2 composite of up to 4 preview images.
	// The in-memory tile cache (cachedTiles) prevents repeated generation
	// within a session, so we skip the disk cache here.
	if len(context.PreviewPaths) > 0 {
		data := layout.makeDirectoryComposite(context.PreviewPaths)
		context.ThumbnailIsScaled = true
		return bytes.NewReader(data), nil
	}

	// A custom Thumbnailer (e.g. one backed by network storage) takes
	// precedence over the local thumbnail directory.
	if layout.viewer.Thumbnailer != nil {
		return layout.viewer.Thumbnailer.GetThumbnail(context)
	}

	var thumbnail string
	var thumbnailDir string = layout.config.General.ThumbnailDir
	var reader io.ReadSeeker
	r, err := context.GetReader()
	if err != nil {
		return nil, err
	}
	r.Seek(0, io.SeekStart)
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	hash, err := contentHash(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	reader = bytes.NewReader(b)
	if len(hash) != 64 {
		return nil, errors.New("Invalid hash: " + hash)
	}
	max := lvlDeep * dirWidth
	for i := 0; i <= max; i = i + dirWidth {
		thumbnailDir = filepath.Join(thumbnailDir, hash[i:i+dirWidth])
	}
	thumbnail = filepath.Join(thumbnailDir, hash)
	if _, err := os.Stat(thumbnail); err == nil && !layout.viewer.refreshThumbs { // && false {
		reader, err = os.Open(thumbnail)
		if err != nil {
			return nil, err
		}
		context.ThumbnailIsScaled = true
		return reader, nil
	} else {
		err := os.MkdirAll(thumbnailDir, 0755)
		if err != nil {
			fmt.Println("Error creating thumbnail dir at:", thumbnailDir)
		} else {
			decoded, _, err := Decode(reader)
			if err != nil {
				return nil, err
			}
			tileWidth := int(layout.config.General.TileWidth)
			scaled := ScaleImage(decoded, tileWidth*2)
			decoded = nil
			buf := &bytes.Buffer{}
			err = jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90})
			if err == nil {
				if err := os.WriteFile(thumbnail, buf.Bytes(), 0755); err != nil {
					fmt.Println("Error writing thumbnail to:", thumbnail)
				}
			}
			context.ThumbnailIsScaled = true

			return bytes.NewReader(buf.Bytes()), nil
		}
	}

	return context.GetReader()
}

// makeDirectoryComposite generates a 2×2 grid thumbnail from up to 4 image
// paths. Each cell is tileWidth/2 × tileWidth/2; the composite is square.
func (layout *TileLayout) makeDirectoryComposite(paths []string) []byte {
	cellW := int(layout.config.General.TileWidth) / 2
	size := cellW * 2

	// Dark neutral background for unfilled cells.
	composite := imaging.New(size, size, color.NRGBA{R: 45, G: 45, B: 45, A: 255})

	positions := [4]image.Point{
		{X: 0, Y: 0},
		{X: cellW, Y: 0},
		{X: 0, Y: cellW},
		{X: cellW, Y: cellW},
	}

	for i, p := range paths {
		if i >= 4 {
			break
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		img, _, err := Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		// Center-crop each preview to a square cell.
		cell := imaging.Fill(img, cellW, cellW, imaging.Center, imaging.Lanczos)
		composite = imaging.Paste(composite, cell, positions[i])
	}

	buf := &bytes.Buffer{}
	jpeg.Encode(buf, composite, &jpeg.Options{Quality: 85})
	return buf.Bytes()
}

// videoThumbnail generates (or retrieves from disk cache) a thumbnail for a
// video entry (local file or network-backed CustomReader). It extracts a frame
// using ffmpeg, scales it to 2×tileWidth wide, and overlays a circular play
// icon in the top-left corner. On any failure it falls back to the loading
// placeholder.
func (layout *TileLayout) videoThumbnail(context *ImageInfo) (io.ReadSeeker, error) {
	tileW := int(layout.config.General.TileWidth)

	// Check disk cache before running ffmpeg.
	cachePath := layout.videoThumbnailCachePath(context.Path)
	if data, err := os.ReadFile(cachePath); err == nil && !layout.viewer.refreshThumbs {
		context.ThumbnailIsScaled = true
		return bytes.NewReader(data), nil
	}

	// Extract a frame from local file or network-backed reader.
	var frame image.Image
	if context.InputIsReader {
		if r, err := context.GetReader(); err == nil {
			frame = extractVideoFrameFromReader(r)
		}
	} else {
		frame = extractVideoFrame(context.Path)
	}
	if frame == nil {
		return bytes.NewReader(loading), nil
	}

	// Scale to 2×tileWidth wide, keeping aspect ratio.
	scaled := ScaleImage(frame, tileW*2)

	// Convert to NRGBA for pixel-level drawing.
	result := image.NewNRGBA(scaled.Bounds())
	stdraw.Draw(result, result.Bounds(), scaled, image.Point{}, stdraw.Src)

	// Overlay play icon in top-left corner.
	iconPx := tileW * 2 / 4 // ~25 % of image width; halved at display scale
	if iconPx < 24 {
		iconPx = 24
	}
	drawVideoPlayIcon(result, iconPx/4, iconPx/4, iconPx)

	// Encode and write to cache.
	buf := &bytes.Buffer{}
	jpeg.Encode(buf, result, &jpeg.Options{Quality: 90})
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
		os.WriteFile(cachePath, buf.Bytes(), 0644)
	}

	context.ThumbnailIsScaled = true
	return bytes.NewReader(buf.Bytes()), nil
}

// videoThumbnailCachePath returns the path under ThumbnailDir where the video
// thumbnail is cached, using a SHA-256 hash of the video file path as the key
// (avoiding reading the full video file just for hashing).
func (layout *TileLayout) videoThumbnailCachePath(videoPath string) string {
	sum := sha256.Sum256([]byte(videoPath))
	hash := "v" + hex.EncodeToString(sum[:])
	dir := layout.config.General.ThumbnailDir
	for i := 0; i < 3; i++ {
		// skip the "v" prefix when indexing into the hash for dir levels
		dir = filepath.Join(dir, hash[i*2+1:i*2+3])
	}
	return filepath.Join(dir, hash)
}

// extractVideoFrame runs ffmpeg to extract one frame from the video file at
// approximately the 1-second mark (input seeking, which is fast for local
// files). Falls back to frame 0 for very short videos. Returns nil if ffmpeg
// is unavailable or extraction fails.
func extractVideoFrame(videoPath string) image.Image {
	for _, ss := range []string{"00:00:01", "00:00:00"} {
		cmd := exec.Command("ffmpeg",
			"-ss", ss,
			"-i", videoPath,
			"-vframes", "1",
			"-f", "image2pipe",
			"-vcodec", "png",
			"-")
		cmd.Stderr = io.Discard
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			continue
		}
		img, _, err := image.Decode(&stdout)
		if err == nil {
			return img
		}
	}
	return nil
}

// extractVideoFrameFromReader pipes r into ffmpeg via stdin and extracts one
// frame. It uses output seeking (-ss after -i) so ffmpeg decodes-and-discards
// to reach the target timestamp — this works even for pipe/non-seekable
// streams. Falls back to frame 0 for very short videos.
func extractVideoFrameFromReader(r io.ReadSeeker) image.Image {
	attempts := [][]string{
		// output seek to 1 s
		{"-i", "pipe:0", "-ss", "00:00:01", "-vframes", "1", "-f", "image2pipe", "-vcodec", "png", "-"},
		// frame 0 fallback
		{"-i", "pipe:0", "-vframes", "1", "-f", "image2pipe", "-vcodec", "png", "-"},
	}
	for _, args := range attempts {
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			break // reader not seekable; try once without re-seeking
		}
		cmd := exec.Command("ffmpeg", args...)
		cmd.Stdin = r
		cmd.Stderr = io.Discard
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			continue
		}
		img, _, err := image.Decode(&stdout)
		if err == nil {
			return img
		}
	}
	return nil
}

// drawVideoPlayIcon overlays a semi-transparent circular play button at
// (x0, y0) with the given pixel size onto dst.
func drawVideoPlayIcon(dst *image.NRGBA, x0, y0, size int) {
	radius := size / 2
	cx := x0 + radius
	cy := y0 + radius
	bg := color.NRGBA{0, 0, 0, 160}
	fg := color.NRGBA{255, 255, 255, 230}
	b := dst.Bounds()

	setPixel := func(x, y int, c color.NRGBA) {
		if x >= b.Min.X && y >= b.Min.Y && x < b.Max.X && y < b.Max.Y {
			// Alpha-blend onto existing pixel.
			src := dst.NRGBAAt(x, y)
			a := float32(c.A) / 255
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(float32(c.R)*a + float32(src.R)*(1-a)),
				G: uint8(float32(c.G)*a + float32(src.G)*(1-a)),
				B: uint8(float32(c.B)*a + float32(src.B)*(1-a)),
				A: 255,
			})
		}
	}

	// Semi-transparent circle background.
	r2 := radius * radius
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= r2 {
				setPixel(cx+dx, cy+dy, bg)
			}
		}
	}

	// Right-pointing triangle (play symbol).
	pad := size / 5
	tLeft := x0 + pad
	tRight := x0 + size - pad
	tHalfH := radius - pad
	for y := cy - tHalfH; y <= cy+tHalfH; y++ {
		dy := y - cy
		if dy < 0 {
			dy = -dy
		}
		t := float32(dy) / float32(tHalfH)
		xLeft := tLeft + int(float32(tRight-tLeft)*t)
		for x := xLeft; x <= tRight; x++ {
			setPixel(x, y, fg)
		}
	}
}

const (
	lvlDeep  = 3
	dirWidth = 2
	max      = lvlDeep * dirWidth
)

func (layout *TileLayout) NewImageTile(reader io.ReadSeeker, context *ImageInfo, tabFn func(t *Tile)) (*Tile, error) {
	t := &Tile{
		Viewer: layout.viewer,
		layout: layout,
	}
	decoded, _, err := Decode(reader)
	if err != nil {
		fmt.Println("Error decoding image when creating new tile:", err, context)
	}
	if decoded == nil {
		na := bytes.NewReader(loading)
		decoded2, _, _ := Decode(na)
		decoded = decoded2
	}
	img := canvas.NewImageFromImage(decoded) // do not resize if picture is smaller than tile
	decoded = nil

	img.ScaleMode = canvas.ImageScaleFastest
	img.FillMode = canvas.ImageFillContain
	t.Info = context
	t.width = float32(img.Image.Bounds().Max.X)
	t.height = float32(img.Image.Bounds().Max.Y)
	t.landscape = t.width > t.height
	t.Content = img
	t.tabFn = tabFn

	// Create name label using the entry's display name.
	if context.DisplayName != "" {
		lbl := widget.NewLabel(context.DisplayName)
		lbl.Alignment = fyne.TextAlignCenter
		lbl.Truncation = fyne.TextTruncateEllipsis
		if !layout.showLabels {
			lbl.Hide()
		}
		t.nameLabel = lbl
	}

	t.ExtendBaseWidget(t)

	return t, nil
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
	for _, x := range bindings.ToggleFilenames {
		add(Hotkey{x, func() {
			layout.ToggleLabels()
		}})
	}
}

// TileRenderer renders the tile image with an optional name label below it.
type TileRenderer struct {
	tile *Tile
}

func (ta *Tile) CreateRenderer() fyne.WidgetRenderer {
	return &TileRenderer{tile: ta}
}

func (r *TileRenderer) Layout(size fyne.Size) {
	if r.tile.nameLabel != nil && r.tile.nameLabel.Visible() {
		lh := labelHeight
		r.tile.Content.Resize(fyne.NewSize(size.Width, size.Height-lh))
		r.tile.Content.Move(fyne.NewPos(0, 0))
		r.tile.nameLabel.Resize(fyne.NewSize(size.Width, lh))
		r.tile.nameLabel.Move(fyne.NewPos(0, size.Height-lh))
	} else {
		r.tile.Content.Resize(size)
		r.tile.Content.Move(fyne.NewPos(0, 0))
	}
}

func (r *TileRenderer) MinSize() fyne.Size {
	return fyne.NewSize(50, 50)
}

func (r *TileRenderer) Refresh() {
	r.tile.Content.Refresh()
	if r.tile.nameLabel != nil {
		r.tile.nameLabel.Refresh()
	}
}

func (r *TileRenderer) Objects() []fyne.CanvasObject {
	if r.tile.nameLabel != nil {
		return []fyne.CanvasObject{r.tile.Content, r.tile.nameLabel}
	}
	return []fyne.CanvasObject{r.tile.Content}
}

func (r *TileRenderer) Destroy() {}


