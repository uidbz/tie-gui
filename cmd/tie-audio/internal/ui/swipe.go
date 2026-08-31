package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// edgeSwipeThreshold is the horizontal travel (px) that commits an edge swipe.
const edgeSwipeThreshold = 45

// edgeSwipe is a thin transparent strip pinned to a view's left edge that fires
// onSwipe when the user drags rightward across it — a back-gesture affordance
// for touch platforms. It is deliberately narrow so it does not cover the
// scrollable body, and implements only fyne.Draggable so taps pass through to
// the widgets beneath it. Once a drag begins on the strip, Fyne keeps sending
// the whole gesture here even after the pointer leaves the strip's bounds.
type edgeSwipe struct {
	widget.BaseWidget
	onSwipe        func()
	accumX, accumY float32
	fired          bool
}

func newEdgeSwipe(onSwipe func()) *edgeSwipe {
	e := &edgeSwipe{onSwipe: onSwipe}
	e.ExtendBaseWidget(e)
	return e
}

func (e *edgeSwipe) MinSize() fyne.Size { return fyne.NewSize(28, 0) }

func (e *edgeSwipe) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

// Dragged accumulates travel and fires once per gesture when a rightward,
// horizontal-dominant swipe passes the threshold.
func (e *edgeSwipe) Dragged(ev *fyne.DragEvent) {
	e.accumX += ev.Dragged.DX
	e.accumY += ev.Dragged.DY
	absX, absY := e.accumX, e.accumY
	if absX < 0 {
		absX = -absX
	}
	if absY < 0 {
		absY = -absY
	}
	if !e.fired && e.accumX >= edgeSwipeThreshold && absX > absY {
		e.fired = true
		if e.onSwipe != nil {
			e.onSwipe()
		}
	}
}

func (e *edgeSwipe) DragEnd() {
	e.accumX, e.accumY, e.fired = 0, 0, false
}
