package gallery

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func rgbaBitmap(w, h int) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// TestCacheBudgetIsBytes is the regression for review §4.5. The budget counted
// tiles, and each tile holds a decoded RGBA of roughly (2*TileWidth) x
// (2*TileWidth*aspect) bytes: at the default TileWidth of 300 a portrait
// thumbnail is ~2.8 MB, so the 1500-entry budget allowed close to 4 GB, with
// TileWidth user-configurable on top of that.
func TestCacheBudgetIsBytes(t *testing.T) {
	const budget = 4 << 20 // 4 MB
	c := newThumbnailCache(budget)

	// 512x512 RGBA = 1 MB each.
	for i := 0; i < 10; i++ {
		c.put(pathFor(i), rgbaBitmap(512, 512))
	}
	if got := c.size(); got > budget {
		t.Errorf("cache holds %d bytes, over its %d byte budget", got, budget)
	}
	if got := c.len(); got == 0 {
		t.Error("the cache evicted everything")
	}
	if got := c.len(); got > 4 {
		t.Errorf("%d entries for 1 MB each in a 4 MB budget", got)
	}

	// A single bitmap bigger than the whole budget must not push everything
	// else out; it is simply not cached.
	c.put("huge", rgbaBitmap(4096, 4096)) // 64 MB
	if _, ok := c.get("huge"); ok {
		t.Error("an oversized bitmap was cached")
	}
	if got := c.size(); got > budget {
		t.Errorf("cache holds %d bytes after admitting an oversized entry", got)
	}
}

func pathFor(i int) string { return filepath.Join("dir", string(rune('a'+i))+".jpg") }

// TestCacheIsLRU: eviction used to be insertion order despite the name, so it
// threw away exactly the page the user had just scrolled back to — the case
// the budget was raised to serve.
func TestCacheIsLRU(t *testing.T) {
	const budget = 3 << 20 // 3 MB
	c := newThumbnailCache(budget)

	for i := 0; i < 3; i++ {
		c.put(pathFor(i), rgbaBitmap(512, 512)) // 1 MB each
	}
	// Touch the oldest so it becomes the most recently used.
	if _, ok := c.get(pathFor(0)); !ok {
		t.Fatal("entry 0 missing before eviction")
	}
	// Push a fourth: the coldest entry must go, and that is now entry 1.
	c.put(pathFor(3), rgbaBitmap(512, 512))

	if _, ok := c.get(pathFor(0)); !ok {
		t.Error("a re-read entry was evicted before a colder one")
	}
	if _, ok := c.get(pathFor(1)); ok {
		t.Error("the coldest entry survived; eviction is not LRU")
	}
}

// TestCacheReplacesEntry covers re-decoding the same path (a refresh with
// refreshThumbs set): the byte accounting must follow the replacement.
func TestCacheReplacesEntry(t *testing.T) {
	c := newThumbnailCache(10 << 20)
	c.put("x", rgbaBitmap(100, 100)) // 40 KB
	before := c.size()
	c.put("x", rgbaBitmap(1000, 1000)) // 4 MB
	after := c.size()
	if c.len() != 1 {
		t.Fatalf("len = %d, want 1 (same path, replaced)", c.len())
	}
	if want := before + (1000*1000*4 - 100*100*4); after != want {
		t.Errorf("size = %d, want %d", after, want)
	}
}

func TestCacheClear(t *testing.T) {
	c := newThumbnailCache(10 << 20)
	c.put("a", rgbaBitmap(64, 64))
	c.clear()
	if c.len() != 0 || c.size() != 0 {
		t.Errorf("clear left len=%d size=%d", c.len(), c.size())
	}
	if _, ok := c.get("a"); ok {
		t.Error("a cleared entry is still readable")
	}
}

// TestSameHashTwoTiles is the regression for review §3.8. The cache used to
// hand back the same *Tile for the same ImageInfo.Path, and for tie entries
// Path is the content hash — so the same image under two names, or a duplicate
// UID, landed the same Fyne object in two grid slots. A CanvasObject has one
// parent and one position, so Layout moved it twice: one entry painted over
// the other, the other slot stayed blank, and the preview and drag overlays
// acted on both "entries" at once.
func TestSameHashTwoTiles(t *testing.T) {
	dir := t.TempDir()
	thumbDir := filepath.Join(dir, "cache")

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()

	// Two entries, one content hash: the tie case.
	infos := []*ImageInfo{
		NewImageInfoCustomReader(0, memReader{path: "tie:/a/song-cover", data: raw}),
		NewImageInfoCustomReader(1, memReader{path: "tie:/b/song-cover", data: raw}),
	}
	// Both share the same content hash, which is what Path is for tie entries.
	infos[0].Path = "tie:/dup-hash-abc"
	infos[1].Path = "tie:/dup-hash-abc"

	test.NewApp()
	win := test.NewWindow(nil)
	config := Config{}
	config.General.TileWidth = 200
	config.General.TileGap = 5
	config.General.Workers = 2
	config.General.ImagesPerPage = 4
	config.General.ThumbnailDir = thumbDir

	viewer := NewGallery(fyne.CurrentApp(), win, config, nil)
	viewer.Init()
	viewer.ReadCustom([]CustomReader{
		memReader{path: "tie:/dup-hash-abc", data: raw},
	})
	if viewer.ImageCount() != 1 {
		t.Fatalf("setup: %d images", viewer.ImageCount())
	}

	layout := viewer.layout
	bitmap, err := decodeThumbnailRGBA(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	layout.bitmapToCache(infos[0].Path, bitmap)

	cached, ok := layout.bitmapFromCache(infos[1].Path)
	if !ok {
		t.Fatal("the second entry missed the cached bitmap")
	}
	tileA := layout.newImageTileFromImage(cached, infos[0], nil)
	tileB := layout.newImageTileFromImage(cached, infos[1], nil)

	if tileA == tileB {
		t.Fatal("two entries share one *Tile")
	}
	// The tiles are separate objects, but they must still share the decoded
	// pixels — that is what the cache is for.
	if tileA.Content.Image != tileB.Content.Image {
		t.Error("the two tiles did not share the decoded bitmap: the cache saved nothing")
	}
	if tileA.Info == tileB.Info {
		t.Error("the two tiles carry the same ImageInfo")
	}
	// Independent geometry: sizing one must not move the other.
	tileA.Resize(fyne.NewSize(120, 90))
	if tileB.Size() == tileA.Size() {
		t.Errorf("resizing tileA also resized tileB: %v", tileB.Size())
	}
}

// TestImageLoaderGivesEverySlotItsOwnTile drives the real worker path with two
// entries that share a content hash.
func TestImageLoaderGivesEverySlotItsOwnTile(t *testing.T) {
	dir := t.TempDir()
	writeTestImages(t, dir, 2)
	viewer, _ := setupTestGallery(t, dir, 2, fyne.NewSize(800, 600))

	// Point both entries at the same cache key, the tie duplicate case.
	if len(viewer.imageFiles) < 2 {
		t.Fatalf("setup: %d images", len(viewer.imageFiles))
	}
	viewer.imageFiles[1].Path = viewer.imageFiles[0].Path
	settleLayout(viewer)

	if len(viewer.layout.tiles) != 2 {
		t.Fatalf("%d tiles, want 2", len(viewer.layout.tiles))
	}
	if viewer.layout.tiles[0] == viewer.layout.tiles[1] {
		t.Fatal("the grid holds the same *Tile twice")
	}
	if viewer.layout.grid.Objects[0] == viewer.layout.grid.Objects[1] {
		t.Fatal("the grid's object list holds the same object twice")
	}
}
