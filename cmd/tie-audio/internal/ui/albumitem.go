package ui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// AudioAlbumItem is a cover-wall tile backed by a tie audio album. It is not
// image content (GetReader fails); tapping it opens the album's track list
// (Openable), and its tile aspect is forced square (DimensionProvider). The
// tile thumbnail is supplied by coverThumbnailer, not by GetReader.
type AudioAlbumItem struct {
	album data.Album
	open  func(data.Album)
}

// GetReader implements gallery.CustomReader. An album is a collection, not a
// displayable image, so decoding always fails; the tile shows a cover via the
// gallery Thumbnailer instead.
func (a *AudioAlbumItem) GetReader() (io.ReadSeeker, error) {
	return nil, errors.New("audio album is not image content: " + a.album.UID)
}

// Path implements gallery.CustomReader: a stable identifier for caching.
func (a *AudioAlbumItem) Path() string { return a.album.UID }

// DisplayName implements gallery.DisplayNamer: the album label under the tile.
func (a *AudioAlbumItem) DisplayName() string { return a.album.Display() }

// Subtitle implements gallery.Subtitler: the artist shown on a second line
// below the title. Empty when unknown, which collapses the tile to one line.
func (a *AudioAlbumItem) Subtitle() string { return a.album.Artist }

// Dimensions implements gallery.DimensionProvider: square tiles for covers.
func (a *AudioAlbumItem) Dimensions() (int, int) { return 1000, 1000 }

// Open implements gallery.Openable: tapping the tile opens the album view.
func (a *AudioAlbumItem) Open() { a.open(a.album) }

// coverThumbnailer implements gallery.Thumbnailer, supplying album-cover
// thumbnails from the filehost. Covers are decoded and rescaled to the tile
// size; albums without a stored cover get a neutral placeholder square. It
// reads the live session through the page so a settings change takes effect.
type coverThumbnailer struct {
	page      *browsePage
	tileWidth int
}

func (t *coverThumbnailer) GetThumbnail(info *gallery.ImageInfo) (io.ReadSeeker, error) {
	item, ok := info.CustomReader.(*AudioAlbumItem)
	if !ok {
		return nil, errors.New("not an audio album item")
	}
	info.ThumbnailIsScaled = true
	rs, err := t.page.session.CoverReader(item.album)
	if err != nil {
		return placeholderCover(t.tileWidth * 2), nil
	}
	decoded, _, err := gallery.Decode(rs)
	if err != nil {
		return placeholderCover(t.tileWidth * 2), nil
	}
	scaled := gallery.ScaleImage(decoded, t.tileWidth*2)
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90}); err != nil {
		return placeholderCover(t.tileWidth * 2), nil
	}
	return bytes.NewReader(buf.Bytes()), nil
}

// placeholderCover returns a plain square JPEG used for albums with no stored
// cover thumbnail.
func placeholderCover(size int) io.ReadSeeker {
	if size <= 0 {
		size = 300
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := color.RGBA{R: 48, G: 48, B: 56, A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, fill)
		}
	}
	buf := &bytes.Buffer{}
	_ = jpeg.Encode(buf, img, &jpeg.Options{Quality: 80})
	return bytes.NewReader(buf.Bytes())
}
