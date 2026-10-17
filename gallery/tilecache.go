package gallery

import (
	"container/list"
	"image"
	"sync"
)

// thumbnailCache is an in-memory LRU cache of decoded thumbnail bitmaps.
//
// It used to cache *Tile widgets, which caused two separate problems:
//
//   - A Fyne CanvasObject has one parent and one position, so handing the same
//     *Tile to two grid slots broke the grid. ImageInfo.Path is the content
//     hash for tie entries, so the same image listed under two names — or a
//     duplicate UID, which tie has — got one widget laid out twice: one entry
//     painted over the other, the second slot stayed blank, and preview
//     cycling and the drag overlays acted on both "entries" at once. Caching
//     the bitmap and building a fresh Tile per slot keeps the decode savings
//     and gives every slot its own object.
//   - The budget counted tiles. Each holds a decoded RGBA of roughly
//     (2*TileWidth) x (2*TileWidth*aspect), so at the default TileWidth of 300
//     a portrait thumbnail is ~2.8 MB and the 1500-entry budget allowed close
//     to 4 GB, with TileWidth user-configurable on top of that. Budgeting the
//     actual bytes makes the limit mean what it says.
//
// Eviction is now least-recently-used rather than insertion order: the old
// FIFO evicted exactly the page the user had just scrolled back to, which is
// the case the budget was raised to serve.
type thumbnailCache struct {
	maxBytes int64

	mu     sync.Mutex
	lru    *list.List // front = most recently used
	byPath map[string]*list.Element
	bytes  int64
}

type thumbEntry struct {
	path  string
	img   *image.RGBA
	bytes int64
}

// newThumbnailCache returns a cache holding at most maxBytes of decoded
// thumbnails.
func newThumbnailCache(maxBytes int64) *thumbnailCache {
	return &thumbnailCache{
		maxBytes: maxBytes,
		lru:      list.New(),
		byPath:   make(map[string]*list.Element),
	}
}

// get returns a cached bitmap and marks it the most recently used entry.
func (c *thumbnailCache) get(path string) (*image.RGBA, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byPath[path]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*thumbEntry).img, true
}

// put stores a decoded bitmap, replacing any entry for path, and evicts the
// coldest entries until the cache is back inside its byte budget.
//
// A bitmap larger than the whole budget is not cached: keeping it (the old
// "never evict what we just admitted" rule) would throw away every warmer
// entry to hold one oversized one.
func (c *thumbnailCache) put(path string, img *image.RGBA) {
	size := imageBytes(img)
	if img == nil || size == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.byPath[path]; ok {
		old := el.Value.(*thumbEntry)
		c.bytes -= old.bytes
		old.img = img
		old.bytes = size
		c.bytes += size
		c.lru.MoveToFront(el)
	} else {
		c.byPath[path] = c.lru.PushFront(&thumbEntry{path: path, img: img, bytes: size})
		c.bytes += size
	}
	if size > c.maxBytes {
		// This one entry cannot fit. Drop it and let the rest of the cache be.
		c.evictOneLocked()
	}
	for c.bytes > c.maxBytes {
		if !c.evictOneLocked() {
			break
		}
	}
}

// evictOneLocked drops the least recently used entry. It reports whether
// anything was removed. Callers must hold c.mu.
func (c *thumbnailCache) evictOneLocked() bool {
	el := c.lru.Back()
	if el == nil {
		return false
	}
	e := el.Value.(*thumbEntry)
	c.lru.Remove(el)
	delete(c.byPath, e.path)
	c.bytes -= e.bytes
	return true
}

// clear empties the cache.
func (c *thumbnailCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru = list.New()
	c.byPath = make(map[string]*list.Element)
	c.bytes = 0
}

// len reports how many bitmaps are cached.
func (c *thumbnailCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byPath)
}

// size reports the bytes of decoded pixel data the cache holds.
func (c *thumbnailCache) size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// imageBytes is the heap a decoded RGBA bitmap costs. Fyne uploads *image.RGBA
// pixel data directly, so this is also what a visible tile pins in memory.
func imageBytes(img *image.RGBA) int64 {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	return int64(b.Dx()) * int64(b.Dy()) * 4
}
