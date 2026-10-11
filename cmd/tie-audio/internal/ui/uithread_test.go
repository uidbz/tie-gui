package ui

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// The Fyne test driver runs fyne.Do inline on whichever goroutine calls it.
// tie-audio does its network and playback work on background goroutines and
// hands results to the UI with fyne.Do, so under the plain test driver those
// callbacks mutate widgets concurrently with the test goroutine — data races
// the real drivers never have (they run every fyne.Do on the one UI thread).
//
// newTestApp installs a driver that models that UI thread: the test
// goroutine is the UI thread. fyne.Do from it runs inline; fyne.Do from any
// other goroutine is queued and runs on the test goroutine when it pumps the
// queue (pumpUI, waitForCond, sleepUI). Tests use these instead of bare
// time.Sleep / polling, which would never let queued UI work run.

// newTestApp replaces test.NewApp for this package's tests.
func newTestApp(t testing.TB) fyne.App {
	t.Helper()
	base := test.NewApp()
	d := &uiDriver{Driver: base.Driver(), owner: goroutineID()}
	a := &uiApp{App: base, drv: d}
	fyne.SetCurrentApp(a)
	t.Cleanup(func() {
		// Serve what is queued (unblocking DoAndWait callers), then stop:
		// later callbacks from this test's goroutines (a poll that outlived
		// the test) belong to no UI thread any more and are dropped.
		d.pump()
		d.close()
	})
	return a
}

type uiApp struct {
	fyne.App
	drv *uiDriver
}

func (a *uiApp) Driver() fyne.Driver { return a.drv }

type uiDriver struct {
	fyne.Driver
	owner uint64

	mu     sync.Mutex
	queue  []func()
	closed bool
}

func (d *uiDriver) DoFromGoroutine(f func(), wait bool) {
	if goroutineID() == d.owner {
		f()
		return
	}
	if !wait {
		d.enqueue(f)
		return
	}
	done := make(chan struct{})
	if !d.enqueue(func() { defer close(done); f() }) {
		return
	}
	<-done
}

// enqueue queues f for the UI thread; false once the test has ended.
func (d *uiDriver) enqueue(f func()) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	d.queue = append(d.queue, f)
	return true
}

// pump runs the queued UI work on the calling (UI) goroutine, including work
// queued by it, and reports how many callbacks ran.
func (d *uiDriver) pump() int {
	n := 0
	for {
		d.mu.Lock()
		q := d.queue
		d.queue = nil
		d.mu.Unlock()
		if len(q) == 0 {
			return n
		}
		for _, f := range q {
			f()
		}
		n += len(q)
	}
}

func (d *uiDriver) close() {
	d.mu.Lock()
	d.queue, d.closed = nil, true
	d.mu.Unlock()
}

// pumpUI runs the UI work queued so far (no-op without newTestApp).
func pumpUI() {
	if d, ok := fyne.CurrentApp().Driver().(*uiDriver); ok && goroutineID() == d.owner {
		d.pump()
	}
}

// sleepUI waits for d while serving the UI queue, so background work that
// reports through fyne.Do completes meanwhile.
func sleepUI(d time.Duration) {
	deadline := time.Now().Add(d)
	for {
		pumpUI()
		left := time.Until(deadline)
		if left <= 0 {
			return
		}
		time.Sleep(min(left, 2*time.Millisecond))
	}
}

// waitForCond polls cond on the UI thread (serving queued UI work between
// polls) until it holds, failing the test after 3 s.
func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pumpUI()
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	pumpUI()
	if cond() {
		return
	}
	t.Fatalf("timed out waiting for %s", what)
}

// goroutineID parses the current goroutine's id from its stack header
// ("goroutine 42 [running]:"). Test-only: identifies the UI thread.
func goroutineID() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
