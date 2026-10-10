package ui

import (
	"testing"
	"time"
)

// Window-close cleanup must never block the UI goroutine indefinitely: a
// wedged backend Close froze tie-audio on exit.
func TestCloseWithinBoundsAHungClose(t *testing.T) {
	block := make(chan struct{})
	defer close(block)

	start := time.Now()
	if closeWithin(50*time.Millisecond, "test", func() { <-block }) {
		t.Error("closeWithin reported success for a close that never returned")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("closeWithin took %v, want ~50ms", d)
	}
	if !closeWithin(time.Second, "test", func() {}) {
		t.Error("closeWithin reported a timeout for an immediate close")
	}
}
