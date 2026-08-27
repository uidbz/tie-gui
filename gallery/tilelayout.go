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
	"os"
	"path/filepath"

	// "path/filepath"
	// "archive/zip"
	"bytes"
	"strconv"
	"time"

	_ "embed"

	"fyne.io/fyne/v2"

	"sync"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"git.sr.ht/~uid/imgview/mpvplayer"
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
	results          chan loadedTile
	tabFn            func(t *Tile)
	grid             *fyne.Container
	hotkeys          []Hotkey
	config           Config
	window           fyne.Window
	app              fyne.App
	viewer           *Gallery // TODO Phase 3: remove this back-reference
	offset           int
	currentlyLoading sync.WaitGroup
	tileCache        *tileCache
	showLabels       bool
	// Direct access to avoid back-reference through viewer
	thumbnailer   Thumbnailer
	refreshThumbs bool
	// Pagination button (shown at end of gallery when more pages exist)
	nextPageButton *widget.Button
}

// loadedTile carries a finished thumbnail tile from a loader worker to the
// tileUpdater goroutine, which batches UI-thread write-backs.
type loadedTile struct {
	info *ImageInfo
	tile *Tile
}

const (
	// flushBatchSize is the number of loaded tiles that triggers an
	// immediate UI write-back; smaller batches are debounced by flushInterval.
	flushBatchSize = 32
	// flushInterval is the trailing debounce for tile write-backs during
	// thumbnail loading. One batched relayout per interval replaces the old
	// per-20-images grid.Refresh() storm (which re-uploaded every texture).
	flushInterval = 120 * time.Millisecond
)

type Tile struct {
	widget.BaseWidget
	Content   *canvas.Image
	width     float32
	height    float32
	landscape bool
	Viewer    *Gallery
	Info      *ImageInfo
	tabFn     func(t *Tile)
	nameLabel *widget.Label
	subLabel  *widget.Label // optional second line (e.g. artist), nil when unused
	layout    *TileLayout   // reference for showLabels state
	// swipeOverlay covers directory/archive tiles (PreviewPaths != nil) and
	// turns horizontal drags into preview cycling; nil for regular images.
	swipeOverlay *dirSwipeOverlay
}

func NewTileLayout(config Config, window fyne.Window, app fyne.App, viewer *Gallery, tabFn func(t *Tile)) *TileLayout {
	batchSize := 1024
	tiles := make([]*Tile, 0)
	imagesToLoad := make(chan *ImageInfo, batchSize)

	// Cache size based on platform: smaller on mobile to save memory
	maxCacheSize := 500
	if viewer.Platform().IsMobile() {
		maxCacheSize = 150
	}

	// Default: labels off to save vertical space (mobile optimization)
	showLabels := false
	// Desktop users can toggle with 'N' key
	if !viewer.Platform().IsMobile() {
		showLabels = false // Keep off by default for desktop too
	}

	layout := &TileLayout{
		tiles:         tiles,
		wg:            sync.WaitGroup{},
		minHeight:     0,
		imagesToLoad:  imagesToLoad,
		results:       make(chan loadedTile, batchSize),
		tabFn:         tabFn,
		config:        config,
		window:        window,
		app:           app,
		viewer:        viewer,
		tileCache:     newTileCache(maxCacheSize),
		showLabels:    showLabels,
		thumbnailer:   viewer.Thumbnailer,
		refreshThumbs: viewer.refreshThumbs,
	}

	for i := 0; i < config.General.Workers; i++ {
		go layout.imageLoader()
	}
	go layout.tileUpdater()

	return layout
}

func (layout *TileLayout) Clear() {
	layout.tiles = make([]*Tile, 0)
	layout.offset = 0
}

func (layout *TileLayout) PlaceTiles(imageFiles []*ImageInfo) {
	// Decode the placeholder image once and share it across all placeholder
	// tiles on this page (previously each tile decoded loading.png again
	// because the shared reader was consumed after the first decode).
	placeholder, _, err := Decode(bytes.NewReader(loading))
	if err != nil || placeholder == nil {
		placeholder = image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}

	end := layout.offset + layout.config.General.ImagesPerPage
	if end > len(imageFiles) {
		end = len(imageFiles)
	}
	// Start each page with a fresh tile list, indexed relative to
	// layout.offset (same indexing imageLoader and Layout use). Build it
	// locally on this background goroutine; layout.tiles is assigned on the
	// UI thread together with the grid objects.
	newTiles := make([]*Tile, 0, end-layout.offset)

	// Create all tiles on the background thread first
	for i := layout.offset; i < end; i++ {
		tile := layout.newImageTileFromImage(placeholder, imageFiles[i], func(t *Tile) {})
		newTiles = append(newTiles, tile)
		layout.currentlyLoading.Add(1)
		layout.imagesToLoad <- imageFiles[i]
	}

	// Now add all tiles to the grid atomically on the UI thread
	fyne.Do(func() {
		layout.tiles = newTiles
		layout.grid.Objects = make([]fyne.CanvasObject, 0, len(newTiles))
		for _, tile := range newTiles {
			layout.grid.Objects = append(layout.grid.Objects, tile)
		}
	})

	// Add "Next Page" button at the end if there are more pages
	// Calculate if we're on the last page
	imagesPerPage := layout.config.General.ImagesPerPage
	totalImages := len(imageFiles)
	currentPage := layout.offset / imagesPerPage
	maxPages := totalImages / imagesPerPage
	if totalImages%imagesPerPage != 0 {
		maxPages++
	}

	if currentPage < maxPages-1 {
		if layout.nextPageButton == nil {
			layout.nextPageButton = widget.NewButton("Load Next Page ▼", func() {
				if layout.viewer != nil {
					layout.viewer.ChangePage(currentPage + 1)
				}
			})
			layout.nextPageButton.Importance = widget.HighImportance
		} else {
			// Update button text in case page number changed
			layout.nextPageButton.SetText("Load Next Page ▼")
			layout.nextPageButton.OnTapped = func() {
				if layout.viewer != nil {
					layout.viewer.ChangePage(currentPage + 1)
				}
			}
		}
		fyne.Do(func() {
			layout.grid.Add(layout.nextPageButton)
		})
	}

	// Trigger an initial layout pass to ensure tiles are visible
	// This is critical for the first page load when scroll offset is 0.
	// relayoutGrid positions tiles without invalidating any textures
	// (unlike grid.Refresh, which recursively refreshes all children).
	fyne.Do(func() {
		layout.relayoutGrid()
	})
}

