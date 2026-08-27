package gallery

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// gridSwipeOverlay is a transparent full-bleed widget laid over the gallery grid
// on mobile. It turns a horizontal-dominant drag past swipeThreshold into an
// OnSwipeLeft/OnSwipeRight callback (letting the app page to an adjacent view),
// and forwards every other drag's vertical component to the gallery scroller so
// grid scrolling still works. It implements only fyne.Draggable, so tile taps
// (dispatched on a separate path) still reach the tiles beneath it.
type gridSwipeOverlay struct {
	widget.BaseWidget
	viewer         *Gallery
	bg             *canvas.Rectangle
	accumX, accumY float32
	fired          bool
}

func newGridSwipeOverlay(v *Gallery) *gridSwipeOverlay {
	o := &gridSwipeOverlay{viewer: v, bg: canvas.NewRectangle(color.Transparent)}
	o.ExtendBaseWidget(o)
	return o
}

func (o *gridSwipeOverlay) Dragged(ev *fyne.DragEvent) {
	o.accumX += ev.Dragged.DX
	o.accumY += ev.Dragged.DY
	absX, absY := o.accumX, o.accumY
	if absX < 0 {
		absX = -absX
	}
	if absY < 0 {
		absY = -absY
	}
	if absX > absY && absX >= swipeThreshold {
		if !o.fired {
			o.fired = true
			if o.accumX < 0 {
				if o.viewer.OnSwipeLeft != nil {
					o.viewer.OnSwipeLeft()
				}
			} else if o.viewer.OnSwipeRight != nil {
				o.viewer.OnSwipeRight()
			}
		}
		o.accumX, o.accumY = 0, 0
		return
	}
	// Vertical-dominant (or sub-threshold) drag: keep the grid scrolling by
	// forwarding the delta to the scroller (mirrors dirSwipeOverlay).
	if v := o.viewer; v != nil && v.scroll != nil && ev.Dragged.DY != 0 {
		v.scroll.ScrollToOffset(fyne.NewPos(v.scroll.Offset.X, v.scroll.Offset.Y-ev.Dragged.DY))
	}
}

func (o *gridSwipeOverlay) DragEnd() { o.accumX, o.accumY, o.fired = 0, 0, false }

func (o *gridSwipeOverlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(o.bg)
}
