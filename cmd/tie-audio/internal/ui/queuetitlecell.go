package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// queueTitleCell is the desktop queue table's title-column cell. One widget
// serves both row kinds of the grouped playlist — a plain track title, or an
// album header with the cover, the album name and the artist·year·count line
// (matching the compact playlist's album headers) — so a table cell recycled
// between row kinds never needs recreation, only a set(). The unused variant
// is hidden, mirroring queueListRow.
type queueTitleCell struct {
	widget.BaseWidget

	trackLabel *widget.Label

	cover       *coverCell
	albumTitle  *widget.Label
	albumDetail *widget.Label
	headerBox   *fyne.Container
}

func newQueueTitleCell(covers *coverStore) *queueTitleCell {
	c := &queueTitleCell{}

	c.trackLabel = widget.NewLabel("")
	c.trackLabel.Truncation = fyne.TextTruncateEllipsis

	c.cover = newCoverCell(covers)
	c.albumTitle = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	c.albumTitle.Truncation = fyne.TextTruncateEllipsis
	c.albumDetail = widget.NewLabel("")
	c.albumDetail.Truncation = fyne.TextTruncateEllipsis
	c.headerBox = container.NewBorder(nil, nil,
		container.NewGridWrap(fyne.NewSize(groupCoverSize, groupCoverSize), c.cover),
		nil,
		container.NewVBox(c.albumTitle, c.albumDetail),
	)

	c.ExtendBaseWidget(c)
	return c
}

// set renders one display row, showing the matching variant.
func (c *queueTitleCell) set(row queueRow) {
	if row.kind == queueRowAlbum {
		c.headerBox.Show()
		c.trackLabel.Hide()
		c.albumTitle.SetText(row.title)
		c.albumDetail.SetText(row.subtitle)
		c.cover.show(row.albumUID)
		return
	}
	c.headerBox.Hide()
	c.trackLabel.Show()
	c.trackLabel.SetText(row.title)
}

func (c *queueTitleCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(c.headerBox, c.trackLabel))
}