// relayoutGrid re-runs the justified layout and marks the canvas dirty
// WITHOUT recursively refreshing child widgets. grid.Refresh() would queue
// every tile's canvas.Image for texture deletion, forcing a re-upload of
// every visible thumbnail on the next frame; this helper only repositions
// tiles (Move/Resize mark the canvas dirty on their own) and queues the
// container, which the painter explicitly handles without touching image
// textures. Must be called on the UI thread (via fyne.Do).
func (layout *TileLayout) relayoutGrid() {
	layout.Layout(layout.grid.Objects, layout.grid.Size())
	canvas.Refresh(layout.grid)
}

func (layout *TileLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w := float32(layout.config.General.TileWidth + layout.config.General.TileGap)
	return fyne.NewSize(w, layout.minHeight)
}

// Layout implements a justified row layout with virtual scrolling: tiles are
// grouped into rows and scaled so each row fills the full container width with
// no horizontal gaps. Only visible rows (plus a buffer) are rendered to reduce
// memory usage.
//
// The target row height is TileWidth from the config (default 300 px). Rows
// accumulate tiles until the computed row height drops to that target, at which
// point the row is finalised. The last partial row is capped at the target
// height so a single straggler image does not stretch to fill the window.
func (layout *TileLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	gap := layout.config.General.TileGap
	targetH := layout.config.General.TileWidth

	// Extra height below each tile image for the optional filename label, or
	// two lines when any tile carries a subtitle (title + artist).
	extraH := float32(0)
	if layout.showLabels {
		extraH = labelHeight
		for _, t := range layout.tiles {
			if t != nil && t.subLabel != nil {
				extraH = 2 * labelHeight
				break
			}
		}
	}

	// layout.tiles is reset and refilled by PlaceTiles independently of the
	// grid's objects; only lay out the indices that exist in both.
	// Note: objects may include a pagination button at the end, which is not a tile.
	n := len(objects)
	hasNextButton := false
	if len(objects) > len(layout.tiles) && layout.nextPageButton != nil {
		// Last object is the next page button
		n = len(layout.tiles)
		hasNextButton = true
	}
	if len(layout.tiles) < n {
		n = len(layout.tiles)
	}
	if n == 0 || containerSize.Width < targetH {
		return
	}

	currentY := float32(0)
	i := 0

	for i < n {
		rowStart := i
		sumAspect := float32(0)

		// Accumulate tiles into the current row until the row height falls to
		// targetH. Each iteration we add one tile and check whether the row is
		// "full" (its height would be ≤ targetH). We break as soon as that
		// threshold is crossed, locking in the row boundary.
		for i < n {
			tile := layout.tiles[i]
			aspect := tile.width / tile.height
			if aspect <= 0 {
				aspect = 1.0 // safety: avoid division by zero for malformed tiles
			}
			sumAspect += aspect
			i++

			numGaps := float32(i - rowStart - 1)
			availW := containerSize.Width - numGaps*gap
			rowH := availW / sumAspect
			if rowH <= targetH {
				break
			}
		}

		// Compute the actual row height from the finalised aspect-ratio sum.
		rowCount := i - rowStart
		numGaps := float32(rowCount - 1)
		availW := containerSize.Width - numGaps*gap
		rowH := availW / sumAspect

		// Last (possibly incomplete) row: cap height so a handful of tall
		// images do not blow up to an enormous size.
		if i == n && rowH > targetH {
			rowH = targetH
		}

		// Place every tile in the row at its justified width and shared height.
		// Off-screen tiles are culled by Fyne's painter against the scroll
		// container's clip rect, so all tiles are positioned unconditionally.
		x := float32(0)
		for k := rowStart; k < i; k++ {
			tile := layout.tiles[k]
			aspect := tile.width / tile.height
			if aspect <= 0 {
				aspect = 1.0
			}
			tileW := aspect * rowH
			objects[k].Resize(fyne.NewSize(tileW, rowH+extraH))
			objects[k].Move(fyne.NewPos(x, currentY))
			x += tileW + gap
		}

		currentY += rowH + extraH + gap
	}

	// Position the "Next Page" button at the end if present
	if hasNextButton && layout.nextPageButton != nil {
		buttonHeight := float32(60) // Large, easily tappable button
		if layout.viewer != nil && layout.viewer.Platform().IsMobile() {
			buttonHeight = 80 // Even larger on mobile
		}
		layout.nextPageButton.Resize(fyne.NewSize(containerSize.Width, buttonHeight))
		layout.nextPageButton.Move(fyne.NewPos(0, currentY))
		currentY += buttonHeight + gap
	}

	if currentY > gap {
		layout.minHeight = currentY - gap
	} else {
		layout.minHeight = currentY
	}
}

