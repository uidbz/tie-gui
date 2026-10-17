//go:build !nompv

package mpvplayer

import (
	"sync"
	"sync/atomic"
	"testing"
)

// newPlayerOrSkip builds a player, skipping the test on a machine where libmpv
// cannot start (no audio device, restricted sandbox).
func newPlayerOrSkip(t *testing.T) *MPVPlayer {
	t.Helper()
	p, err := NewMPVPlayer("nonexistent-file-for-tests.mp4")
	if err != nil {
		t.Skip("libmpv unavailable:", err)
	}
	return p
}

// TestUseAfterCloseIsInert is the regression for review §3.9. Close freed the
// mpv core and render context and deleted the cgo handle; RenderInto, command
// and the seek/pause helpers then dereferenced a nil (or freed) handle. The
// property getters checked for nil and the others did not, so whether closing
// a video window crashed the app depended on which call lost the race with the
// next repaint.
func TestUseAfterCloseIsInert(t *testing.T) {
	p := newPlayerOrSkip(t)
	p.Close()

	// Every entry point that used to reach C unconditionally.
	p.RenderInto(0, 64, 64)
	p.command("quit")
	p.SeekTo(1)
	p.Play()
	p.Pause()
	p.TogglePause()
	p.SetOnUpdate(func() {})
	_ = p.Aspect()
	_ = p.Position()
	_ = p.Duration()
	_ = p.IsPaused()
	_ = p.AtEOF()
	_ = p.NeedsPaint()

	if !p.Closed() {
		t.Error("Closed() is false after Close")
	}
	// ensureRender must not create a render context on a terminated core.
	if err := p.ensureRender(); err == nil {
		t.Error("ensureRender succeeded on a closed player")
	}
	p.Close() // idempotent
}

// TestCloseRacesTheCommandPath exercises the lock that keeps Close from
// freeing the mpv core while another goroutine is inside a cgo call on it.
// RenderInto is not driven here: it needs a GL context, and ensureRender asks
// GLFW for the native display, which a headless test has none of. The
// command/property path takes the same lock and is the one the UI hammers —
// seek, pause, play — from click handlers while a window close is in flight.
// Run it under -race.
func TestCloseRacesTheCommandPath(t *testing.T) {
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		p := newPlayerOrSkip(t)
		wg.Add(3)
		go func() { defer wg.Done(); p.SeekTo(0.5); p.Pause(); p.Play(); p.TogglePause() }()
		go func() { defer wg.Done(); _ = p.Position(); _ = p.Duration(); _ = p.IsPaused(); _ = p.Aspect() }()
		go func() { defer wg.Done(); p.Close() }()
	}
	wg.Wait()
	// Whichever order the goroutines won in, every player must end up closed
	// and every later call inert.
}

// fireUpdateCallback mirrors goRenderUpdate's body: mpv invokes that export on
// one of its own threads, and this is how the test reaches the same code
// without a GL context to make mpv call it.
func fireUpdateCallback(p *MPVPlayer) {
	p.needsPaint.Store(true)
	if fn := p.onUpdate.Load(); fn != nil {
		(*fn)()
	}
}

// TestSetOnUpdateRace: the callback is installed from the UI goroutine and
// called on an mpv thread, so it must not be a plain field.
func TestSetOnUpdateRace(t *testing.T) {
	p := newPlayerOrSkip(t)
	defer p.Close()

	var fired atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			p.SetOnUpdate(func() { fired.Add(1) })
			p.SetOnUpdate(nil)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			fireUpdateCallback(p)
		}
	}()
	wg.Wait()
}
