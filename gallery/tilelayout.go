package gallery

import (
	"bytes"
	"image"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	_ "embed"

	"fyne.io/fyne/v2"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

//go:embed loading.png
var loading []byte

type Thumbnailer interface {
	GetThumbnail(info *ImageInfo) (io.ReadSeeker, error)
}

type TileLayout struct {
	tiles        []*Tile
	wg           sync.WaitGroup
	minHeight    float32
	imagesToLoad chan *ImageInfo
	// results is the grid's work queue; the tileUpdater is its only consumer,
	// which makes that goroutine the single owner of every grid mutation the
	// gallery's background goroutines want to make.
	results          chan uiJob
	tabFn            func(t *Tile)
	grid             *fyne.Container
	hotkeys          []Hotkey
	config           Config
	window           fyne.Window
	app              fyne.App
	viewer           *Gallery     // TODO Phase 3: remove this back-reference
	offset           atomic.Int64 // page start index into the listing
	currentlyLoading inflight
	// uiCtl round-trips a message through the tileUpdater goroutine; see
	// uiSync.
	uiCtl chan chan struct{}
	// placement tracks a running PlaceTiles call: Add(1) before spawning,
	// Done when it returns. Tests Wait on it to observe page placement
	// (with the test driver, PlaceTiles' fyne.Do blocks run inline before
	// it returns, so Wait covers the whole placement).
	placement  sync.WaitGroup
	thumbCache *thumbnailCache
	showLabels bool
	// syncUI runs posted grid mutations on the caller's goroutine instead of
	// handing them to the tileUpdater. Only set in synchronous-placement mode;
	// see Gallery.SetSyncPlacement.
	syncUI bool
	// log reports what the grid cannot show on screen: a thumbnail that would
	// not load, a panic on a loader goroutine. Copied from the Gallery.
	log *slog.Logger
	// placeMu serializes overlapping placements (rapid page changes,
	// re-sorts): the last placement must win deterministically — its tile
	// install is queued last and its results land on its own tiles. It is held
	// across the whole placement — drain, wait for the previous page's loads,
	// build and install — so two placements never drain the same queue or
	// wait on the same counter at the same time.
	placeMu sync.Mutex
	// Direct access to avoid back-reference through viewer
	thumbnailer   Thumbnailer
	refreshThumbs bool
	// Pagination button (shown at end of gallery when more pages exist)
	nextPageButton *widget.Button
	// pendingReveal, when >= 0, is the page-relative tile index PlaceTiles
	// scrolls into view after placing this page (set by showGallery when
	// returning from the single-image view to a different page). Consumed
	// once by the page-placement completion block.
	pendingReveal int
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

func NewTileLayout(config Config, window fyne.Window, app fyne.App, viewer *Gallery, tabFn func(t *Tile)) *TileLayout {
	batchSize := 1024
	tiles := make([]*Tile, 0)
	imagesToLoad := make(chan *ImageInfo, batchSize)

	// Cache budget in bytes of decoded thumbnails rather than a tile count: a
	// tile's pixel data is (2*TileWidth) x (2*TileWidth*aspect) bytes, which
	// varies with a user-settable config value, so counting tiles made the
	// limit meaningless. Sized for roughly three pages of thumbnails in each
	// direction so page-back navigation is served from memory.
	maxCacheBytes := int64(150 << 20)
	if viewer.Platform().IsMobile() {
		maxCacheBytes = 40 << 20
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
		results:       make(chan uiJob, batchSize),
		uiCtl:         make(chan chan struct{}),
		tabFn:         tabFn,
		config:        config,
		window:        window,
		app:           app,
		viewer:        viewer,
		thumbCache:    newThumbnailCache(maxCacheBytes),
		syncUI:        viewer.syncPlace,
		log:           viewer.log(),
		showLabels:    showLabels,
		thumbnailer:   viewer.Thumbnailer,
		refreshThumbs: viewer.refreshThumbs,
		pendingReveal: -1,
	}

	for i := 0; i < config.General.Workers; i++ {
		go layout.imageLoader()
	}
	go layout.tileUpdater()

	return layout
}

// placeTiles places the page starting at offset. Background callers pass an
// offset snapshotted on the UI goroutine: layout.offset is written there
// (ChangeGallery, ChangePage), so reading it here would race a newer
// placement.
//
// The caller must hold placeMu: the drain, the wait for the previous page's
// loads and this build are one critical section, and two overlapping
// placements would otherwise drain the same queue and re-arm the same counter.
// getOffset and setOffset read and write the page start index. ChangePage and
// ChangeGallery write it on the UI goroutine while the tileUpdater reads it to
// map a finished thumbnail back to its slot, so it is atomic rather than a
// plain field.
func (layout *TileLayout) getOffset() int { return int(layout.offset.Load()) }

func (layout *TileLayout) setOffset(o int) { layout.offset.Store(int64(o)) }

func (layout *TileLayout) placeTiles(imageFiles []*ImageInfo, offset int) {
	// Decode the placeholder image once and share it across all placeholder
	// tiles on this page (previously each tile decoded loading.png again
	// because the shared reader was consumed after the first decode).
	placeholder, _, err := Decode(bytes.NewReader(loading))
	if err != nil || placeholder == nil {
		placeholder = image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}

	end := offset + layout.config.General.ImagesPerPage
	if end > len(imageFiles) {
		end = len(imageFiles)
	}
	// Start each page with a fresh tile list, indexed relative to
	// layout.offset (same indexing imageLoader and Layout use). Build it
	// locally on this background goroutine; layout.tiles is assigned on the
	// UI thread together with the grid objects.
	newTiles := make([]*Tile, 0, end-offset)
	for i := offset; i < end; i++ {
		tile := layout.newImageTileFromImage(placeholder, imageFiles[i], func(t *Tile) {})
		newTiles = append(newTiles, tile)
	}

	// Install the new page's tiles BEFORE enqueueing the load work. A
	// worker result can only exist after an item has been enqueued, so the
	// tileUpdater's flush fyne.Do is always queued after this install and
	// its Info guard matches the new placeholders. The old order (enqueue
	// first, install after) let fast cache-hit flushes run against the
	// previous page's tile list and drop the new page's results as
	// "stale", leaving permanent placeholder tiles.
	layout.postUI(func() {
		layout.tiles = newTiles
		layout.grid.Objects = make([]fyne.CanvasObject, 0, len(newTiles))
		for _, tile := range newTiles {
			layout.grid.Objects = append(layout.grid.Objects, tile)
		}
	})

	// Enqueue the load work; each item gets a fresh retry budget.
	for i := offset; i < end; i++ {
		imageFiles[i].resetLoadAttempts()
		layout.currentlyLoading.Add(1)
		layout.imagesToLoad <- imageFiles[i]
	}

	// Add "Next Page" button at the end if there are more pages
	// Calculate if we're on the last page
	imagesPerPage := layout.config.General.ImagesPerPage
	totalImages := len(imageFiles)
	currentPage := offset / imagesPerPage
	maxPages := totalImages / imagesPerPage
	if totalImages%imagesPerPage != 0 {
		maxPages++
	}

	if currentPage < maxPages-1 {
		layout.postUI(func() {
			// Widget creation and mutation belong on the UI goroutine: the
			// enclosing PlaceTiles runs on a background goroutine.
			if layout.nextPageButton == nil {
				layout.nextPageButton = widget.NewButton("Load Next Page ▼", func() {
					if layout.viewer != nil {
						layout.viewer.ChangePage(currentPage + 1)
					}
				})
				layout.nextPageButton.Importance = widget.HighImportance
			} else {
				// Update the callback in case the page number changed
				layout.nextPageButton.OnTapped = func() {
					if layout.viewer != nil {
						layout.viewer.ChangePage(currentPage + 1)
					}
				}
			}
			layout.grid.Add(layout.nextPageButton)
		})
	}

	// Trigger an initial layout pass to ensure tiles are visible
	// This is critical for the first page load when scroll offset is 0.
	// relayoutGrid positions tiles without invalidating any textures
	// (unlike grid.Refresh, which recursively refreshes all children).
	layout.postUI(func() {
		layout.relayoutGrid()
		// A pending reveal (returning from the single-image view to a
		// freshly placed page) scrolls the target tile into view now that
		// this page's tiles are positioned. Consumed exactly once.
		if layout.pendingReveal >= 0 {
			idx := layout.pendingReveal
			layout.pendingReveal = -1
			if idx < len(layout.tiles) {
				tile := layout.tiles[idx]
				layout.scrollToOffset(tile.Position().Y, tile.Size().Height)
			}
		}
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

// scrollToOffset scrolls the gallery so the tile row at (y, h) sits in the
// middle of the viewport, clamped to the scrollable range.
func (layout *TileLayout) scrollToOffset(y, h float32) {
	v := layout.viewer
	if v == nil || v.scroll == nil {
		return
	}
	viewport := v.scroll.Size().Height
	target := y - (viewport-h)/2
	maxOffset := layout.minHeight - viewport
	if maxOffset < 0 {
		maxOffset = 0
	}
	if target < 0 {
		target = 0
	}
	if target > maxOffset {
		target = maxOffset
	}
	v.scroll.ScrollToOffset(fyne.NewPos(0, target))
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
	layout.postUI(func() {
		layout.relayoutGrid()
	})
}
