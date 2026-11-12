package gallery

import (
	"image"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// zoomTheme overrides only the text size, like a zoomed/larger-font UI.
type zoomTheme struct {
	fyne.Theme
	text float32
}

func (t zoomTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText {
		return t.text
	}
	return t.Theme.Size(name)
}

// TestLabelHeightFollowsThemeTextSize is the regression test for a zoomed UI
// pushing tile names past their label box: the reserved label height must track
// the theme's text metric, not a fixed 22px.
func TestLabelHeightFollowsThemeTextSize(t *testing.T) {
	app := test.NewApp()

	textH := fyne.MeasureText("Ag", theme.TextSize(), fyne.TextStyle{}).Height
	if got := labelHeight(); got < textH+theme.InnerPadding() {
		t.Fatalf("labelHeight = %v, smaller than the %v text plus its top padding at the default theme", got, textH)
	}

	app.Settings().SetTheme(zoomTheme{Theme: theme.DefaultTheme(), text: 30})
	zoomedText := fyne.MeasureText("Ag", 30, fyne.TextStyle{}).Height
	if got := labelHeight(); got < zoomedText+theme.InnerPadding() {
		t.Fatalf("labelHeight = %v, smaller than the %v text plus its top padding at a zoomed text size", got, zoomedText)
	}
	if labelHeight() <= textH {
		t.Fatalf("labelHeight did not grow with the text size: %v", labelHeight())
	}
}

// TestTileLabelFitsWithZoomedTheme checks a zoomed/larger-font theme keeps the
// name text inside the label row, below the thumbnail: the old fixed 22px label
// box drew the text into the next row, over that tile's folder badge.
func TestTileLabelFitsWithZoomedTheme(t *testing.T) {
	app := test.NewApp()
	app.Settings().SetTheme(zoomTheme{Theme: theme.DefaultTheme(), text: 30})

	tile := &Tile{Content: canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 100, 50)))}
	tile.nameLabel = widget.NewLabel("zoomed-name.jpg")
	tile.nameLabel.Truncation = fyne.TextTruncateEllipsis
	r := tile.CreateRenderer()

	// The grid gives a tile rowH + labelHeight: a 150px row plus the label.
	size := fyne.NewSize(200, 150+labelHeight())
	r.Layout(size)

	textH := fyne.MeasureText("zoomed-name.jpg", 30, fyne.TextStyle{}).Height
	textBottom := tile.nameLabel.Position().Y + theme.InnerPadding() + textH
	if textBottom > size.Height {
		t.Fatalf("label text ends at %v, past the tile bottom %v", textBottom, size.Height)
	}
	if imgBottom := tile.Content.Position().Y + tile.Content.Size().Height; imgBottom > tile.nameLabel.Position().Y {
		t.Fatalf("thumbnail ends at %v, past the label top %v", imgBottom, tile.nameLabel.Position().Y)
	}
}

// TestShortTileLabelStaysInside checks a tile shorter than its label area
// shrinks the label instead of drawing it into the neighbouring row.
func TestShortTileLabelStaysInside(t *testing.T) {
	test.NewApp()

	tile := &Tile{Content: canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 100, 50)))}
	tile.nameLabel = widget.NewLabel("a-long-file-name.jpg")
	tile.nameLabel.Truncation = fyne.TextTruncateEllipsis
	r := tile.CreateRenderer()

	for _, h := range []float32{200, 100, 10, 1} {
		size := fyne.NewSize(100, h)
		r.Layout(size)
		bottom := tile.nameLabel.Position().Y + tile.nameLabel.Size().Height
		if bottom > size.Height {
			t.Fatalf("tile height %v: label bottom %v exceeds the tile", h, bottom)
		}
		if tile.Content.Size().Height < 0 {
			t.Fatalf("tile height %v: image height %v is negative", h, tile.Content.Size().Height)
		}
	}
}
