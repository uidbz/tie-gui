package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// Star icons for the 1-5 rating widget. Gold = filled, grey = empty.
//
//go:embed star-filled.png
var starFilledPNG []byte

//go:embed star-empty.png
var starEmptyPNG []byte

var (
	starFilledRes = fyne.NewStaticResource("star-filled", starFilledPNG)
	starEmptyRes  = fyne.NewStaticResource("star-empty", starEmptyPNG)
)

// starRating is a horizontal row of five clickable stars backing a 1-5 rating.
//
// Clicking star N sets the rating to N (filling stars 1..N); clicking the star
// that is already the current rating clears it back to 0 (unrated). On desktop,
// hovering previews the rating up to the star under the cursor and leaving
// restores the committed value. OnChanged fires with the new rating (0 = cleared,
// 1..5 otherwise) only on user interaction — SetRating updates the display
// silently so loading a stored value never writes back.
type starRating struct {
	widget.BaseWidget
	cells     []*starCell
	selected  int // committed rating: 0 = unrated, else 1..5
	starSize  float32
	OnChanged func(rating int)
}

func newStarRating(starSize float32, onChanged func(rating int)) *starRating {
	sr := &starRating{starSize: starSize, OnChanged: onChanged}
	sr.cells = make([]*starCell, 5)
	for i := range sr.cells {
		sr.cells[i] = newStarCell(sr, i+1)
	}
	sr.ExtendBaseWidget(sr)
	return sr
}

func (sr *starRating) CreateRenderer() fyne.WidgetRenderer {
	objs := make([]fyne.CanvasObject, len(sr.cells))
	for i, c := range sr.cells {
		objs[i] = c
	}
	return widget.NewSimpleRenderer(container.NewHBox(objs...))
}

// SetRating updates the committed rating and the star display without invoking
// OnChanged. Use it when reflecting a value loaded from the store.
func (sr *starRating) SetRating(rating int) {
	if rating < 0 {
		rating = 0
	}
	if rating > 5 {
		rating = 5
	}
	sr.selected = rating
	sr.paint(rating)
}

// Rating returns the committed rating (0 = unrated).
func (sr *starRating) Rating() int { return sr.selected }

// paint fills stars 1..n and empties the rest.
func (sr *starRating) paint(n int) {
	for _, c := range sr.cells {
		c.setFilled(c.idx <= n)
	}
}

// setRatingFromUser commits a user-chosen rating and notifies OnChanged.
func (sr *starRating) setRatingFromUser(rating int) {
	sr.selected = rating
	sr.paint(rating)
	if sr.OnChanged != nil {
		sr.OnChanged(rating)
	}
}

// starCell is one clickable, hoverable star in a starRating.
type starCell struct {
	widget.BaseWidget
	parent *starRating
	idx    int // 1-based position
	img    *canvas.Image
	filled bool
}

func newStarCell(parent *starRating, idx int) *starCell {
	c := &starCell{parent: parent, idx: idx}
	c.img = canvas.NewImageFromResource(starEmptyRes)
	c.img.FillMode = canvas.ImageFillContain
	c.ExtendBaseWidget(c)
	return c
}

func (c *starCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.img)
}

func (c *starCell) MinSize() fyne.Size {
	s := c.parent.starSize
	return fyne.NewSize(s, s)
}

func (c *starCell) setFilled(filled bool) {
	if c.filled == filled && c.img.Resource != nil {
		return
	}
	c.filled = filled
	if filled {
		c.img.Resource = starFilledRes
	} else {
		c.img.Resource = starEmptyRes
	}
	c.img.Refresh()
}

// Tapped sets the rating to this star's position, or clears it when tapping the
// star that already holds the current rating.
func (c *starCell) Tapped(_ *fyne.PointEvent) {
	if c.parent.selected == c.idx {
		c.parent.setRatingFromUser(0)
		return
	}
	c.parent.setRatingFromUser(c.idx)
}

// MouseIn/MouseMoved/MouseOut implement desktop.Hoverable for a live preview
// of the rating under the cursor, reverting to the committed value on leave.
func (c *starCell) MouseIn(_ *desktop.MouseEvent)    { c.parent.paint(c.idx) }
func (c *starCell) MouseMoved(_ *desktop.MouseEvent) {}
func (c *starCell) MouseOut()                        { c.parent.paint(c.parent.selected) }
