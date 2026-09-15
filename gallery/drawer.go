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
	// drawerCloseDrag is how far left the user must drag the scrim to dismiss
	// the drawer, matching the gallery's own swipe threshold feel.
	drawerCloseDrag = 45
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
}

// newSidebarDrawer builds a closed drawer holding sidebar. onClose is called
// when the user dismisses it by tapping or dragging the scrim.
func newSidebarDrawer(sidebar fyne.CanvasObject, onClose func()) *sidebarDrawer {
	d := &sidebarDrawer{sidebar: sidebar, onClose: onClose}

	scrim := newDrawerScrim(func() {
		if d.onClose != nil {
			d.onClose()
		}
	})
	// An opaque background plus a tap sink: without the sink, taps landing on
	// the panel's empty space fall through to the tiles underneath and open an
	// album the user cannot even see.
	panel := container.NewStack(
		canvas.NewRectangle(theme.Color(theme.ColorNameBackground)),
		newDrawerTapSink(),
		container.NewPadded(sidebar),
	)

	d.object = container.New(&drawerLayout{}, scrim, panel)
	d.object.Hide()
	return d
}

// Open shows the drawer. Closed returns it to hidden.
func (d *sidebarDrawer) Open()        { d.object.Show() }
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
	onDismiss func()
	dragStart fyne.Position
	dragging  bool
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

func (s *drawerScrim) Dragged(e *fyne.DragEvent) {
	if !s.dragging {
		s.dragging = true
		s.dragStart = e.AbsolutePosition
	}
	if s.dragStart.X-e.AbsolutePosition.X >= drawerCloseDrag {
		s.dragging = false
		if s.onDismiss != nil {
			s.onDismiss()
		}
	}
}

func (s *drawerScrim) DragEnd() { s.dragging = false }

func (s *drawerScrim) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 128})
	return widget.NewSimpleRenderer(rect)
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