// ToggleLabels flips the filename label visibility for all current tiles and
// relayouts the grid. Label Show/Hide marks the canvas dirty by itself; the
// tile renderer reads nameLabel.Visible() during its Layout, so a single
// relayoutGrid suffices — no per-tile Refresh (which would invalidate every
// tile texture).
func (layout *TileLayout) ToggleLabels() {
	layout.showLabels = !layout.showLabels
	for _, t := range layout.tiles {
		for _, lbl := range []*widget.Label{t.nameLabel, t.subLabel} {
			if lbl == nil {
				continue
			}
			if layout.showLabels {
				lbl.Show()
			} else {
				lbl.Hide()
			}
		}
	}
	fyne.Do(func() {
		layout.relayoutGrid()
	})
}

func (layout *TileLayout) tileFromCache(path string) (*Tile, bool) {
	return layout.tileCache.get(path)
}

func (layout *TileLayout) tileToCache(path string, tile *Tile) {
	layout.tileCache.put(path, tile)
}

// imageLoader is a worker goroutine that drains imagesToLoad, builds (or
// fetches from cache) the real thumbnail tile, and forwards it to the
// tileUpdater for batched UI write-back. currentlyLoading.Add is called by
// PlaceTiles before enqueueing; each item is Done here exactly once (or by
// the page-drain loops in ChangePage/ChangeGallery if discarded).
func (layout *TileLayout) imageLoader() {
	for tc := range layout.imagesToLoad {
		var tile *Tile
		if t, ok := layout.tileFromCache(tc.Path); ok {
			tile = t
		} else {
			thumb, err := layout.GetThumbnail(tc)
			if err != nil {
				// Skip thumbnails that fail to generate
				layout.currentlyLoading.Done()
				continue
			}
			tile, err = layout.NewImageTile(thumb, tc, layout.tabFn)
			if err != nil {
				// Skip tiles that fail to decode
				layout.currentlyLoading.Done()
				continue
			}
			layout.tileToCache(tc.Path, tile)
		}
		layout.results <- loadedTile{info: tc, tile: tile}
		layout.currentlyLoading.Done()
	}
}

// tileUpdater batches loaded tiles and applies them to the grid in a single
// UI-thread task per flush (every flushInterval trailing, or immediately at
// flushBatchSize). This replaces the previous scheme (grid.Refresh every 20
// thumbnails plus a 500 ms timer per worker), which recursively refreshed
// every child widget and forced the painter to delete and re-upload ALL
// visible tile textures on the next frame — the main source of scroll jank
// while a page was loading.
func (layout *TileLayout) tileUpdater() {
	var pending []loadedTile
	timer := time.NewTimer(flushInterval)
	// stopTimer stops the debounce timer and non-blockingly drains any
	// pending tick, so a later Reset never observes a stale fire.
	stopTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	stopTimer()

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := pending
		pending = nil
		fyne.Do(func() {
			changed := false
			for _, r := range batch {
				idx := r.info.order - layout.offset
				// r.info may belong to a page that has since been replaced;
				// only write back when its slot still exists AND still
				// holds the placeholder created for this exact ImageInfo.
				if idx < 0 || idx >= len(layout.tiles) || layout.tiles[idx].Info != r.info {
					continue
				}
				layout.tiles[idx] = r.tile
				if idx < len(layout.grid.Objects) {
					layout.grid.Objects[idx] = r.tile
				}
				changed = true
			}
			if changed {
				layout.relayoutGrid()
			}
		})
	}

	for {
		select {
		case r := <-layout.results:
			pending = append(pending, r)
			stopTimer()
			if len(pending) >= flushBatchSize {
				flush()
			} else {
				timer.Reset(flushInterval)
			}
		case <-timer.C:
			flush()
		}
	}
}

