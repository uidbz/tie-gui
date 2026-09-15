package gallery

import (
	"errors"
	"image/color"
	"io"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

type openReader struct{ opened *bool }

func (o openReader) GetReader() (io.ReadSeeker, error) {
	return nil, errors.New("not an image")
}
func (o openReader) Path() string { return "openable" }
func (o openReader) Open()        { *o.opened = true }

// ReadCustom must wire Openable readers to ImageInfo.OnOpen, and
// ChangeImage must call OnOpen instead of displaying the entry as an image
// (used for browsable directory entries in the gallery).
func TestChangeImageOnOpen(t *testing.T) {
	viewer := NewGallery(nil, nil, Config{}, nil)
	opened := false
	viewer.ReadCustom([]CustomReader{openReader{&opened}})
	if len(viewer.imageFiles) != 1 {
		t.Fatalf("imageFiles = %d, want 1", len(viewer.imageFiles))
	}
	if viewer.imageFiles[0].OnOpen == nil {
		t.Fatal("OnOpen not wired from Openable reader")
	}
	viewer.ChangeImage(viewer.imageFiles[0])
	if !opened {
		t.Error("ChangeImage did not call OnOpen")
	}
}

// ImageView must NOT implement fyne.DoubleTappable — that interface makes the
// drivers defer every single Tapped by the double-tap window (500ms on
// mobile) to disambiguate. Double-taps are instead detected manually in
// Tapped: the first tap fires OnTapped immediately, and a second tap within
// the driver's DoubleTapDelay window fires OnDoubleTapped on its own.
func TestImageViewDoubleTapManual(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	v := &Gallery{
		cache:    make(map[string]*ImageView),
		window:   win,
		platform: NewPlatform(),
	}
	info := NewImageInfoCustomReader(0, memReader{path: "p", data: testJPEGBytes(t, 64, 48, color.RGBA{10, 20, 30, 255})})
	info.Path = "p"

	var single, double int
	info.OnTapped = func() { single++ }
	info.OnDoubleTapped = func() { double++ }

	iv := v.LoadImageToCache(info)

	if _, ok := interface{}(iv).(fyne.DoubleTappable); ok {
		t.Fatal("ImageView must not satisfy fyne.DoubleTappable (drivers would defer every tap)")
	}

	// First tap fires immediately as a single tap.
	iv.Tapped(&fyne.PointEvent{})
	if single != 1 || double != 0 {
		t.Fatalf("first tap: single=%d double=%d, want 1,0", single, double)
	}

	// Second tap within the driver's double-tap window fires only the double.
	iv.Tapped(&fyne.PointEvent{})
	if single != 1 || double != 1 {
		t.Fatalf("double tap: single=%d double=%d, want 1,1", single, double)
	}

	// After the window expires a tap is single again.
	iv.lastTap = time.Now().Add(-fyne.CurrentApp().Driver().DoubleTapDelay() - time.Second)
	iv.Tapped(&fyne.PointEvent{})
	if single != 2 || double != 1 {
		t.Fatalf("after window: single=%d double=%d, want 2,1", single, double)
	}
}
