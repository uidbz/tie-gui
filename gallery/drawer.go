package gallery

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Drawer geometry. The panel takes most of a phone's width but stops short of
// covering it entirely, so the scrim is always tappable to dismiss, and is
// capped so a wide window does not get an absurdly wide panel.
const (
	drawerWidthFraction = 0.85
	drawerMaxWidth      = 360
	// drawerCloseDrag is how far left the user must drag the scrim or the panel
	// to dismiss the drawer, matching the gallery's own swipe threshold feel.
	drawerCloseDrag = 45
	// drawerSwipeExtent is how far the panel's invisible swipe scroller can
	// travel. It only has to exceed the widest drawer, so a leftward drag
	// always has room to move it.
	drawerSwipeExtent = 4096
)

// sidebarDrawer presents Gallery.Sidebar as a slide-over panel above the
// gallery grid instead of an HSplit pane. On a phone the split leaves neither
// side usable: the sidebar's tag list needs ~200px and the grid needs the rest.
//
// The drawer is a single container stacked over the grid and hidden when
// closed, so opening and closing it is a Show/Hide — it never rebuilds the
// window content (which would reset the grid's scroll position).
type sidebarDrawer struct {
	object  *fyne.Container
	sidebar fyne.CanvasObject
	onClose func()
	// swipe catches a leftward drag anywhere over the panel; see drawerSwipe.
	swipe *drawerSwipe
}

// newSidebarDrawer builds a closed drawer holding sidebar. onClose is called
// when the user dismisses it by tapping or dragging the scrim, or by swiping
// left across the panel itself.
func newSidebarDrawer(sidebar fyne.CanvasObject, onClose func()) *sidebarDrawer {
	d := &sidebarDrawer{sidebar: sidebar, onClose: onClose}

	dismiss := func() {
		if d.onClose != nil {
			d.onClose()
		}
	}
	scrim := newDrawerScrim(dismiss)
	d.swipe = newDrawerSwipe(dismiss)
	// An opaque background plus a tap sink: without the sink, taps landing on
	// the panel's empty space fall through to the tiles underneath and open an
	// album the user cannot even see. The swipe scroller sits above the sink
	// (so it, not the sink, takes drags on empty space) but below the
	// background and the sidebar, so its scrollbar is hidden and the sidebar's
	// own scrollers keep priority.
	panel := container.NewStack(
		newDrawerTapSink(),
		d.swipe.scroll,
		canvas.NewRectangle(theme.Color(theme.ColorNameBackground)),
		container.NewPadded(sidebar),
	)

	d.object = container.New(&drawerLayout{}, scrim, panel)
	d.object.Hide()
	return d
}

// Open shows the drawer. Closed returns it to hidden.
func (d *sidebarDrawer) Open() {
	d.swipe.reset()
	d.object.Show()
}
func (d *sidebarDrawer) Close()       { d.object.Hide() }
func (d *sidebarDrawer) IsOpen() bool { return d.object.Visible() }

// drawerLayout gives the scrim the whole area and the panel a left-anchored
// slice of it. A Stack cannot express this (it sizes every child to the full
// area), and a Border would size the panel to its content's MinSize, which for
// a tag list is far narrower than a usable drawer.
type drawerLayout struct{}

func (l *drawerLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) < 2 {
		return
	}
	scrim, panel := objs[0], objs[1]
	scrim.Resize(size)
	scrim.Move(fyne.NewPos(0, 0))

	width := size.Width * drawerWidthFraction
	if width > drawerMaxWidth {
		width = drawerMaxWidth
	}
	if min := panel.MinSize().Width; width < min {
		width = min
	}
	panel.Resize(fyne.NewSize(width, size.Height))
	panel.Move(fyne.NewPos(0, 0))
}

// MinSize is zero: the drawer is an overlay and must not inflate the minimum
// size of the view it covers.
func (l *drawerLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, 0)
}