func (layout *TileLayout) GetThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	// Video files: extract a frame thumbnail for both local and
	// network-backed entries (the reader is seekable in both cases).
	if info.InputIsVideo {
		return layout.videoThumbnail(info)
	}

	// Directory and archive tiles: thumbnail of the currently selected
	// preview image (previewIndex, changed by swiping the tile) with a
	// semi-transparent folder icon overlay. Entries whose CustomReader can
	// supply previews (PreviewProvider) enter this branch too; the preview
	// list is resolved lazily inside dirPreviewThumbnail. On failure the
	// code falls through to the Thumbnailer (e.g. folder icon) or the
	// generic path instead of leaving the tile on the placeholder.
	_, hasPreviewProvider := info.CustomReader.(PreviewProvider)
	if len(info.PreviewPaths) > 0 || len(info.PreviewReaders) > 0 || hasPreviewProvider {
		data, err := layout.dirPreviewThumbnail(info)
		if err == nil {
			info.ThumbnailIsScaled = true
			return bytes.NewReader(data), nil
		}
	}

	// A custom Thumbnailer (e.g. one backed by network storage) takes
	// precedence over the local thumbnail directory.
	if layout.thumbnailer != nil {
		return layout.thumbnailer.GetThumbnail(info)
	}

	var thumbnail string
	var thumbnailDir string = layout.config.General.ThumbnailDir
	var reader io.ReadSeeker
	r, err := info.GetReader()
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
	if _, err := os.Stat(thumbnail); err == nil && !layout.refreshThumbs { // && false {
		reader, err = os.Open(thumbnail)
		if err != nil {
			return nil, err
		}
		info.ThumbnailIsScaled = true
		return reader, nil
	} else {
		err := os.MkdirAll(thumbnailDir, 0755)
		if err != nil {
			// Cache dir creation failed; thumbnail will not be cached on disk
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
				// Write to disk cache; ignore errors (thumbnail still returned in-memory)
				os.WriteFile(thumbnail, buf.Bytes(), 0755)
			}
			info.ThumbnailIsScaled = true

			return bytes.NewReader(buf.Bytes()), nil
		}
	}

	return info.GetReader()
}

// dirPreviewThumbnail generates (or retrieves from disk cache) the thumbnail
// for a directory or archive tile: the preview image at info.previewIndex,
// scaled to 2×tileWidth wide, with a semi-transparent folder icon overlaid in
// the top-left corner (the same way video tiles get a play icon).
//
// Previews come from PreviewPaths (local directories: file paths; local
// archives: member paths read via archiveFile) or from PreviewReaders
// (CustomReader-backed entries such as tie directories and archive blobs).
// The disk cache key is the preview's content hash with a "d" suffix so the
// overlaid thumbnail never collides with the same image's plain thumbnail.
func (layout *TileLayout) dirPreviewThumbnail(info *ImageInfo) ([]byte, error) {
	tileWidth := int(layout.config.General.TileWidth)
	var cacheKey string
	var scaled image.Image

	if len(info.PreviewPaths) > 0 {
		idx := info.previewIndex
		if idx < 0 || idx >= len(info.PreviewPaths) {
			idx = 0
		}
		previewPath := info.PreviewPaths[idx]

		// Read the preview image content: from the archive for archive
		// entries, from disk for directory entries.
		var b []byte
		if info.InputIsArchive {
			if info.archiveFile == nil {
				return nil, errors.New("archive not readable")
			}
			f, err := info.archiveFile.Open(previewPath)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			b, err = io.ReadAll(f)
			if err != nil {
				return nil, err
			}
		} else {
			var err error
			b, err = os.ReadFile(previewPath)
			if err != nil {
				return nil, err
			}
		}

		hash, err := contentHash(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if len(hash) != 64 {
			return nil, errors.New("Invalid hash: " + hash)
		}
		cacheKey = hash
		if data, ok := layout.dirPreviewCache(cacheKey); ok {
			return data, nil
		}
		decoded, _, err := Decode(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		scaled = ScaleImage(decoded, tileWidth*2)
	} else {
		// Cover fast path: entries with a ready-made cover thumbnail
		// (CoverProvider, e.g. a server-cached tie archive cover) serve the
		// tile's initial view WITHOUT enumerating the collection — no
		// archive download, no directory query.
		if cp, ok := info.CustomReader.(CoverProvider); ok &&
			info.previewIndex == 0 && len(info.PreviewReaders) == 0 {
			cacheKey = readerCacheKey(info.CustomReader.Path())
			if data, ok := layout.dirPreviewCache(cacheKey); ok {
				return data, nil
			}
			if rs, err := cp.CoverThumbnail(); err == nil {
				decoded, _, derr := Decode(rs)
				if derr != nil {
					return nil, derr
				}
				return layout.finishDirPreview(cacheKey, decoded)
			}
			// Cover unavailable: fall through to the preview-readers path;
			// the generated first preview is stored as the cover below.
		}

		// Lazily resolve the preview readers (PreviewProvider). This is
		// where collection enumeration happens — one cheap query for tie
		// directories, a full blob download for tie archives.
		if len(info.PreviewReaders) == 0 {
			if pp, ok := info.CustomReader.(PreviewProvider); ok {
				if pr, err := pp.Previews(); err == nil {
					info.PreviewReaders = pr
				}
			}
			if len(info.PreviewReaders) == 0 {
				return nil, errors.New("no previews available")
			}
		}
		idx := info.previewIndex
		if idx < 0 || idx >= len(info.PreviewReaders) {
			idx = 0
		}
		r := info.PreviewReaders[idx]
		cacheKey = readerCacheKey(r.Path())
		if data, ok := layout.dirPreviewCache(cacheKey); ok {
			return data, nil
		}

		// plainJPEG keeps the pre-badge thumbnail bytes so a CoverProvider
		// can store them as the entry's cover for next time.
		var plainJPEG []byte
		if layout.thumbnailer != nil {
			// Let the app's Thumbnailer (e.g. the tie filehost cache)
			// supply the already-scaled preview thumbnail.
			child := NewImageInfoCustomReader(0, r)
			child.Path = r.Path()
			rs, err := layout.thumbnailer.GetThumbnail(child)
			if err != nil {
				return nil, err
			}
			plainJPEG, err = io.ReadAll(rs)
			if err != nil {
				return nil, err
			}
			decoded, _, err := Decode(bytes.NewReader(plainJPEG))
			if err != nil {
				return nil, err
			}
			scaled = decoded
		} else {
			rs, err := r.GetReader()
			if err != nil {
				return nil, err
			}
			decoded, _, err := Decode(rs)
			if err != nil {
				return nil, err
			}
			scaled = ScaleImage(decoded, tileWidth*2)
			if _, ok := info.CustomReader.(CoverProvider); ok && info.previewIndex == 0 {
				buf := &bytes.Buffer{}
				if err := jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90}); err == nil {
					plainJPEG = buf.Bytes()
				}
			}
		}

		if cp, ok := info.CustomReader.(CoverProvider); ok &&
			info.previewIndex == 0 && plainJPEG != nil {
			cp.StoreCoverThumbnail(plainJPEG)
		}
	}

	return layout.finishDirPreview(cacheKey, scaled)
}

