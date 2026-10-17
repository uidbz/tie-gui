package gallery

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// slowThumbnailer stands in for a tie filehost that takes a moment per
// thumbnail: it keeps the loader workers busy so a page turn is guaranteed to
// land in the middle of a page of transfers.
type slowThumbnailer struct {
	mu     sync.Mutex
	delay  time.Duration
	jpeg   []byte
	calls  map[string]int
	failed bool // when set, every call fails
}

func (s *slowThumbnailer) GetThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[info.Path]++
	failed := s.failed
	jpeg := s.jpeg
	s.mu.Unlock()
	if failed {
		return nil, errors.New("thumbnail source failing on purpose")
	}
	time.Sleep(s.delay)
	return bytes.NewReader(jpeg), nil
}

func (s *slowThumbnailer) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[path]
}

func (s *slowThumbnailer) setFailing(v bool) {
	s.mu.Lock()
	s.failed = v
	s.mu.Unlock()
}

// churnGallery builds a reader-backed gallery — the Thumbnailer only serves
// CustomReader entries, plain local files go through the disk cache — whose
// thumbnails come from a deliberately slow source.
func churnGallery(t *testing.T, entries int, thumb *slowThumbnailer) (*Gallery, fyne.Window) {
	t.Helper()
	test.NewApp()
	win := test.NewWindow(nil)

	config := Config{}
	config.General.TileWidth = 200
	config.General.TileGap = 5
	config.General.Workers = 4
	config.General.ImagesPerPage = 4
	config.General.ThumbnailDir = filepath.Join(t.TempDir(), "cache")

	viewer := NewGallery(fyne.CurrentApp(), win, config, nil)
	viewer.Thumbnailer = thumb // must be set before Init copies it into the layout
	viewer.Init()

	readers := make([]CustomReader, 0, entries)
	for i := 0; i < entries; i++ {
		readers = append(readers, memReader{
			path: fmt.Sprintf("tie:%04d-hash", i),
			data: thumb.jpeg,
		})
	}
	viewer.ReadCustom(readers)
	win.SetContent(viewer.Content)
	win.Resize(fyne.NewSize(900, 700))
	viewer.LoadGallery()
	viewer.AwaitIdle()
	return viewer, win
}

// TestRapidPagingUnderLoad is the -race stress case the review asked for
// (§10): page turns and listing swaps landing while thumbnail workers are
// still running, which is what paging through a remote tie directory does.
// Every placement, queue drain, write-back and cache access in the gallery is
// exercised concurrently, so under -race a missing edge shows up as a report.
//
// The assertions are behavioural too: after the last page settles, every tile
// must show a real thumbnail — no placeholder left behind by a write-back that
// was dropped as stale, and no tile from an earlier page painted onto a later
// one.
func TestRapidPagingUnderLoad(t *testing.T) {
	raw := testJPEGBytes(t, 120, 90, color.RGBA{10, 120, 30, 255})
	thumb := &slowThumbnailer{delay: 4 * time.Millisecond, jpeg: raw}
	viewer, _ := churnGallery(t, 24, thumb)

	// 20 page turns across 6 pages, with an async listing swap every third
	// turn so a fetch goroutine is in flight while the next placement starts.
	lastPage := 0
	for round := 0; round < 20; round++ {
		lastPage = round % 6
		viewer.ChangePage(lastPage)
		if round%3 == 0 {
			viewer.ReadCustomAsync(func() []CustomReader {
				readers := make([]CustomReader, 0, 12)
				for i := 0; i < 12; i++ {
					readers = append(readers, memReader{
						path: fmt.Sprintf("remote-%02d-hash", i),
						data: raw,
					})
				}
				return readers
			})
			viewer.LoadGallery()
		}
		// Settle between rounds. Everything that matters is still racing
		// inside a round — the listing fetch, the placement goroutine, four
		// thumbnail workers and the tile cache — but two UI-level actions
		// must not be in flight at once: in production they would be
		// serialised by the single UI goroutine, which the test driver has
		// no equivalent of.
		viewer.AwaitIdle()
	}

	if got := len(viewer.files()); got != 12 {
		t.Fatalf("%d entries after the listing swaps, want 12", got)
	}
	if got := viewer.layout.getOffset(); got != lastPage*4 {
		t.Errorf("offset = %d, want %d: the last page requested must win",
			got, lastPage*4)
	}
	if got := len(viewer.layout.tiles); got == 0 {
		t.Fatal("no tiles after settling")
	}
	for i, tile := range viewer.layout.tiles {
		if tile == nil || tile.Content == nil || tile.Content.Image == nil {
			t.Fatalf("tile %d has no image after settling", i)
		}
		if b := tile.Content.Image.Bounds(); b.Dx() != 120 || b.Dy() != 90 {
			t.Errorf("tile %d shows a %v image, want the 120x90 thumbnail: a "+
				"placeholder survived the last placement", i, b)
		}
	}
	if viewer.fetchPending() {
		t.Error("a listing fetch is still counted after AwaitIdle")
	}
}

