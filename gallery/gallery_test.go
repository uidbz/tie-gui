package gallery

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// testExifJPEG is a 120x90 JPEG carrying a minimal EXIF block
// (Make=TestMake, Model=TestCamera), base64-encoded.
const testExifJPEG = `/9j/4AAQSkZJRgABAQAAAQABAAD/4QBERXhpZgAATU0AKgAAAAgAAgEPAAIAAAAJAAAAJgEQAAIAAAALAAAAMAAAAABUZXN0TWFr
ZQAAVGVzdENhbWVyYQAA/9sAQwADAgIDAgIDAwMDBAMDBAUIBQUEBAUKBwcGCAwKDAwLCgsLDQ4SEA0OEQ4LCxAWEBETFBUVFQwP
FxgWFBgSFBUU/9sAQwEDBAQFBAUJBQUJFA0LDRQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU
FBQU/8AAEQgAWgB4AwEiAAIRAQMRAf/EAB8AAAEFAQEBAQEBAAAAAAAAAAABAgMEBQYHCAkKC//EALUQAAIBAwMCBAMFBQQEAAAB
fQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpj
ZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn
6Onq8fLz9PX29/j5+v/EAB8BAAMBAQEBAQEBAQEAAAAAAAABAgMEBQYHCAkKC//EALURAAIBAgQEAwQHBQQEAAECdwABAgMRBAUh
MQYSQVEHYXETIjKBCBRCkaGxwQkjM1LwFWJy0QoWJDThJfEXGBkaJicoKSo1Njc4OTpDREVGR0hJSlNUVVZXWFlaY2RlZmdoaWpz
dHV2d3h5eoKDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uLj5OXm5+jp6vLz9PX2
9/j5+v/aAAwDAQACEQMRAD8A8Kooor+rT+cgooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigA
ooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAoooo
AKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKACiiigAooooAKKKKAP/2Q==`

// makeExifJPEG decodes testExifJPEG.
func makeExifJPEG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(testExifJPEG, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// setupTestGallery builds a Gallery on the test driver with dir's images
// loaded and the initial page placed, and returns the viewer ready for
// interaction. It blocks until all background work (placement, thumbnail
// workers, and the tile updater's trailing flush) has settled, so test
// assertions never run concurrently with the layout goroutines (the test
// driver executes fyne.Do inline instead of marshalling to a UI thread).
func setupTestGallery(t *testing.T, dir string, imagesPerPage int, winSize fyne.Size) (*Gallery, fyne.Window) {
	t.Helper()
	test.NewApp()
	win := test.NewWindow(nil)

	config := Config{}
	config.General.TileWidth = 300
	config.General.TileGap = 5
	config.General.Workers = 2
	config.General.ImagesPerPage = imagesPerPage
	config.General.ThumbnailDir = filepath.Join(t.TempDir(), "cache")

	viewer := NewGallery(fyne.CurrentApp(), win, config, nil)
	viewer.Init()
	viewer.ReadImageDir(dir, nil)
	// Pre-set the (known) dimensions so placeholder tiles have the final
	// aspect ratio and row positions stay stable when real thumbnails land.
	for _, info := range viewer.imageFiles {
		info.Width, info.Height = 120, 90
	}
	win.SetContent(viewer.Content)
	win.Resize(winSize)
	viewer.LoadGallery()
	viewer.CreateView()
	win.SetContent(viewer.Content)
	settleLayout(viewer)
	return viewer, win
}

// settleLayout waits for a page's async work to finish: tile placement, the
// thumbnail workers (their cache writes included), and the tile updater's
// trailing debounce flush.
func settleLayout(viewer *Gallery) {
	viewer.layout.placement.Wait()
	viewer.layout.currentlyLoading.Wait()
	// The tile updater's trailing flush fires up to flushInterval after the
	// last loaded tile; wait it out so no goroutine mutates the tile list
	// while the test inspects it.
	time.Sleep(2 * flushInterval)
}

// forceGridLayout simulates the driver's layout cascade, which the test
// driver does not run: it sizes the scroll viewport and grid and runs the
// tile layout so minHeight and tile positions are computed.
func forceGridLayout(viewer *Gallery, w, h float32) {
	viewer.scroll.Resize(fyne.NewSize(w, h))
	viewer.layout.relayoutGrid()
}

// writeTestImages creates n tiny JPEGs in dir.
func writeTestImages(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		writeTestJPEG(t, filepath.Join(dir, fmt.Sprintf("img%03d.jpg", i)), 120, 90,
			color.RGBA{uint8(i * 3), 100, 200, 255})
	}
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The bottom bar shows 2-6 page links depending on the grid width, with the
// hidden middle range elided into a single "…" label.
func TestPaginationElisionWidget(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 72) // 12 pages at 6 per page

	viewer, _ := setupTestGallery(t, dir, 6, fyne.NewSize(1024, 768))

	pageLinks := func() (links []*widget.Hyperlink, ellipses int) {
		for _, obj := range viewer.bottomBar.Objects[2:] {
			switch o := obj.(type) {
			case *widget.Hyperlink:
				links = append(links, o)
			case *widget.Label:
				if o.Text == "…" {
					ellipses++
				}
			}
		}
		return links, ellipses
	}

	// Narrow grid: only 2 page slots -> first + last with one middle gap.
	viewer.layout.grid.Resize(fyne.NewSize(500, 600))
	viewer.buildPagination()
	links, ellipses := pageLinks()
	if len(links) != 2 || ellipses != 1 {
		t.Fatalf("narrow: links=%d ellipses=%d, want 2 links and 1 ellipsis", len(links), ellipses)
	}

	// Wide grid: 5 slots -> first, window around page 0, last.
	viewer.layout.grid.Resize(fyne.NewSize(910, 600))
	viewer.buildPagination()
	links, ellipses = pageLinks()
	if len(links) != 5 || ellipses != 1 {
		t.Fatalf("wide: links=%d ellipses=%d, want 5 links and 1 ellipsis", len(links), ellipses)
	}
	if links[len(links)-1].Text != "67-72" {
		t.Fatalf("wide: last link = %q, want %q", links[len(links)-1].Text, "67-72")
	}

	// Interior current page: current stays visible, gaps on both sides.
	viewer.currentPage = 6
	viewer.buildPagination()
	links, ellipses = pageLinks()
	if len(links) != 5 || ellipses != 2 {
		t.Fatalf("interior: links=%d ellipses=%d, want 5 links and 2 ellipses", len(links), ellipses)
	}
	bold := -1
	for i, l := range links {
		if l.TextStyle.Bold {
			bold = i
		}
	}
	if bold < 0 || links[bold].Text != "37-42" {
		t.Fatalf("interior: current page link missing/bold wrong (bold index %d)", bold)
	}
}

