package gallery

import (
	"errors"
	"io"
	"testing"
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
	viewer := NewViewer(nil, nil, Config{}, nil)
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
