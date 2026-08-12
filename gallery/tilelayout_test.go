package gallery

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

func testTile() *Tile {
	img := canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 100, 150)))
	t := &Tile{Content: img}
	t.width = 100
	t.height = 150
	return t
}

// PlaceTiles resets layout.tiles synchronously while grid objects are
// replaced via fyne.Do, so Layout can transiently see object/tile lists of
// different lengths. It must not panic (regression: index out of range).
func TestLayoutToleratesTileObjectMismatch(t *testing.T) {
	config := Config{}
	config.General.TileWidth = 300
	config.General.TileGap = 5
	layout := &TileLayout{config: config}

	newObjects := func(n int) []fyne.CanvasObject {
		objects := make([]fyne.CanvasObject, n)
		for i := range objects {
			objects[i] = canvas.NewRectangle(color.Black)
		}
		return objects
	}

	size := fyne.NewSize(1000, 1000)

	// More objects than tiles (stale grid during a gallery reload).
	layout.tiles = []*Tile{testTile(), testTile(), testTile()}
	layout.Layout(newObjects(5), size)

	// More tiles than objects.
	layout.tiles = []*Tile{testTile(), testTile(), testTile(), testTile(), testTile()}
	layout.Layout(newObjects(3), size)

	// Matching lists.
	objects := newObjects(5)
	layout.Layout(objects, size)

	// Empty grid.
	layout.tiles = nil
	layout.Layout(nil, size)
}

type errReader struct{}

func (errReader) GetReader() (io.ReadSeeker, error) {
	return bytes.NewReader([]byte("this is not an image")), nil
}
func (errReader) Path() string { return "err" }

// NewImageView on an undecodable image must fall back to the placeholder so
// fyneImage is never nil (regression: renderer Layout panicked on
// fyneImage.Resize after a failed decode).
func TestNewImageViewBadImage(t *testing.T) {
	info := NewImageInfoCustomReader(0, errReader{})
	iv := NewImageView(info, fyne.NewSize(100, 100), true, nil, nil, NewPlatform())
	if iv.fyneImage == nil {
		t.Fatal("fyneImage is nil after failed decode")
	}
	iv.CreateRenderer().(*ImageViewRenderer).Layout(fyne.NewSize(100, 100))
}