// drawerScrim is the dimmed area beside the panel. It swallows taps and drags
// that would otherwise reach the grid behind it, and dismisses the drawer on a
// tap or a leftward drag (the gesture that would push the panel off-screen).
type drawerScrim struct {
	widget.BaseWidget
	onDismiss      func()
	accumX, accumY float32
	fired          bool
}

func newDrawerScrim(onDismiss func()) *drawerScrim {
	s := &drawerScrim{onDismiss: onDismiss}
	s.ExtendBaseWidget(s)
	return s
}

func (s *drawerScrim) Tapped(_ *fyne.PointEvent) {
	if s.onDismiss != nil {
		s.onDismiss()
	}
}

// Dragged accumulates its own travel rather than reading AbsolutePosition:
// the mobile driver reports the per-event delta but leaves AbsolutePosition
// at zero, which used to make the drag-dismiss a no-op on touch devices.
func (s *drawerScrim) Dragged(e *fyne.DragEvent) {
	s.accumX += e.Dragged.DX
	s.accumY += e.Dragged.DY
	if s.fired {
		return
	}
	absX, absY := s.accumX, s.accumY
	if absX < 0 {
		absX = -absX
	}
	if absY < 0 {
		absY = -absY
	}
	if absX > absY && -s.accumX >= drawerCloseDrag {
		s.fired = true
		if s.onDismiss != nil {
			s.onDismiss()
		}
	}
}

func (s *drawerScrim) DragEnd() { s.accumX, s.accumY, s.fired = 0, 0, false }

func (s *drawerScrim) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 128})
	return widget.NewSimpleRenderer(rect)
}

// drawerSwipe dismisses the drawer on a leftward drag anywhere over the panel,
// not just on the scrim. It is a transparent horizontal scroll container laid
// under the sidebar: on touch platforms Fyne pairs the horizontal half of a
// drag with the vertical scroller at the same point, so a swipe that starts on
// the tag list still reaches this scroller while vertical drags keep scrolling
// that list. Its offset is the swipe travel; the panel background hides it.
type drawerSwipe struct {
	scroll    *container.Scroll
	onDismiss func()
	fired     bool
}

func newDrawerSwipe(onDismiss func()) *drawerSwipe {
	d := &drawerSwipe{onDismiss: onDismiss}
	d.scroll = container.NewHScroll(newDrawerSwipeContent())
	d.scroll.OnScrolled = d.scrolled
	return d
}

// scrolled dismisses once the leftward travel reaches the threshold.
func (d *drawerSwipe) scrolled(pos fyne.Position) {
	if d.fired || pos.X < drawerCloseDrag {
		return
	}
	d.fired = true
	if d.onDismiss != nil {
		d.onDismiss()
	}
}

// reset returns the scroller to the start so the next opening begins fresh.
func (d *drawerSwipe) reset() {
	d.fired = false
	d.scroll.ScrollToOffset(fyne.NewPos(0, 0))
}

// drawerSwipeContent gives the swipe scroller something wider than the drawer
// to scroll, so a drag actually moves its offset. It draws nothing.
type drawerSwipeContent struct {
	widget.BaseWidget
	bg *canvas.Rectangle
}

func newDrawerSwipeContent() *drawerSwipeContent {
	c := &drawerSwipeContent{bg: canvas.NewRectangle(color.Transparent)}
	c.ExtendBaseWidget(c)
	return c
}

func (c *drawerSwipeContent) MinSize() fyne.Size {
	return fyne.NewSize(drawerSwipeExtent, 1)
}

func (c *drawerSwipeContent) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.bg)
}

// drawerTapSink is an invisible tappable/draggable area that absorbs pointer
// events landing on the panel's empty space, so they don't reach the grid.
type drawerTapSink struct {
	widget.BaseWidget
}

func newDrawerTapSink() *drawerTapSink {
	s := &drawerTapSink{}
	s.ExtendBaseWidget(s)
	return s
}

func (s *drawerTapSink) Tapped(_ *fyne.PointEvent) {}
func (s *drawerTapSink) Dragged(_ *fyne.DragEvent) {}
func (s *drawerTapSink) DragEnd()                  {}

func (s *drawerTapSink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
