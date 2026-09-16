package gallery

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// pullViewer returns a minimal Gallery whose scroll container has a real
// viewport (100x100 over 1000px of content), so offsets clamp to [0, 900]
// and the overlay's scroll forwarding can be observed.
func pullViewer() *Gallery {
	v := &Gallery{}
	content := container.NewGridWrap(fyne.NewSize(100, 1000), widget.NewLabel("x"))
	v.scroll = container.NewScroll(content)
	v.scroll.Resize(fyne.NewSize(100, 100))
	return v
}

func dragDY(o *gridSwipeOverlay, dx, dy float32) {
	o.Dragged(&fyne.DragEvent{Dragged: fyne.Delta{DX: dx, DY: dy}})
}

// A downward drag at the top of the grid charges a refresh and fires it once
// per gesture past the threshold; the next gesture may fire again.
func TestGridSwipePullRefresh(t *testing.T) {
	test.NewApp()
	v := pullViewer()
	fired := 0
	v.OnPullRefresh = func() { fired++ }
	o := newGridSwipeOverlay(v)

	dragDY(o, 0, 30)
	if fired != 0 {
		t.Fatalf("pull fired below the threshold (%d px)", 30)
	}
	dragDY(o, 0, pullRefreshThreshold) // overshoot in one event
	if fired != 1 {
		t.Fatalf("pull fired %d times at the threshold, want 1", fired)
	}
	dragDY(o, 0, 50) // same gesture: must not fire twice
	if fired != 1 {
		t.Fatalf("pull fired %d times in one gesture, want 1", fired)
	}
	if got := v.scroll.Offset.Y; got != 0 {
		t.Errorf("scroll offset = %v after a pull, want 0 (nothing to scroll)", got)
	}

	o.DragEnd()
	dragDY(o, 0, pullRefreshThreshold)
	if fired != 2 {
		t.Errorf("pull fired %d times after a second gesture, want 2", fired)
	}
}

// A downward drag while scrolled away from the top keeps scrolling the grid
// and never charges a refresh.
func TestGridSwipePullOnlyAtTop(t *testing.T) {
	test.NewApp()
	v := pullViewer()
	fired := 0
	v.OnPullRefresh = func() { fired++ }
	o := newGridSwipeOverlay(v)

	v.scroll.ScrollToOffset(fyne.NewPos(0, 500))
	dragDY(o, 0, 2*pullRefreshThreshold)
	if fired != 0 {
		t.Errorf("pull fired %d times while scrolled down, want 0", fired)
	}
	if got := v.scroll.Offset.Y; got >= 500 {
		t.Errorf("scroll offset = %v after a downward drag, want it scrolled back up", got)
	}
}

// An upward drag at the top scrolls down into the grid (and is not a pull),
// and a pull charge does not survive a drag that scrolled the grid.
func TestGridSwipePullResetByScrolling(t *testing.T) {
	test.NewApp()
	v := pullViewer()
	fired := 0
	v.OnPullRefresh = func() { fired++ }
	o := newGridSwipeOverlay(v)

	dragDY(o, 0, pullRefreshThreshold-10) // charge just below the threshold
	dragDY(o, 0, -40)                     // scroll down into the grid: charge resets
	if got := v.scroll.Offset.Y; got != 40 {
		t.Fatalf("scroll offset = %v after an upward drag, want 40", got)
	}
	// Back at the top, the half-charged pull must start from zero.
	v.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	dragDY(o, 0, 20)
	if fired != 0 {
		t.Errorf("pull fired %d times from a reset charge, want 0", fired)
	}
}

// With no OnPullRefresh set, a downward drag at the top is harmless (it
// forwards to the scroller, which clamps at 0).
func TestGridSwipePullWithoutCallback(t *testing.T) {
	test.NewApp()
	v := pullViewer()
	o := newGridSwipeOverlay(v)

	dragDY(o, 0, 2*pullRefreshThreshold)
	o.DragEnd()
	if got := v.scroll.Offset.Y; got != 0 {
		t.Errorf("scroll offset = %v, want 0", got)
	}
}
