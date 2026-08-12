package gallery

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

const (
	leftTop = iota
	leftMiddle
	leftBottom
	rightTop
	rightMiddle
	rightBottom
	topMiddle
	bottomMiddle
)

type Region struct {
	widget.BaseWidget

	parent    *ImageView
	pos       fyne.Position
	color     color.Color
	startPos  fyne.Position // drag start position for calculating deltas
	dragStart bool          // true while a drag is in progress
}

type RegionRenderer struct {
	region       *Region
	rect         *canvas.Rectangle
	leftTop      *CornerKnob
	leftMiddle   *CornerKnob
	leftBottom   *CornerKnob
	rightTop     *CornerKnob
	rightMiddle  *CornerKnob
	rightBottom  *CornerKnob
	topMiddle    *CornerKnob
	bottomMiddle *CornerKnob
}

type CornerKnob struct {
	widget.BaseWidget
	parent         *Region
	cornerPosition uint

	corner *canvas.Circle
}

func NewCornerKnob(cornerPosition uint, parent *Region) *CornerKnob {
	ck := &CornerKnob{
		cornerPosition: cornerPosition,
		parent:         parent,
	}
	ck.corner = canvas.NewCircle(color.RGBA{0, 0, 255, 255})

	ck.ExtendBaseWidget(ck)
	return ck
}

func (ck *CornerKnob) Dragged(drag *fyne.DragEvent) {
	size := ck.parent.Size()
	pos := ck.parent.Position()
	dragPos := drag.AbsolutePosition.Subtract(ck.parent.parent.Position()) //subtract image position
	switch ck.cornerPosition {
	case leftTop:
		ck.parent.Resize(size.Subtract(fyne.NewSize(drag.Dragged.Components())))
		ck.parent.Move(dragPos)
	case leftMiddle:
		ck.parent.Resize(size.Subtract(fyne.NewSize(drag.Dragged.DX, 0)))
		ck.parent.Move(fyne.NewPos(dragPos.X, pos.Y))
	case leftBottom:
		ck.parent.Resize(size.Subtract(fyne.NewSize(drag.Dragged.DX, -drag.Dragged.DY)))
		ck.parent.Move(fyne.NewPos(dragPos.X, pos.Y))
	case rightTop:
		ck.parent.Resize(size.Subtract(fyne.NewSize(-drag.Dragged.DX, drag.Dragged.DY)))
		ck.parent.Move(fyne.NewPos(pos.X, dragPos.Y))
	case rightMiddle:
		ck.parent.Resize(size.Subtract(fyne.NewSize(-drag.Dragged.DX, 0)))
		ck.parent.Move(fyne.NewPos(pos.X, pos.Y))
	case rightBottom:
		ck.parent.Resize(size.Subtract(fyne.NewSize(-drag.Dragged.DX, -drag.Dragged.DY)))
		ck.parent.Move(fyne.NewPos(pos.X, pos.Y))
	case topMiddle:
		ck.parent.Resize(size.Subtract(fyne.NewSize(0, drag.Dragged.DY)))
		ck.parent.Move(fyne.NewPos(pos.X, dragPos.Y))
	case bottomMiddle:
		ck.parent.Resize(size.Subtract(fyne.NewSize(0, -drag.Dragged.DY)))
		ck.parent.Move(fyne.NewPos(pos.X, pos.Y))
	}
}

func (ck *CornerKnob) DragEnd() {
}

func NewRegion(color color.Color, parent *ImageView) *Region {
	r := &Region{color: color, parent: parent}
	r.ExtendBaseWidget(r)

	return r
}

func (r *CornerKnob) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.corner)
}

// Refresh causes this object to be redrawn in it's current state
func (r *RegionRenderer) Destroy() {
}

func (r *Region) CreateRenderer() fyne.WidgetRenderer {
	ren := &RegionRenderer{
		region:       r,
		rect:         canvas.NewRectangle(color.Transparent),
		leftTop:      NewCornerKnob(leftTop, r),
		leftMiddle:   NewCornerKnob(leftMiddle, r),
		leftBottom:   NewCornerKnob(leftBottom, r),
		rightTop:     NewCornerKnob(rightTop, r),
		rightMiddle:  NewCornerKnob(rightMiddle, r),
		rightBottom:  NewCornerKnob(rightBottom, r),
		topMiddle:    NewCornerKnob(topMiddle, r),
		bottomMiddle: NewCornerKnob(bottomMiddle, r),
	}
	ren.rect.StrokeColor = color.RGBA{255, 20, 20, 255}
	ren.rect.StrokeWidth = 5
	return ren
}

func (ren *RegionRenderer) Refresh() {
	ren.rect.Refresh()
}

func (ren *RegionRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{ren.rect, ren.leftTop, ren.leftMiddle, ren.leftBottom, ren.rightTop, ren.rightMiddle, ren.rightBottom, ren.topMiddle, ren.bottomMiddle}
}

func (ren *RegionRenderer) MinSize() fyne.Size {
	return fyne.NewSize(100, 100)
}

func (ren *RegionRenderer) Layout(s fyne.Size) {
	ren.rect.Resize(s)
	size := float32(15.0)
	halfSize := size / 2
	knobSize := fyne.NewSize(size, size)
	ren.leftTop.Resize(knobSize)
	ren.leftMiddle.Resize(knobSize)
	ren.leftBottom.Resize(knobSize)
	ren.rightTop.Resize(knobSize)
	ren.rightMiddle.Resize(knobSize)
	ren.rightBottom.Resize(knobSize)
	ren.topMiddle.Resize(knobSize)
	ren.bottomMiddle.Resize(knobSize)

	ren.leftTop.Move(fyne.NewPos(-halfSize, -halfSize))
	ren.leftMiddle.Move(fyne.NewPos(-halfSize, s.Height/2-halfSize))
	ren.leftBottom.Move(fyne.NewPos(-halfSize, s.Height-halfSize))
	ren.rightTop.Move(fyne.NewPos(-halfSize+s.Width, -halfSize))
	ren.rightMiddle.Move(fyne.NewPos(-halfSize+s.Width, s.Height/2-halfSize))
	ren.rightBottom.Move(fyne.NewPos(-halfSize+s.Width, s.Height-halfSize))
	ren.topMiddle.Move(fyne.NewPos(-halfSize+(s.Width/2), -halfSize))
	ren.bottomMiddle.Move(fyne.NewPos(-halfSize+(s.Width/2), s.Height-halfSize))
}

func (r *Region) Dragged(drag *fyne.DragEvent) {
	if !r.dragStart {
		r.startPos = drag.PointEvent.Position
		r.dragStart = true
	}

	// r.pos = drag.AbsolutePosition.Subtract(r.startPos)
	pos := drag.AbsolutePosition.Subtract(r.startPos).Subtract(r.parent.Position())
	if pos.X+r.Size().Width > r.parent.Size().Width {
		pos.X = r.parent.Size().Width - r.Size().Width
	}
	if pos.Y+r.Size().Height > r.parent.Size().Height {
		pos.Y = r.parent.Size().Height - r.Size().Height
	}
	if pos.X < 0 {
		pos.X = 0
	}
	if pos.Y < 0 {
		pos.Y = 0
	}

	r.pos = pos
	r.Move(r.pos)
	r.Refresh()
}

func (r *Region) DragEnd() {
	r.dragStart = false
}
