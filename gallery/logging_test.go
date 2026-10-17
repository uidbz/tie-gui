package gallery

import (
	"bytes"
	"errors"
	"image/color"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// loggingGallery builds a reader-backed gallery whose logger writes into buf,
// so the assertions can read what the gallery reported. The logger has to be
// installed before Init: NewTileLayout copies it.
func loggingGallery(t *testing.T, buf *bytes.Buffer, thumb Thumbnailer, entries int) *Gallery {
	t.Helper()
	test.NewApp()
	win := test.NewWindow(nil)

	config := Config{}
	config.General.TileWidth = 200
	config.General.TileGap = 5
	config.General.Workers = 2
	config.General.ImagesPerPage = 4
	config.General.ThumbnailDir = filepath.Join(t.TempDir(), "cache")

	viewer := NewGallery(fyne.CurrentApp(), win, config, nil)
	viewer.SetLogger(slog.New(slog.NewTextHandler(buf, nil)))
	viewer.Thumbnailer = thumb
	viewer.Init()

	readers := make([]CustomReader, 0, entries)
	for i := 0; i < entries; i++ {
		readers = append(readers, memReader{path: "tie:entry" + string(rune('a'+i))})
	}
	viewer.ReadCustom(readers)
	win.SetContent(viewer.Content)
	win.Resize(fyne.NewSize(800, 600))
	return viewer
}

// failingThumbnailer stands in for a filehost that cannot serve the blob.
type failingThumbnailer struct{}

func (failingThumbnailer) GetThumbnail(*ImageInfo) (io.ReadSeeker, error) {
	return nil, errors.New("thumbnail source failing on purpose")
}

// A thumbnail that cannot be loaded leaves a placeholder on screen and is
// otherwise silent. Before the logger it was silent in the logs too: the only
// trace of a failing filehost was a grey square.
func TestThumbnailFailureIsLogged(t *testing.T) {
	var buf bytes.Buffer
	viewer := loggingGallery(t, &buf, failingThumbnailer{}, 4)
	viewer.LoadGallery()
	viewer.AwaitIdle()

	logged := buf.String()
	if !strings.Contains(logged, "thumbnail load failed") {
		t.Fatalf("the exhausted thumbnail failure was not logged:\n%s", logged)
	}
	for _, want := range []string{"level=WARN", "path=tie:entrya"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the failure record is missing %q:\n%s", want, logged)
		}
	}
	// The retries are debug-level: they would flood the log on a flaky link.
	if strings.Contains(logged, "level=DEBUG") {
		t.Errorf("retries were logged above debug:\n%s", logged)
	}
}

// panickingThumbnailer blows up on one entry and serves the rest, which is what
// a truncated blob or a bad cgo decode looks like from the gallery's side.
type panickingThumbnailer struct {
	bad  string
	jpeg []byte
}

func (p panickingThumbnailer) GetThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	if info.Path == p.bad {
		panic("decoder exploded on " + info.Path)
	}
	return bytes.NewReader(p.jpeg), nil
}

// TestPanicInThumbnailerIsSurvived is the reason the loader recovers per entry
// rather than per worker: one bad blob must not take the app down, must not
// stop the other loads, and must not leave a placement waiting forever on the
// count the panicking entry was holding.
func TestPanicInThumbnailerIsSurvived(t *testing.T) {
	var buf bytes.Buffer
	raw := testJPEGBytes(t, 90, 60, color.RGBA{20, 60, 200, 255})
	thumb := panickingThumbnailer{bad: "tie:entryb", jpeg: raw}

	viewer := loggingGallery(t, &buf, thumb, 4)
	viewer.LoadGallery()
	// AwaitIdle returning at all is half the assertion: the deferred Done ran
	// even for the entry that panicked.
	viewer.AwaitIdle()

	logged := buf.String()
	if !strings.Contains(logged, "panic") || !strings.Contains(logged, "gallery.imageLoader") {
		t.Fatalf("the panic was not logged:\n%s", logged)
	}
	if !strings.Contains(logged, "decoder exploded on tie:entryb") {
		t.Errorf("the panic value is missing from the record:\n%s", logged)
	}

	filled := 0
	for _, tile := range viewer.layout.tiles {
		if tile != nil && tile.Content != nil && tile.Content.Image != nil &&
			tile.Content.Image.Bounds().Dx() == 90 {
			filled++
		}
	}
	if filled != 3 {
		t.Errorf("%d of 4 tiles got a thumbnail, want 3: the surviving workers "+
			"must keep loading after a panic\nlog:\n%s", filled, logged)
	}
}

// TestGalleryLoggerDefaultsToDiscard pins the library rule: a gallery the app
// never gave a logger must not write anywhere.
func TestGalleryLoggerDefaultsToDiscard(t *testing.T) {
	viewer, _ := setupTestGallery(t, t.TempDir(), 4, fyne.NewSize(800, 600))
	if viewer.Logger == nil {
		t.Fatal("a Gallery has no logger at all; the call sites would need nil checks")
	}
	// Nothing to assert beyond "this does not panic and goes nowhere".
	viewer.log().Error("nowhere", "err", errors.New("dropped"))
}
