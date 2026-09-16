package ui

import (
	"bytes"
	"errors"
	"image"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// coverMaxEdge is the pixel width decoded covers are downscaled to. It is sized
// for the largest consumer (the Now Playing page on a HiDPI phone); every other
// consumer — the queue's 32 px column, the mini bar's 64 px thumb, the grouped
// list's 56 px header — scales the same decoded image down at paint time, so one
// fetch and one decode serve them all.
const coverMaxEdge = 512

// coverLimit bounds the number of decoded covers held in memory. Covers are
// ~512² RGBA (~1 MB each), so this is the cache's real memory budget; entries
// are evicted in insertion order like the gallery's tile cache.
const coverLimit = 120

// coverStore caches decoded album artwork keyed by album UID, shared by the
// cover wall, the queue (table column and grouped list), the transport mini bar
// and the Now Playing page. Album covers are fetched from the filehost and
// decoded, which is far too slow to do per table cell per repaint — and the same
// album's cover is asked for by several views at once.
//
// A known-coverless album is cached as a nil image so it is not re-probed on
// every row; a fetch *failure* is not cached, so a cover that was merely
// unreachable (server down, transient error) is retried on the next ask.
type coverStore struct {
	session *data.Session

	mu    sync.Mutex
	imgs  map[string]image.Image // album UID → cover; nil entry = known coverless
	order []string               // insertion order, for eviction
	// inflight tracks album UIDs a fetch is already running for, so N rows
	// asking for the same album at once cause one fetch, not N.
	inflight map[string][]func(image.Image)
	// decodeFn fetches and decodes one album's cover; nil selects the real
	// filehost path. Tests substitute it to exercise the caching policy
	// without a server.
	decodeFn func(albumUID string) (image.Image, error)
}

func newCoverStore(session *data.Session) *coverStore {
	return &coverStore{
		session:  session,
		imgs:     map[string]image.Image{},
		inflight: map[string][]func(image.Image){},
	}
}

// Lookup returns a cached cover without touching the network. ok is false when
// the album has not been resolved yet; the returned image is nil when the album
// is known to have no cover.
func (c *coverStore) Lookup(albumUID string) (image.Image, bool) {
	if albumUID == "" {
		return nil, true // no album: settled, and settled as coverless
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	img, ok := c.imgs[albumUID]
	return img, ok
}

// Request resolves an album's cover, calling done on the Fyne UI goroutine with
// the decoded image (nil when the album has no cover). A cached cover invokes
// done synchronously, so a caller that renders inside a table/list cell update
// paints immediately on a hit and only defers on a miss.
func (c *coverStore) Request(albumUID string, done func(image.Image)) {
	if img, ok := c.Lookup(albumUID); ok {
		if done != nil {
			done(img)
		}
		return
	}

	c.mu.Lock()
	waiters, running := c.inflight[albumUID]
	if done != nil {
		c.inflight[albumUID] = append(waiters, done)
	} else if !running {
		c.inflight[albumUID] = nil
	}
	c.mu.Unlock()
	if running {
		return // a fetch is already on its way; done was queued above
	}

	go c.fetch(albumUID)
}

// fetch loads and decodes one album's cover off the UI goroutine, stores the
// outcome, and hands the result to everyone who asked for it meanwhile.
func (c *coverStore) fetch(albumUID string) {
	img, err := c.decode(albumUID)

	c.mu.Lock()
	waiters := c.inflight[albumUID]
	delete(c.inflight, albumUID)
	// Only a definitive answer is cached: a coverless album (nil, ErrNoCover)
	// is settled forever, but a fetch failure is left uncached so the next ask
	// retries it.
	if err == nil || errors.Is(err, data.ErrNoCover) {
		c.store(albumUID, img)
	}
	c.mu.Unlock()

	if len(waiters) == 0 {
		return
	}
	fyne.Do(func() {
		for _, done := range waiters {
			done(img)
		}
	})
}

// decode fetches and decodes an album's cover. A nil image with a nil error is
// impossible: absence is reported as data.ErrNoCover.
func (c *coverStore) decode(albumUID string) (image.Image, error) {
	if c.decodeFn != nil {
		return c.decodeFn(albumUID)
	}
	blob, err := c.session.CoverBytesForUID(albumUID)
	if err != nil {
		return nil, err
	}
	img, _, err := gallery.Decode(bytes.NewReader(blob))
	if err != nil {
		// Undecodable bytes are as good as no cover: retrying cannot help.
		return nil, data.ErrNoCover
	}
	if b := img.Bounds(); b.Dx() > coverMaxEdge {
		img = gallery.ScaleImage(img, coverMaxEdge)
	}
	return img, nil
}

// store records a resolved cover and evicts the oldest entries past the limit.
// Caller holds c.mu.
func (c *coverStore) store(albumUID string, img image.Image) {
	if _, seen := c.imgs[albumUID]; !seen {
		c.order = append(c.order, albumUID)
	}
	c.imgs[albumUID] = img
	for len(c.order) > coverLimit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.imgs, oldest)
	}
}

// Get resolves an album's cover on the calling goroutine, returning nil when
// the album has none. It is for callers already running off the UI goroutine
// (the gallery's thumbnail workers); UI code uses Request instead. The result
// is cached, so a cover the wall has already drawn costs the queue and the
// transport nothing.
func (c *coverStore) Get(albumUID string) image.Image {
	if img, ok := c.Lookup(albumUID); ok {
		return img
	}
	img, err := c.decode(albumUID)
	if err != nil && !errors.Is(err, data.ErrNoCover) {
		return nil // transient failure: leave it uncached so it is retried
	}
	c.mu.Lock()
	c.store(albumUID, img)
	c.mu.Unlock()
	return img
}

// Clear drops every cached cover. Called after a collection switch, where the
// same album UID can resolve to different artwork.
func (c *coverStore) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.imgs = map[string]image.Image{}
	c.order = nil
}

// coverCell displays one album's art inside a recycled table cell or list row.
// It tracks which album it is currently showing so a fetch that completes after
// the cell has been recycled to a different row is discarded instead of
// painting the wrong album's cover — the same recycling hazard that forces the
// queue's play indicator to be a label rather than an icon.
type coverCell struct {
	widget.BaseWidget
	store    *coverStore
	img      *canvas.Image
	albumUID string
}

func newCoverCell(store *coverStore) *coverCell {
	c := &coverCell{
		store: store,
		img:   &canvas.Image{FillMode: canvas.ImageFillContain, ScaleMode: canvas.ImageScaleFastest},
	}
	c.img.Hide()
	c.ExtendBaseWidget(c)
	return c
}

// show displays the art for albumUID, fetching it if needed. A cache hit paints
// synchronously, so scrolling back over already-seen rows does not flicker.
func (c *coverCell) show(albumUID string) {
	c.albumUID = albumUID
	if c.store == nil || albumUID == "" {
		c.set(nil)
		return
	}
	if img, ok := c.store.Lookup(albumUID); ok {
		c.set(img)
		return
	}
	c.set(nil)
	c.store.Request(albumUID, func(img image.Image) {
		if c.albumUID != albumUID {
			return // recycled to another album while loading
		}
		c.set(img)
	})
}

func (c *coverCell) set(img image.Image) {
	c.img.Image = img
	if img == nil {
		c.img.Hide()
	} else {
		c.img.Show()
	}
	c.img.Refresh()
}

func (c *coverCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(
		canvas.NewRectangle(placeholderCoverColor),
		c.img,
	))
}
