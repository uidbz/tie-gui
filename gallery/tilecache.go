package gallery

import (
	"sync"
)

// tileCache is an in-memory LRU cache for thumbnail tiles. It enforces a size
// budget by evicting the oldest entries when the cache fills up. Entries are
// tracked in insertion order; the front of the list is oldest.
type tileCache struct {
	maxSize int // maximum number of tiles to cache

	mu      sync.Mutex
	entries map[string]*Tile
	order   []string // paths in insertion order; front = oldest (evict first)
}

// newTileCache creates a tileCache that holds up to maxSize tiles in memory.
func newTileCache(maxSize int) *tileCache {
	return &tileCache{
		maxSize: maxSize,
		entries: make(map[string]*Tile),
	}
}

// get retrieves a tile by path, returning (tile, true) if present or (nil, false)
// if absent. Access does not update LRU position (insertion-order LRU, not access-order).
func (c *tileCache) get(path string) (*Tile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.entries[path]
	return t, ok
}

// put stores a tile in the cache and evicts old entries if the cache exceeds maxSize.
func (c *tileCache) put(path string, tile *Tile) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If already present, don't add it again (preserves original insertion order).
	if _, ok := c.entries[path]; ok {
		return
	}

	c.entries[path] = tile
	c.order = append(c.order, path)
	c.evictLocked(path)
}

// evictLocked drops the oldest tiles until the cache size is within maxSize.
// keep is the path just admitted; it is rotated to the back rather than evicted
// immediately (a tile that would overflow the budget is still cached).
// Must be called with c.mu held.
func (c *tileCache) evictLocked(keep string) {
	for len(c.entries) > c.maxSize && len(c.order) > 0 {
		oldest := c.order[0]
		if oldest == keep {
			// Never evict the tile we just admitted.
			if len(c.order) == 1 {
				break
			}
			c.order = append(c.order[1:], oldest)
			continue
		}
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// clear removes all cached tiles.
func (c *tileCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*Tile)
	c.order = nil
}
