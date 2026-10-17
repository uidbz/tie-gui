package gallery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// pngHeader builds a valid PNG signature plus IHDR declaring w×h, with no
// image data after it. image.DecodeConfig reads the geometry from that alone,
// which is exactly what a decompression bomb relies on: a few dozen bytes that
// promise gigabytes of pixels.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})

	var body []byte
	body = append(body, 'I', 'H', 'D', 'R')
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // colour type RGBA
	body = append(body, ihdr...)

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ihdr))) // length covers the data, not the type
	b.Write(lenBuf[:])
	b.Write(body)

	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(body))
	b.Write(crcBuf[:])
	return b.Bytes()
}

// TestDecodeRejectsBomb pins the review's §3.7: one crafted image used to be
// enough to have the OOM killer take the whole app down.
func TestDecodeRejectsBomb(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h uint32
	}{
		{"40k square", 40000, 40000},
		{"under dim limit but over pixel limit", 16000, 16000},
		{"one huge side", 100000, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bytes.NewReader(pngHeader(tc.w, tc.h))
			_, _, err := Decode(r)
			if !errors.Is(err, ErrImageTooLarge) {
				t.Fatalf("Decode(%dx%d) err = %v, want ErrImageTooLarge", tc.w, tc.h, err)
			}
		})
	}
}

func TestDecodeAcceptsNormalImage(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	got, format, err := Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decode of a normal image: %v", err)
	}
	if format != "png" {
		t.Errorf("format = %q, want png", format)
	}
	if b := got.Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Errorf("bounds = %v, want 64×48", b)
	}
}

func TestSetDecodeLimits(t *testing.T) {
	defer SetDecodeLimits(0, 0)

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 100, 100))); err != nil {
		t.Fatal(err)
	}

	SetDecodeLimits(1000, 0) // 100×100 = 10 000 px, over the cap
	if _, _, err := Decode(bytes.NewReader(buf.Bytes())); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("with a 1000 px budget: err = %v, want ErrImageTooLarge", err)
	}

	SetDecodeLimits(0, 50) // 100 px on a side, over the cap
	if _, _, err := Decode(bytes.NewReader(buf.Bytes())); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("with a 50 px side budget: err = %v, want ErrImageTooLarge", err)
	}

	SetDecodeLimits(1_000_000, 1000)
	if _, _, err := Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("with a generous budget: %v", err)
	}
}