// TestPlacementSupersededUnderLoad queues placements without letting any of
// them settle in between: the generation check must leave exactly the last
// page laid out, and no earlier placement may install tiles after it.
func TestPlacementSupersededUnderLoad(t *testing.T) {
	raw := testJPEGBytes(t, 60, 60, color.RGBA{30, 30, 160, 255})
	thumb := &slowThumbnailer{delay: 3 * time.Millisecond, jpeg: raw}
	viewer, _ := churnGallery(t, 40, thumb)

	for page := 0; page < 10; page++ {
		viewer.ChangePage(page)
	}
	viewer.AwaitIdle()

	if got := viewer.layout.getOffset(); got != 36 {
		t.Errorf("offset = %d, want 36 (page 9 of 40 at 4/page)", got)
	}
	if got := len(viewer.layout.tiles); got != 4 {
		t.Fatalf("%d tiles, want 4", got)
	}
	for i, tile := range viewer.layout.tiles {
		if tile == nil || tile.Content == nil || tile.Content.Image == nil {
			t.Fatalf("tile %d has no image", i)
		}
	}
}

// TestThumbnailFailureUnderLoad drives the retry path concurrently with page
// turns: a failing load is re-queued onto the same queue the next placement
// drains, so the retry budget must still terminate rather than spin, and a
// source that recovers must still fill the page.
func TestThumbnailFailureUnderLoad(t *testing.T) {
	raw := testJPEGBytes(t, 80, 80, color.RGBA{200, 20, 20, 255})
	thumb := &slowThumbnailer{delay: time.Millisecond, jpeg: raw}
	viewer, _ := churnGallery(t, 12, thumb)

	thumb.setFailing(true)
	for round := 0; round < 6; round++ {
		viewer.ChangePage(round % 3)
	}
	viewer.AwaitIdle()

	// Bounded retries. AwaitIdle returning proves the workers finished; the
	// counts prove they did not spin. The budget is per placement — each page
	// turn resets an entry's attempt count, which is what lets a source that
	// recovers fill the page later — so the ceiling is the number of times
	// this entry's page was placed times one attempt plus the budget.
	// Pages 0,1,2 were each visited twice here.
	perEntry := 2 * (1 + maxTileLoadAttempts)
	for _, info := range viewer.files() {
		if got := thumb.count(info.Path); got > perEntry {
			t.Errorf("%s attempted %d times, want at most %d", info.Path, got, perEntry)
		}
	}

	// A source that recovers must fill the page: the retry budget is per
	// placement, so a fresh page turn gets a fresh one.
	thumb.setFailing(false)
	viewer.ChangePage(0)
	viewer.AwaitIdle()
	for i, tile := range viewer.layout.tiles {
		if tile == nil || tile.Content == nil || tile.Content.Image == nil {
			t.Fatalf("tile %d has no image after the source recovered", i)
		}
		if b := tile.Content.Image.Bounds(); b.Dx() != 80 {
			t.Errorf("tile %d shows a %v image after recovery, want 80x80", i, b)
		}
	}
}
