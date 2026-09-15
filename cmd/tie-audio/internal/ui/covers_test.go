package ui

import (
	"errors"
	"image"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// fakeCovers replaces coverStore's network+decode path so the caching policy
// can be tested without a filehost. It records how many times each album was
// actually resolved, which is the whole point of the store.
type fakeCovers struct {
	mu    sync.Mutex
	calls map[string]int
	// results maps album UID to what decode should return.
	results map[string]struct {
		img image.Image
		err error
	}
}

func newFakeCovers() *fakeCovers {
	return &fakeCovers{
		calls: map[string]int{},
		results: map[string]struct {
			img image.Image
			err error
		}{},
	}
}

func (f *fakeCovers) set(uid string, img image.Image, err error) {
	f.results[uid] = struct {
		img image.Image
		err error
	}{img, err}
}

func (f *fakeCovers) decode(uid string) (image.Image, error) {
	f.mu.Lock()
	f.calls[uid]++
	f.mu.Unlock()
	r := f.results[uid]
	return r.img, r.err
}

func (f *fakeCovers) count(uid string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[uid]
}

// newTestCoverStore builds a store whose decode step is the fake's.
func newTestCoverStore(f *fakeCovers) *coverStore {
	s := newCoverStore(nil)
	s.decodeFn = f.decode
	return s
}

func testImage() image.Image { return image.NewRGBA(image.Rect(0, 0, 2, 2)) }

func TestCoverStoreCachesHitsAndMisses(t *testing.T) {
	f := newFakeCovers()
	f.set("has", testImage(), nil)
	f.set("none", nil, data.ErrNoCover)
	s := newTestCoverStore(f)

	if img := s.Get("has"); img == nil {
		t.Fatal("Get(has) = nil, want an image")
	}
	if img := s.Get("has"); img == nil {
		t.Fatal("second Get(has) = nil, want the cached image")
	}
	if n := f.count("has"); n != 1 {
		t.Errorf("resolved %d times, want 1 (second call must be cached)", n)
	}

	// A coverless album is a settled answer: it must not be re-probed, or every
	// row of a coverless album would hit the network on every repaint.
	if img := s.Get("none"); img != nil {
		t.Fatal("Get(none) returned an image, want nil")
	}
	s.Get("none")
	if n := f.count("none"); n != 1 {
		t.Errorf("coverless album resolved %d times, want 1", n)
	}
}

func TestCoverStoreRetriesAfterFetchFailure(t *testing.T) {
	f := newFakeCovers()
	f.set("flaky", nil, errors.New("server down"))
	s := newTestCoverStore(f)

	if img := s.Get("flaky"); img != nil {
		t.Fatal("Get during outage returned an image")
	}
	// The server coming back must be noticed: a transient failure is not cached.
	f.set("flaky", testImage(), nil)
	if img := s.Get("flaky"); img == nil {
		t.Fatal("Get after recovery = nil, want the now-available cover")
	}
	if n := f.count("flaky"); n != 2 {
		t.Errorf("resolved %d times, want 2 (failure must not be cached)", n)
	}
}

func TestCoverStoreLookupReportsSettledState(t *testing.T) {
	f := newFakeCovers()
	f.set("a", testImage(), nil)
	s := newTestCoverStore(f)

	if _, ok := s.Lookup("a"); ok {
		t.Fatal("Lookup before resolution reported settled")
	}
	s.Get("a")
	img, ok := s.Lookup("a")
	if !ok || img == nil {
		t.Fatalf("Lookup after Get = (%v, %v), want (image, true)", img, ok)
	}
	// An empty album UID is settled-and-coverless, so callers can paint the
	// placeholder without waiting.
	if img, ok := s.Lookup(""); !ok || img != nil {
		t.Errorf(`Lookup("") = (%v, %v), want (nil, true)`, img, ok)
	}
}

func TestCoverStoreRequestDeduplicatesConcurrentAsks(t *testing.T) {
	// Request delivers its callbacks through fyne.Do, which needs a running app.
	test.NewApp()

	f := newFakeCovers()
	f.set("a", testImage(), nil)
	s := newTestCoverStore(f)

	// Hold the decode until every caller has asked, so all three Requests are
	// in flight at once — the case a table full of one album's rows produces.
	release := make(chan struct{})
	s.decodeFn = func(uid string) (image.Image, error) {
		<-release
		return f.decode(uid)
	}

	var done int32
	for i := 0; i < 3; i++ {
		s.Request("a", func(image.Image) { atomic.AddInt32(&done, 1) })
	}
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&done) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&done); got != 3 {
		t.Fatalf("%d of 3 callbacks ran", got)
	}
	if n := f.count("a"); n != 1 {
		t.Errorf("resolved %d times for 3 concurrent asks, want 1", n)
	}
}

func TestCoverStoreEvictsOldest(t *testing.T) {
	f := newFakeCovers()
	s := newTestCoverStore(f)

	for i := 0; i < coverLimit+5; i++ {
		uid := string(rune('a'+i%26)) + string(rune('0'+i/26))
		f.set(uid, testImage(), nil)
		s.Get(uid)
	}
	s.mu.Lock()
	n := len(s.imgs)
	s.mu.Unlock()
	if n > coverLimit {
		t.Fatalf("cache holds %d entries, want at most %d", n, coverLimit)
	}
}
