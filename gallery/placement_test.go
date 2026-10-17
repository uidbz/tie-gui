package gallery

import (
	"image/color"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

// TestLoadGalleryDoesNotBlockOnFetch is the regression for review §3.3.
// LoadGallery used to start with loading.Wait() on the calling goroutine, and
// it is called from click handlers, pagination links and tag selections. One
// stalled triplestore or filehost therefore froze the window: the click did
// nothing, repainting stopped, and there was no cancel.
func TestLoadGalleryDoesNotBlockOnFetch(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 4)
	viewer, _ := setupTestGallery(t, dir, 4, fyne.NewSize(800, 600))

	release := make(chan struct{})
	fetched := make(chan struct{})
	viewer.ReadCustomAsync(func() []CustomReader {
		<-release
		close(fetched)
		return nil
	})

	start := time.Now()
	viewer.LoadGallery()
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Fatalf("LoadGallery blocked %v behind an in-flight listing fetch", d)
	}

	close(release)
	select {
	case <-fetched:
	case <-time.After(5 * time.Second):
		t.Fatal("the listing fetch never ran")
	}
}

// TestChangePageDoesNotBlockOnInFlightLoads covers the other half: page turns
// used to drain the load queue and wait for every in-flight thumbnail on the
// UI goroutine. For a tie cache miss that wait is a full blob download, so
// paging through a remote directory stalled on the network.
func TestChangePageDoesNotBlockOnInFlightLoads(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 12)
	viewer, _ := setupTestGallery(t, dir, 4, fyne.NewSize(800, 600))

	// Queue a page, then turn the page while its loads are still running.
	viewer.ChangePage(1)
	start := time.Now()
	viewer.ChangePage(2)
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Fatalf("ChangePage blocked %v for the previous page's thumbnail loads", d)
	}
	settleLayout(viewer)
	if got := viewer.layout.offset; got != 8 {
		t.Errorf("offset = %d after paging to page 2 of 12 at 4/page, want 8", got)
	}
}

// TestPlacementWaitsForPendingFetch pins that deferring the wait does not lose
// the result: the page that gets laid out is the one the fetch produced, not
// whatever was on screen when LoadGallery was called.
func TestPlacementWaitsForPendingFetch(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 4)
	viewer, _ := setupTestGallery(t, dir, 4, fyne.NewSize(800, 600))
	if viewer.ImageCount() != 4 {
		t.Fatalf("setup: %d images", viewer.ImageCount())
	}

	raw := testJPEGBytes(t, 64, 48, color.RGBA{200, 30, 30, 255})
	readers := []CustomReader{
		memReader{path: "remote-a", data: raw},
		memReader{path: "remote-b", data: raw},
		memReader{path: "remote-c", data: raw},
	}
	viewer.ReadCustomAsync(func() []CustomReader {
		time.Sleep(200 * time.Millisecond) // a slow tie query
		return readers
	})
	viewer.LoadGallery()

	waitFor(t, func() bool { return viewer.ImageCount() == 3 }, "the fetched listing to be installed")
	settleLayout(viewer)
	if got := len(viewer.layout.tiles); got != 3 {
		t.Errorf("%d tiles placed, want 3 (the page must come from the fetch, not the old list)", got)
	}
}

// TestSupersededPlacementIsSkipped: hammering the pagination used to queue a
// placement per click. Each now checks its generation and stands down if a
// newer request has been queued, so the last page wins without laying the
// intermediate ones out.
func TestSupersededPlacementIsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 20)
	viewer, _ := setupTestGallery(t, dir, 4, fyne.NewSize(800, 600))

	for page := 1; page <= 4; page++ {
		viewer.ChangePage(page)
	}
	settleLayout(viewer)

	if got := viewer.layout.offset; got != 16 {
		t.Errorf("offset = %d, want 16 (page 4 of 20 at 4/page)", got)
	}
	if got := len(viewer.layout.tiles); got != 4 {
		t.Errorf("%d tiles, want 4", got)
	}
}

// TestFetchTrackingBalances guards the counter that decides whether a placement
// has to wait: an unbalanced begin/end would either stall every placement
// behind a phantom fetch or let one race ahead of a real listing.
func TestFetchTrackingBalances(t *testing.T) {
	viewer, _ := setupTestGallery(t, t.TempDir(), 4, fyne.NewSize(800, 600))
	if viewer.fetchPending() {
		t.Fatal("a settled gallery reports a pending fetch")
	}

	dir := t.TempDir()
	writeTestImages(t, dir, 2)
	viewer.ReadImageDir(filepath.Join(dir, "missing"), nil)
	if viewer.fetchPending() {
		t.Fatal("ReadImageDir left the fetch count raised")
	}

	release := make(chan struct{})
	done := make(chan struct{})
	viewer.ReadCustomAsync(func() []CustomReader { <-release; close(done); return nil })
	if !viewer.fetchPending() {
		t.Fatal("an in-flight ReadCustomAsync is not counted")
	}
	close(release)
	<-done
	waitFor(t, func() bool { return !viewer.fetchPending() }, "the fetch count to drop")
}
