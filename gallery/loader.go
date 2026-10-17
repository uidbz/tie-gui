// The loader pipeline: the worker queue, the per-item retry budget, the
// tileUpdater that owns every grid mutation, and the two counters tests and
// placement wait on.
package gallery

import (
	"log/slog"
	"sync"
	"time"

	_ "embed"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie-gui/tielog"
)

// Thumbnailer supplies scaled thumbnails for reader-backed gallery items
// (CustomReader != nil). Items without a CustomReader (plain local files)
// are always thumbnailed from the image content and cached in the directory
// given by GeneralConfig.ThumbnailDir, regardless of the Thumbnailer.
// inflight counts outstanding background jobs and lets a caller wait for them.
// It replaces sync.WaitGroup for the thumbnail loads because a WaitGroup
// forbids Add concurrent with a Wait at a zero counter, and the gallery hits
// exactly that: a worker re-queues a failed thumbnail (Add) while a page
// placement waits for the previous page's loads to drain, and two placements
// can queue behind each other. The counter plus a broadcast channel has no
// such rule.
type inflight struct {
	mu     sync.Mutex
	count  int
	idleCh []chan struct{}
}

func (w *inflight) Add(n int) {
	w.mu.Lock()
	w.count += n
	w.mu.Unlock()
}

func (w *inflight) Done() {
	w.mu.Lock()
	w.count--
	if w.count <= 0 {
		for _, ch := range w.idleCh {
			close(ch)
		}
		w.idleCh = nil
	}
	w.mu.Unlock()
}