// readerCacheKey derives a 64-hex disk-cache key from a reader path: tie
// readers already use content hashes; anything else is hashed by name.
func readerCacheKey(path string) string {
	if len(path) == 64 {
		return path
	}
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

// finishDirPreview badges a scaled preview image with the folder icon,
// encodes it as JPEG, and writes it to the disk cache under cacheKey.
func (layout *TileLayout) finishDirPreview(cacheKey string, scaled image.Image) ([]byte, error) {
	// Convert to NRGBA for the icon overlay.
	result := image.NewNRGBA(scaled.Bounds())
	stdraw.Draw(result, result.Bounds(), scaled, image.Point{}, stdraw.Src)

	iconPx := int(layout.config.General.TileWidth) * 2 / 4 // ~25 % of image width; halved at display scale
	if iconPx < 24 {
		iconPx = 24
	}
	drawFolderIcon(result, iconPx/4, iconPx/4, iconPx)

	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, result, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	cachePath := layout.dirPreviewCachePath(cacheKey)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
		os.WriteFile(cachePath, buf.Bytes(), 0644)
	}
	return buf.Bytes(), nil
}

// dirPreviewCachePath returns the disk cache path for a directory/archive
// preview thumbnail: the 64-hex cache key split into directory levels, with
// a "d" suffix on the filename.
func (layout *TileLayout) dirPreviewCachePath(cacheKey string) string {
	thumbnailDir := layout.config.General.ThumbnailDir
	max := lvlDeep * dirWidth
	for i := 0; i <= max; i = i + dirWidth {
		thumbnailDir = filepath.Join(thumbnailDir, cacheKey[i:i+dirWidth])
	}
	return filepath.Join(thumbnailDir, cacheKey+"d")
}

// dirPreviewCache returns the cached preview thumbnail for cacheKey.
func (layout *TileLayout) dirPreviewCache(cacheKey string) ([]byte, bool) {
	if layout.refreshThumbs {
		return nil, false
	}
	data, err := os.ReadFile(layout.dirPreviewCachePath(cacheKey))
	return data, err == nil
}

// videoPreviewFrames is the number of frame thumbnails a video tile offers
// when swiped horizontally. Frames are spread across the duration by seek
// percent, so any video length works without probing it first.
const videoPreviewFrames = 10

// videoThumbnail generates (or retrieves from disk cache) a thumbnail for a
// video entry (local file or network-backed CustomReader). It extracts the
// frame at info.previewIndex (changed by swiping the tile), scales it to
// 2×tileWidth wide, and overlays a circular play icon in the top-left corner.
// On any failure it falls back to the loading placeholder.
func (layout *TileLayout) videoThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	tileW := int(layout.config.General.TileWidth)

	frameIdx := info.previewIndex
	if frameIdx < 0 || frameIdx >= videoPreviewFrames {
		frameIdx = 0
	}

	// Check disk cache before running mpv.
	cachePath := layout.videoThumbnailCachePath(info.Path, frameIdx)
	if data, err := os.ReadFile(cachePath); err == nil && !layout.refreshThumbs {
		info.ThumbnailIsScaled = true
		return bytes.NewReader(data), nil
	}

	// Extract a frame using libmpv's software renderer. Frame k seeks to
	// (k+1)/(frames+1) of the duration, skipping the very start and end
	// (often black frames). For network-backed entries that expose a stream
	// URL, pass the URL directly so libmpv streams without downloading.
	percent := float64(frameIdx+1) / float64(videoPreviewFrames+1) * 100
	var frame image.Image
	tileW2 := tileW * 2
	if info.InputIsReader {
		if vs, ok := info.CustomReader.(VideoStreamer); ok && vs.StreamURL() != "" {
			frame = mpvplayer.ExtractFramePercent(vs.StreamURL(), tileW2, tileW2, percent)
		} else if r, err := info.GetReader(); err == nil {
			frame = mpvplayer.ExtractFrameFromReaderPercent(r, tileW2, tileW2, percent)
		}
	} else {
		frame = mpvplayer.ExtractFramePercent(info.Path, tileW2, tileW2, percent)
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

	info.ThumbnailIsScaled = true
	return bytes.NewReader(buf.Bytes()), nil
}