// ChangePage must return the grid scroll position to the top.
func TestChangePageScrollsTop(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 24) // 4 pages at 6 per page

	viewer, _ := setupTestGallery(t, dir, 6, fyne.NewSize(650, 500))
	forceGridLayout(viewer, 650, 460)
	if viewer.layout.minHeight <= 460 {
		t.Fatalf("minHeight = %v, want > 460 (grid taller than viewport)", viewer.layout.minHeight)
	}

	viewer.scroll.ScrollToOffset(fyne.NewPos(0, 300))
	if viewer.scroll.Offset.Y == 0 {
		t.Fatal("precondition failed: scroll offset did not move down")
	}
	viewer.ChangePage(1)
	if viewer.currentPage != 1 {
		t.Fatalf("currentPage = %d, want 1", viewer.currentPage)
	}
	if viewer.scroll.Offset.Y != 0 {
		t.Fatalf("scroll offset after ChangePage = %v, want top (0)", viewer.scroll.Offset.Y)
	}
	settleLayout(viewer)
}

// Returning from the single-image view switches back to the opened image's
// page and scrolls its tile into view — even when next/prev navigation moved
// onto a different page than the one the user left.
func TestShowGalleryRevealsOpenedTile(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 24) // 4 pages at 6 per page

	viewer, _ := setupTestGallery(t, dir, 6, fyne.NewSize(650, 500))
	forceGridLayout(viewer, 650, 460)

	// Open an image on the second page, then come back.
	target := viewer.imageFiles[9]
	viewer.ChangeImage(target)
	viewer.showGallery()

	if viewer.currentPage != 1 {
		t.Fatalf("currentPage = %d, want 1 (page of opened image)", viewer.currentPage)
	}
	settleLayout(viewer) // waits for placement, which consumes pendingReveal
	if viewer.layout.pendingReveal != -1 {
		t.Fatal("pendingReveal was not consumed by the page placement")
	}
	if viewer.scroll.Offset.Y <= 0 {
		t.Fatalf("scroll offset = %v, want > 0 (tile of opened image revealed)", viewer.scroll.Offset.Y)
	}
	// The revealed tile must be the opened image (page-relative index 3).
	tile := viewer.layout.tiles[3]
	if tile.Info != target {
		t.Fatalf("tiles[3].Info = %p, want the opened image %p", tile.Info, target)
	}
	top := tile.Position().Y
	off := viewer.scroll.Offset.Y
	vp := viewer.scroll.Size().Height
	if top < off || top > off+vp {
		t.Fatalf("tile top %v not within viewport [%v, %v]", top, off, off+vp)
	}
}