// Wait blocks until the count reaches zero.
func (w *inflight) Wait() {
	w.mu.Lock()
	if w.count <= 0 {
		w.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	w.idleCh = append(w.idleCh, ch)
	w.mu.Unlock()
	<-ch
}

// loadedTile carries a finished thumbnail tile from a loader worker to the
// tileUpdater goroutine, which batches UI-thread write-backs.
type loadedTile struct {
	info *ImageInfo
	tile *Tile
}

// uiJob is one unit of work for the tileUpdater. Keeping whole-page installs
// and incremental thumbnail write-backs in one channel is what makes them land
// in production order: a page's placeholder tiles must be installed before
// that page's finished thumbnails are written back, or the write-back's Info
// guard compares against the previous page's tiles, drops the result as stale,
// and the tile stays a placeholder forever.
type uiJob struct {
	// install replaces the grid's contents wholesale (a new page).
	install func()
	// tiles are thumbnails finished for the current page.
	tiles []loadedTile
}

// maxTileLoadAttempts bounds how many times a failing thumbnail load is
// re-queued before the worker gives up and leaves the placeholder tile
// (transient network/decoder failures should not strand a placeholder,
// but a permanently failing entry must not spin forever).
const maxTileLoadAttempts = 3

// drainPending discards the work still queued for a page that has been
// replaced. Each queued item was Add()ed to currentlyLoading when it was
// enqueued, so it must be Done() here — the workers will never see it.
//
// This is deliberately non-blocking: it only takes what is already sitting in
// the buffer. Waiting for the loads already in flight is the caller's next
// step, and it belongs on a background goroutine.
func (layout *TileLayout) drainPending() {
	for {
		select {
		case <-layout.imagesToLoad:
			layout.currentlyLoading.Done()
			continue
		default:
			return
		}
	}
}

// imageLoader is a worker goroutine that drains imagesToLoad, builds (or
// fetches from cache) the real thumbnail tile, and forwards it to the
// tileUpdater for batched UI write-back. currentlyLoading.Add is called by
// PlaceTiles (and by the retry re-enqueue below) before enqueueing; each
// enqueued attempt is Done here exactly once (or by the page-drain loops in
// ChangePage/ChangeGallery if discarded).
func (layout *TileLayout) imageLoader() {
	for tc := range layout.imagesToLoad {
		layout.loadOne(tc)
	}
}

// loadOne loads one queued entry. It is a separate call rather than the loop
// body so that a failure of any kind is contained to this entry: the worker
// keeps taking from the queue, and the deferred Done always runs, so a page
// placement waiting on currentlyLoading is never left hanging. A thumbnailer
// that panics on one blob (a truncated cgo decode, an assertion in a
// third-party reader) used to take the process down with it.
func (layout *TileLayout) loadOne(tc *ImageInfo) {
	defer layout.currentlyLoading.Done()
	defer tielog.Recover(layout.logger(), "gallery.imageLoader")

	bitmap, ok := layout.bitmapFromCache(tc.Path)
	if !ok {
		thumb, err := layout.GetThumbnail(tc)
		if err == nil {
			bitmap, err = decodeThumbnailRGBA(thumb)
		}
		if err != nil {
			// Thumbnail failed (network hiccup, truncated blob, ...):
			// re-queue with a bounded budget so a transient failure
			// does not leave a permanent placeholder. Once the budget
			// is spent the placeholder stays (previous behavior).
			if tc.loadAttempts() < maxTileLoadAttempts {
				tc.bumpLoadAttempts()
				layout.logger().Debug("gallery: thumbnail load, retrying",
					"path", tc.Path, "attempt", tc.loadAttempts(), "err", err)
				layout.currentlyLoading.Add(1)
				layout.imagesToLoad <- tc
			} else {
				layout.logger().Warn("gallery: thumbnail load failed",
					"path", tc.Path, "attempts", 1+maxTileLoadAttempts, "err", err)
			}
			return
		}
		layout.bitmapToCache(tc.Path, bitmap)
	}
	// Every slot gets its own Tile around the shared bitmap: two entries
	// with the same content hash must be two independent widgets.
	tile := layout.newImageTileFromImage(bitmap, tc, layout.tabFn)
	layout.results <- uiJob{tiles: []loadedTile{{info: tc, tile: tile}}}
}

// tileUpdater batches loaded tiles and applies them to the grid in a single
// UI-thread task per flush (every flushInterval trailing, or immediately at
// flushBatchSize). This replaces the previous scheme (grid.Refresh every 20
// thumbnails plus a 500 ms timer per worker), which recursively refreshed
// every child widget and forced the painter to delete and re-upload ALL
// visible tile textures on the next frame — the main source of scroll jank
// while a page was loading.
func (layout *TileLayout) tileUpdater() {
	defer tielog.Recover(layout.logger(), "gallery.tileUpdater")
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
				idx := r.info.order - int(layout.offset.Load())
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
		case job := <-layout.results:
			if job.install != nil {
				// A page replacement: apply the thumbnails already in hand
				// first, then install, so the two never interleave.
				flush()
				stopTimer()
				fyne.Do(job.install)
				continue
			}
			pending = append(pending, job.tiles...)
			stopTimer()
			if len(pending) >= flushBatchSize {
				flush()
			} else {
				timer.Reset(flushInterval)
			}
		case <-timer.C:
			flush()
		case ack := <-layout.uiCtl:
			// Drain everything the workers have already handed over before
			// answering, so the caller cannot be told "settled" while a
			// result is still sitting in the channel.
		drain:
			for {
				select {
				case job := <-layout.results:
					if job.install != nil {
						fyne.Do(job.install)
					} else {
						pending = append(pending, job.tiles...)
					}
				default:
					break drain
				}
			}
			flush()
			close(ack)
		}
	}
}

// uiSync blocks until every write-back queued before the call has been applied
// to the grid.
//
// The workers finishing does not imply their results have landed: the updater
// debounces them, and a caller that then touches the widget tree has no
// happens-before edge with the goroutine that installs them. Under -race that
// is a data race even when the two happen seconds apart in wall-clock time,
// which is why waiting for the workers and then sleeping was never enough.
func (layout *TileLayout) uiSync() {
	ack := make(chan struct{})
	select {
	case layout.uiCtl <- ack:
		<-ack
	case <-time.After(10 * time.Second):
	}
}

// logger returns the layout's logger, so a TileLayout built without a Gallery
// still works.
func (layout *TileLayout) logger() *slog.Logger {
	if layout.log == nil {
		return tielog.Discard()
	}
	return layout.log
}

// postUI hands a grid mutation to the tileUpdater.
//
// Every background goroutine in the gallery used to call fyne.Do itself. In
// production that is fine — the app driver runs the tasks on one goroutine, in
// the order they arrive — but nothing inside the gallery then orders a page
// install against a thumbnail write-back, and under the software test driver
// (which runs fyne.Do inline on the calling goroutine) the two mutate the
// widget tree concurrently. Routing them through one consumer gets that order
// inside the gallery itself instead of relying on an assumption about the
// driver.
func (layout *TileLayout) postUI(fn func()) {
	if layout.syncUI {
		fn()
		return
	}
	select {
	case layout.results <- uiJob{install: fn}:
	case <-time.After(10 * time.Second):
	}
}