// videoThumbnailCachePath returns the path under ThumbnailDir where the video
// frame thumbnail is cached, using a SHA-256 hash of the video file path as
// the key (avoiding reading the full video file just for hashing). Frame 0
// keeps the historic suffix-free name so existing caches stay valid; other
// frames get a "-N" suffix.
func (layout *TileLayout) videoThumbnailCachePath(videoPath string, frame int) string {
	sum := sha256.Sum256([]byte(videoPath))
	hash := "v" + hex.EncodeToString(sum[:])
	if frame > 0 {
		hash += "-" + strconv.Itoa(frame)
	}
	dir := layout.config.General.ThumbnailDir
	for i := 0; i < 3; i++ {
		// skip the "v" prefix when indexing into the hash for dir levels
		dir = filepath.Join(dir, hash[i*2+1:i*2+3])
	}
	return filepath.Join(dir, hash)
}

// blendNRGBA alpha-blends c onto the pixel at (x, y) if inside dst's bounds.
func blendNRGBA(dst *image.NRGBA, x, y int, c color.NRGBA) {
	b := dst.Bounds()
	if x >= b.Min.X && y >= b.Min.Y && x < b.Max.X && y < b.Max.Y {
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

// drawVideoPlayIcon overlays a semi-transparent circular play button at
// (x0, y0) with the given pixel size onto dst.
func drawVideoPlayIcon(dst *image.NRGBA, x0, y0, size int) {
	radius := size / 2
	cx := x0 + radius
	cy := y0 + radius
	bg := color.NRGBA{0, 0, 0, 160}
	fg := color.NRGBA{255, 255, 255, 230}

	setPixel := func(x, y int, c color.NRGBA) {
		blendNRGBA(dst, x, y, c)
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

// drawFolderIcon overlays a semi-transparent folder badge at (x0, y0) with
// the given pixel size onto dst. It marks directory and archive tiles the
// same way drawVideoPlayIcon marks video tiles.
func drawFolderIcon(dst *image.NRGBA, x0, y0, size int) {
	bg := color.NRGBA{0, 0, 0, 160}
	fg := color.NRGBA{255, 255, 255, 230}

	// Semi-transparent rounded-square background: inside each corner square,
	// keep only pixels within the corner circle.
	corner := size / 6
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cx, cy := -1, -1
			switch {
			case x < corner && y < corner:
				cx, cy = corner, corner
			case x >= size-corner && y < corner:
				cx, cy = size-corner-1, corner
			case x < corner && y >= size-corner:
				cx, cy = corner, size-corner-1
			case x >= size-corner && y >= size-corner:
				cx, cy = size-corner-1, size-corner-1
			}
			if cx >= 0 {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy > corner*corner {
					continue
				}
			}
			blendNRGBA(dst, x0+x, y0+y, bg)
		}
	}

	// Folder silhouette: back panel with a tab, then a slightly offset front
	// flap cut out of it (simple two-rect glyph, readable at small sizes).
	pad := size / 4
	fx0, fx1 := x0+pad, x0+size-pad
	tabH := size / 9
	bodyY0 := y0 + size/3
	bodyY1 := y0 + size - pad
	// Tab (left half of the folder top).
	for y := bodyY0 - tabH; y < bodyY0; y++ {
		for x := fx0; x <= fx0+(fx1-fx0)/2; x++ {
			blendNRGBA(dst, x, y, fg)
		}
	}
	// Body.
	for y := bodyY0; y <= bodyY1; y++ {
		for x := fx0; x <= fx1; x++ {
			blendNRGBA(dst, x, y, fg)
		}
	}
}

const (
	lvlDeep  = 3
	dirWidth = 2
	max      = lvlDeep * dirWidth
)

func (layout *TileLayout) NewImageTile(reader io.ReadSeeker, info *ImageInfo, tabFn func(t *Tile)) (*Tile, error) {
	decoded, _, err := Decode(reader)
	if err != nil || decoded == nil {
		// Decode failed; use loading placeholder
		na := bytes.NewReader(loading)
		decoded2, _, _ := Decode(na)
		decoded = decoded2
	}
	return layout.newImageTileFromImage(toRGBA(decoded), info, tabFn), nil
}

// toRGBA converts img to *image.RGBA. The GL painter uploads *image.RGBA
// pixel data directly, but converts any other image type (YCbCr from JPEG
// decode, NRGBA from imaging.Resize) with a full-pixel draw.Draw on the
// paint thread at texture-upload time — calling this on a loader goroutine
// keeps that cost off the UI thread and parallelises it across workers.
func toRGBA(img image.Image) *image.RGBA {
	if img == nil {
		return nil
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	rgba := image.NewRGBA(img.Bounds())
	stdraw.Draw(rgba, img.Bounds(), img, img.Bounds().Min, stdraw.Src)
	return rgba
}

// newImageTileFromImage builds a tile around an already-decoded image.
// PlaceTiles uses it to share one decoded placeholder across all tiles on a
// page instead of decoding loading.png once per tile. The image is not
// mutated afterwards, so sharing it between canvas.Image objects is safe.
func (layout *TileLayout) newImageTileFromImage(decoded image.Image, info *ImageInfo, tabFn func(t *Tile)) *Tile {
	t := &Tile{
		Viewer: layout.viewer,
		layout: layout,
	}
	img := canvas.NewImageFromImage(decoded) // do not resize if picture is smaller than tile

	img.ScaleMode = canvas.ImageScaleFastest
	img.FillMode = canvas.ImageFillContain
	t.Info = info
	if info.Width > 0 && info.Height > 0 {
		// Use pre-stored original dimensions so placeholder tiles already
		// carry the correct aspect ratio, avoiding layout reflow on load.
		t.width = float32(info.Width)
		t.height = float32(info.Height)
	} else {
		t.width = float32(img.Image.Bounds().Max.X)
		t.height = float32(img.Image.Bounds().Max.Y)
	}
	t.landscape = t.width > t.height
	t.Content = img
	t.tabFn = tabFn

	// Create name label using the entry's display name. When the entry has a
	// subtitle, the name renders bold with the subtitle below it in normal
	// weight (e.g. album title over artist).
	if info.DisplayName != "" {
		lbl := widget.NewLabel(info.DisplayName)
		lbl.Alignment = fyne.TextAlignCenter
		lbl.Truncation = fyne.TextTruncateEllipsis
		if info.Subtitle != "" {
			lbl.TextStyle = fyne.TextStyle{Bold: true}
		}
		if !layout.showLabels {
			lbl.Hide()
		}
		t.nameLabel = lbl
	}
	if info.Subtitle != "" {
		sub := widget.NewLabel(info.Subtitle)
		sub.Alignment = fyne.TextAlignCenter
		sub.Truncation = fyne.TextTruncateEllipsis
		if !layout.showLabels {
			sub.Hide()
		}
		t.subLabel = sub
	}

	t.ExtendBaseWidget(t)

	return t
}

func (t *Tile) Tapped(_ *fyne.PointEvent) {
	t.tabFn(t)
}

func (t *Tile) TappedSecondary(_ *fyne.PointEvent) {
	if t.Viewer != nil && t.Viewer.OnTileSecondaryTapped != nil {
		t.Viewer.OnTileSecondaryTapped(t)
	}
}

// cyclePreview advances the preview shown on a directory/archive/video tile
// by delta (+1 = next, -1 = previous, wrapping around): the image within a
// folder/archive, or the frame within a video. The new thumbnail is
// generated off-thread and swapped into the existing tile in place, so only
// this tile's texture is re-uploaded.
func (t *Tile) cyclePreview(delta int) {
	info := t.Info
	n := info.PreviewCount()
	if n == 0 || t.layout == nil {
		return
	}
	info.previewIndex = ((info.previewIndex+delta)%n + n) % n
	go func() {
		thumb, err := t.layout.GetThumbnail(info)
		if err != nil {
			return
		}
		decoded, _, err := Decode(thumb)
		if err != nil || decoded == nil {
			return
		}
		rgba := toRGBA(decoded)
		fyne.Do(func() {
			if t.Info != info {
				return // tile was replaced while loading
			}
			t.Content.Image = rgba
			b := rgba.Bounds()
			t.width, t.height = float32(b.Dx()), float32(b.Dy())
			t.landscape = t.width > t.height
			t.Content.Refresh() // invalidate only this tile's texture
			t.layout.relayoutGrid()
		})
	}()
}

// swipeThreshold is the horizontal drag distance in pixels that triggers a
// preview change on directory/archive tiles.
const swipeThreshold = 40

// dirSwipeOverlay is a transparent widget covering the image area of tiles
// with cyclable previews (directories, archives, videos). It forwards taps to
// the tile (so the entry still opens) and turns horizontal drags into preview
// cycling (next/previous image inside the folder/archive, or next/previous
// frame of a video). It only exists on tiles whose ImageInfo.HasPreviews is
// true, so regular image tiles never intercept drags and the scroll container
// keeps its native drag/fling behavior there. Vertical drags are forwarded to
// the gallery's scroller so page scrolling still works when the gesture
// starts on an overlaid tile (only fling momentum is lost in that case).
type dirSwipeOverlay struct {
	widget.BaseWidget
	tile   *Tile
	bg     *canvas.Rectangle
	accumX float32
	accumY float32
}

func newDirSwipeOverlay(t *Tile) *dirSwipeOverlay {
	o := &dirSwipeOverlay{tile: t, bg: canvas.NewRectangle(color.Transparent)}
	o.ExtendBaseWidget(o)
	return o
}

func (o *dirSwipeOverlay) Tapped(ev *fyne.PointEvent)          { o.tile.Tapped(ev) }
func (o *dirSwipeOverlay) TappedSecondary(ev *fyne.PointEvent) { o.tile.TappedSecondary(ev) }

func (o *dirSwipeOverlay) Dragged(ev *fyne.DragEvent) {
	o.accumX += ev.Dragged.DX
	o.accumY += ev.Dragged.DY
	absX, absY := o.accumX, o.accumY
	if absX < 0 {
		absX = -absX
	}
	if absY < 0 {
		absY = -absY
	}
	if absX > absY && absX >= swipeThreshold {
		// Swipe left (finger moves left) = next image, right = previous.
		delta := 1
		if o.accumX > 0 {
			delta = -1
		}
		o.accumX, o.accumY = 0, 0
		o.tile.cyclePreview(delta)
		return
	}
	// Vertical-dominant (or still sub-threshold) drag: keep the page
	// scrolling by forwarding the delta to the gallery's scroller.
	if v := o.tile.Viewer; v != nil && v.scroll != nil && ev.Dragged.DY != 0 {
		v.scroll.ScrollToOffset(fyne.NewPos(v.scroll.Offset.X, v.scroll.Offset.Y-ev.Dragged.DY))
	}
}

func (o *dirSwipeOverlay) DragEnd() { o.accumX, o.accumY = 0, 0 }

func (o *dirSwipeOverlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(o.bg)
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
	// Gallery navigation hotkeys live in layout.hotkeys because KeyPress
	// dispatches those unconditionally, while Gallery.hotkeys are dispatched
	// at window level only on mobile — on desktop they reach the focused
	// ImageView instead. No widget is focused in the gallery grid, so
	// registering these on Gallery.hotkeys left them dead on desktop.
	scrollBy := func(dy float32) {
		if layout.viewer == nil || layout.viewer.scroll == nil {
			return
		}
		s := layout.viewer.scroll
		s.ScrollToOffset(fyne.NewPos(s.Offset.X, s.Offset.Y+dy))
	}
	for _, x := range bindings.ScrollDown {
		add(Hotkey{x, func() { scrollBy(300) }})
	}
	for _, x := range bindings.ScrollUp {
		add(Hotkey{x, func() { scrollBy(-300) }})
	}
	for _, x := range bindings.PathLevelUp {
		add(Hotkey{x, func() {
			if layout.viewer != nil {
				layout.viewer.ShowImageDir(filepath.Dir(layout.viewer.currentPath))
			}
		}})
	}
	// Skip ToggleFilenames on mobile (no keyboard to press N)
	if !layout.viewer.Platform().IsMobile() {
		for _, x := range bindings.ToggleFilenames {
			add(Hotkey{x, func() {
				layout.ToggleLabels()
			}})
		}
	}
}

// TileRenderer renders the tile image with an optional name label below it.
type TileRenderer struct {
	tile *Tile
	// objects is precomputed once at renderer creation: the painter calls
	// Objects() for every visible widget on every repaint and on every
	// mouse hit-test walk, so allocating a fresh slice per call cost ~500
	// allocations per repaint on a full page. Hidden labels are skipped by
	// the painter's visibility check, so the slice contents never change.
	objects []fyne.CanvasObject
}

func (ta *Tile) CreateRenderer() fyne.WidgetRenderer {
	r := &TileRenderer{tile: ta}
	r.objects = []fyne.CanvasObject{ta.Content}
	// Tiles with cyclable previews (directories, archives, videos) get a
	// transparent swipe overlay above the image. Later objects win
	// hit-testing, so the overlay (not the tile) receives taps/drags and
	// forwards them appropriately.
	if ta.swipeOverlay == nil && ta.Info != nil && ta.Info.HasPreviews() {
		ta.swipeOverlay = newDirSwipeOverlay(ta)
	}
	if ta.swipeOverlay != nil {
		r.objects = append(r.objects, ta.swipeOverlay)
	}
	if ta.nameLabel != nil {
		r.objects = append(r.objects, ta.nameLabel)
	}
	if ta.subLabel != nil {
		r.objects = append(r.objects, ta.subLabel)
	}
	return r
}

func (r *TileRenderer) Layout(size fyne.Size) {
	imgH := size.Height
	if r.tile.nameLabel != nil && r.tile.nameLabel.Visible() {
		lines := float32(1)
		if r.tile.subLabel != nil {
			lines = 2
		}
		imgH = size.Height - lines*labelHeight
		r.tile.nameLabel.Resize(fyne.NewSize(size.Width, labelHeight))
		r.tile.nameLabel.Move(fyne.NewPos(0, imgH))
		if r.tile.subLabel != nil {
			r.tile.subLabel.Resize(fyne.NewSize(size.Width, labelHeight))
			r.tile.subLabel.Move(fyne.NewPos(0, imgH+labelHeight))
		}
	}
	r.tile.Content.Resize(fyne.NewSize(size.Width, imgH))
	r.tile.Content.Move(fyne.NewPos(0, 0))
	if r.tile.swipeOverlay != nil {
		r.tile.swipeOverlay.Resize(fyne.NewSize(size.Width, imgH))
		r.tile.swipeOverlay.Move(fyne.NewPos(0, 0))
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
	if r.tile.subLabel != nil {
		r.tile.subLabel.Refresh()
	}
}

func (r *TileRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *TileRenderer) Destroy() {}