// syncInfoMetadata makes the info overlay's metadata load synchronous, so
// tests don't race the test driver's inline fyne.Do against UI-thread text
// shaping.
func syncInfoMetadata(viewer *Gallery) {
	viewer.infoMetadataFn = func(info *ImageInfo, apply func(imageMetadata)) {
		apply(collectImageMetadata(info))
	}
}

// The info overlay toggles over the image view, carries the basic metadata,
// and is hidden again when returning to the grid.
func TestInfoOverlayToggle(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 3)

	viewer, _ := setupTestGallery(t, dir, 6, fyne.NewSize(650, 500))
	syncInfoMetadata(viewer)

	info := viewer.imageFiles[1]
	viewer.ChangeImage(info)

	viewer.ToggleInfoOverlay()
	if viewer.infoOverlay == nil || !viewer.infoOverlay.Visible() {
		t.Fatal("overlay not visible after toggle on")
	}
	found := false
	for _, obj := range viewer.Content.Objects {
		if obj == viewer.infoOverlay {
			found = true
		}
	}
	if !found {
		t.Fatal("overlay not stacked in the content")
	}
	// The full metadata loads on a goroutine; wait for the size row.
	waitFor(t, func() bool {
		for _, seg := range viewer.infoOverlay.text.Segments {
			if ts, ok := seg.(*widget.TextSegment); ok && ts.Text == "Size: " {
				return true
			}
		}
		return false
	}, "metadata size row")
	// Filename is in the title.
	if viewer.infoOverlay.title.Text != "img001.jpg" {
		t.Fatalf("overlay title = %q, want %q", viewer.infoOverlay.title.Text, "img001.jpg")
	}

	// Returning to the grid hides the overlay.
	viewer.showGallery()
	if viewer.infoOverlay.Visible() {
		t.Fatal("overlay still visible after returning to the grid")
	}
}

// Tapping the scrim (outside the panel) closes the overlay; a tap inside the
// panel is ignored.
func TestInfoOverlayScrimTap(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 3)

	viewer, _ := setupTestGallery(t, dir, 6, fyne.NewSize(650, 500))
	syncInfoMetadata(viewer)
	viewer.ChangeImage(viewer.imageFiles[0])
	viewer.ToggleInfoOverlay()
	overlay := viewer.infoOverlay
	if overlay == nil || !overlay.Visible() {
		t.Fatal("overlay not open")
	}
	// Lay out at a known size so the panel bounds are computed.
	overlay.Resize(fyne.NewSize(800, 600))
	overlay.CreateRenderer().Layout(fyne.NewSize(800, 600))

	// Tap inside the panel: ignored.
	center := fyne.NewPos(400, 300)
	overlay.Tapped(&fyne.PointEvent{Position: center})
	if !overlay.Visible() {
		t.Fatal("tap inside the panel closed the overlay")
	}
	// Tap on the scrim (top-left corner): closes.
	overlay.Tapped(&fyne.PointEvent{Position: fyne.NewPos(5, 5)})
	if overlay.Visible() {
		t.Fatal("scrim tap did not close the overlay")
	}
}

// setupTestGallery with EXIF-bearing image: the overlay shows the EXIF
// section for images that carry it.
func TestInfoOverlayExifSection(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 2)
	// Inject a minimal EXIF block into one file.
	exifJPEG := makeExifJPEG(t)
	if err := os.WriteFile(filepath.Join(dir, "img001.jpg"), exifJPEG, 0644); err != nil {
		t.Fatal(err)
	}

	viewer, _ := setupTestGallery(t, dir, 6, fyne.NewSize(650, 500))
	syncInfoMetadata(viewer)
	var target *ImageInfo
	for _, f := range viewer.imageFiles {
		if filepath.Base(f.Path) == "img001.jpg" {
			target = f
		}
	}
	viewer.ChangeImage(target)
	viewer.ToggleInfoOverlay()

	waitFor(t, func() bool {
		for _, seg := range viewer.infoOverlay.text.Segments {
			if ts, ok := seg.(*widget.TextSegment); ok && ts.Text == "TestCamera\n" {
				return true
			}
		}
		return false
	}, "EXIF model row")
}
