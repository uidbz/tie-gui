package gallery

import (
	"bytes"
	"image/color"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// TestImageInfoConcurrentAccess hammers the mutable part of ImageInfo from the
// goroutines that really touch it in production: the UI goroutine swiping a
// directory tile, a loader worker resolving previews and recording dimensions,
// and a placement resetting the retry budget while a worker spends it.
//
// Before the split these were plain field accesses shared across goroutines —
// previewIndex, PreviewReaders, Width/Height, ThumbnailIsScaled and
// loadAttempts — and the race detector reports them the moment a swipe lands
// during a load. Run this under -race.
func TestImageInfoConcurrentAccess(t *testing.T) {
	raw := testJPEGBytes(t, 40, 30, color.RGBA{10, 10, 10, 255})
	info := NewImageInfoCustomReader(0, memReader{path: "dir", data: raw})
	info.SetPreviewReaders([]CustomReader{
		memReader{path: "dir/a.jpg", data: raw},
		memReader{path: "dir/b.jpg", data: raw},
		memReader{path: "dir/c.jpg", data: raw},
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { // the prefetch loop runs until the others are done
		defer workers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _, _ = info.decodedImage()
			}
		}
	}()

	wg.Add(3)
	go func() { // the UI goroutine: swipe, read the count
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			info.AdvancePreview(1, 3)
			_ = info.PreviewCount()
			_ = info.HasPreviews()
		}
	}()
	go func() { // a loader worker: resolve previews, record what it fetched
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			info.SetPreviewReaders([]CustomReader{memReader{path: "dir/x.jpg", data: raw}})
			info.SetDimensions(640+i, 480+i)
			info.SetThumbnailScaled(i%2 == 0)
			_ = info.PreviewIndex()
		}
	}()
	go func() { // placement + worker retry accounting
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			info.resetLoadAttempts()
			info.bumpLoadAttempts()
			_ = info.loadAttempts()
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the concurrent access test stalled")
	}
	close(stop)
	workers.Wait()

	if w, h := info.Dimensions(); w == 0 || h == 0 {
		t.Errorf("Dimensions = (%d,%d) after concurrent writes", w, h)
	}
	if info.PreviewCount() == 0 {
		t.Error("PreviewCount read back nothing after concurrent writes")
	}
}

// TestAdvancePreviewWraps pins the arithmetic that used to be spelled out at
// the call site: forward and backward cycling wrap inside [0,n), and n<=0 is a
// no-op rather than a division by zero.
func TestAdvancePreviewWraps(t *testing.T) {
	info := NewImageInfo(0, "x")
	if got := info.AdvancePreview(1, 0); got != 0 {
		t.Errorf("AdvancePreview(1, 0) = %d, want 0 (no-op)", got)
	}
	info.SetPreviewIndex(2)
	if got := info.AdvancePreview(1, 3); got != 0 {
		t.Errorf("wrapped forward to %d, want 0", got)
	}
	if got := info.AdvancePreview(-1, 3); got != 2 {
		t.Errorf("wrapped back to %d, want 2", got)
	}
}

// gateThumbnailer blocks every GetThumbnail until release is closed, so a test
// can be sure a swipe lands while a load is genuinely in flight.
type gateThumbnailer struct {
	jpeg    []byte
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gateThumbnailer) GetThumbnail(*ImageInfo) (io.ReadSeeker, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return bytes.NewReader(g.jpeg), nil
}

// TestCyclePreviewDuringLoad is the targeted case from review §4.1: swipe a
// directory tile while its thumbnail is still being resolved. cyclePreview
// writes the preview index on the caller's goroutine and then spawns a loader
// that reads it and installs the preview readers, so the two overlapped on
// plain fields with nothing between them.
func TestCyclePreviewDuringLoad(t *testing.T) {
	raw := testJPEGBytes(t, 100, 75, color.RGBA{20, 90, 20, 255})

	test.NewApp()
	win := test.NewWindow(nil)
	config := Config{}
	config.General.TileWidth = 200
	config.General.ImagesPerPage = 4
	config.General.Workers = 2
	config.General.ThumbnailDir = filepath.Join(t.TempDir(), "cache")

	hold := &gateThumbnailer{jpeg: raw, entered: make(chan struct{}), release: make(chan struct{})}
	viewer := NewGallery(fyne.CurrentApp(), win, config, nil)
	viewer.Thumbnailer = hold
	viewer.Init()
	viewer.ReadCustom([]CustomReader{
		memPreviewProvider{
			memReader: memReader{path: "tie:dir-hash", data: raw},
			previews: []CustomReader{
				memReader{path: "tie:dir-hash/a", data: raw},
				memReader{path: "tie:dir-hash/b", data: raw},
				memReader{path: "tie:dir-hash/c", data: raw},
			},
		},
	})
	win.SetContent(viewer.Content)
	win.Resize(fyne.NewSize(800, 600))
	viewer.LoadGallery()

	// Wait for a load to start, then swipe the tile while it is blocked.
	<-hold.entered
	// The page install is queued ahead of the load that is now blocked, so a
	// round-trip through the tile updater applies it without waiting for the
	// blocked load itself (AwaitIdle would deadlock here).
	viewer.layout.uiSync()
	if len(viewer.layout.tiles) == 0 {
		close(hold.release)
		t.Fatal("no tiles to swipe")
	}
	tile := viewer.layout.tiles[0]
	for i := 0; i < 3; i++ {
		tile.cyclePreview(1)
	}
	close(hold.release)
	viewer.AwaitIdle()

	count := tile.Info.PreviewCount()
	if count == 0 {
		t.Fatal("the entry advertises no previews to cycle")
	}
	if got, want := tile.Info.PreviewIndex(), 3%count; got != want {
		t.Errorf("previewIndex = %d after three forward swipes of %d previews, want %d",
			got, count, want)
	}
}
