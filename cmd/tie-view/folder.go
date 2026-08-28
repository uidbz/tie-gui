package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
)

// folderIcon renders a simple folder glyph as a PNG of size x size pixels,
// used as the gallery thumbnail for tie directories so they are clearly
// marked as directories.
func folderIcon(size int) io.ReadSeeker {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	body := color.RGBA{250, 205, 105, 255} // folder yellow
	edge := color.RGBA{160, 120, 45, 255}  // darker outline
	inner := color.RGBA{255, 232, 170, 255}

	m := size / 8 // margin
	w := size - 2*m
	tabW := w / 2
	tabH := size / 10
	top := m + size/6 // body top; the tab sits above it

	rect := func(x0, y0, x1, y1 int, c color.Color) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), &image.Uniform{c}, image.Point{}, draw.Src)
	}

	// Tab, then body, each filled with a thin outline.
	rect(m, top-tabH, m+tabW, top, edge)
	rect(m+2, top-tabH+2, m+tabW-2, top, body)
	rect(m, top, m+w, size-m, edge)
	rect(m+2, top+2, m+w-2, size-m-2, body)
	// A lighter inner rectangle suggests papers inside the folder.
	rect(m+w/6, top+w/8, m+w-w/6, top+w/8+w/12, inner)

	buf := &bytes.Buffer{}
	if err := png.Encode(buf, img); err != nil {
		// PNG encoding of an in-memory image cannot realistically fail;
		// return an empty (decodable) 1x1 image as a last resort.
		buf.Reset()
		_ = png.Encode(buf, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	}
	return bytes.NewReader(buf.Bytes())
}
