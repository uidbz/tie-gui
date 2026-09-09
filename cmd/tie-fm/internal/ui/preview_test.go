package ui

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/gallery"
)

func writeTestJPEG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 90))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
}

func testGalleryConfig() gallery.Config {
	cfg := gallery.Config{}
	cfg.General.TileWidth = 150
	cfg.General.TileGap = 5
	cfg.General.Workers = 2
	cfg.General.ImagesPerPage = 100
	return cfg
}

// TestEntryReaderFor checks the listing → gallery reader mapping: folders,
// images and videos are shown; other files are excluded.
func TestEntryReaderFor(t *testing.T) {
	fm := &FileManager{}
	cases := []struct {
		entry fs.Entry
		want  string // "", "dir", "file", "tiefile"
	}{
		{fs.Entry{Name: "sub", Path: "/x/sub", IsDir: true}, "dir"},
		{fs.Entry{Name: "pic.jpg", Path: "/x/pic.jpg"}, "file"},
		{fs.Entry{Name: "clip.MP4", Path: "/x/clip.MP4"}, "file"},
		{fs.Entry{Name: "notes.txt", Path: "/x/notes.txt"}, ""},
		{fs.Entry{Name: "pic.jpg", Path: "tie:/x/pic.jpg", Hash: "abc123"}, "tiefile"},
		// A tie entry without a content hash (should not happen for files)
		// falls back to the plain reader.
		{fs.Entry{Name: "pic.jpg", Path: "tie:/x/pic.jpg"}, "file"},
	}
	for _, c := range cases {
		got := ""
		switch entryReaderFor(fm, c.entry).(type) {
		case nil:
		case *dirReader:
			got = "dir"
		case *tieFileReader:
			got = "tiefile"
		case *entryReader:
			got = "file"
		}
		if got != c.want {
			t.Errorf("entryReaderFor(%q) = %q, want %q", c.entry.Name, got, c.want)
		}
	}
}

// TestPreviewToggle swaps the pane between the table and the preview grid:
// the toolbar action builds the embedded gallery on first use, feeds it the
// pane's listing (non-media files excluded), and Escape on the grid leaves
// preview mode.
func TestPreviewToggle(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	dir := t.TempDir()
	writeTestJPEG(t, filepath.Join(dir, "pic.jpg"))
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, filepath.Join(dir, "sub", "inner.jpg"))
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := fs.NewRegistry(fs.NewLocalFS(), nil)
	ops := fs.NewOperations(registry)
	cfg := config.Default()
	fm := NewFileManager(dir, registry, ops, &cfg, win)

	galCfg := testGalleryConfig()
	galCfg.General.ThumbnailDir = t.TempDir()
	fm.InitPreview(galCfg, nil)

	// Preview off: the table holds the pane's center slot, keys pass through.
	if fm.content.Objects[0] != fm.table.Instance {
		t.Fatal("pane center is not the table while preview is off")
	}
	if fm.PreviewHandlesKey(&fyne.KeyEvent{Name: fyne.KeyJ}) {
		t.Fatal("keys must not be captured while preview is off")
	}

	fm.preview.toggle()
	if !fm.preview.on {
		t.Fatal("preview did not turn on")
	}
	if fm.content.Objects[0] != fm.preview.pane {
		t.Fatal("pane center is not the preview grid while preview is on")
	}
	if !fm.preview.pane.Visible() {
		t.Fatal("preview pane not visible while on")
	}
	if len(fm.preview.pane.Objects) != 1 {
		t.Fatalf("preview pane holds %d objects, want the gallery content", len(fm.preview.pane.Objects))
	}
	// The grid lists pic.jpg and sub/ (folder preview); notes.txt is excluded.
	if got := fm.preview.gallery.ImageCount(); got != 2 {
		t.Fatalf("ImageCount = %d, want 2 (pic.jpg + sub/)", got)
	}
	// Let tile placement and the thumbnail workers settle before teardown:
	// the test driver runs fyne.Do inline, so worker write-backs would
	// otherwise race the next test.
	time.Sleep(500 * time.Millisecond)

	// Escape on the grid backs out of preview mode.
	if !fm.PreviewHandlesKey(&fyne.KeyEvent{Name: fyne.KeyEscape}) {
		t.Fatal("Escape was not consumed while preview is on")
	}
	if fm.preview.on {
		t.Fatal("Escape on the grid should leave preview mode")
	}
	if fm.content.Objects[0] != fm.table.Instance {
		t.Fatal("pane center did not return to the table")
	}

	// Toggling back on re-feeds the gallery from the (unchanged) listing.
	fm.preview.toggle()
	if got := fm.preview.gallery.ImageCount(); got != 2 {
		t.Fatalf("ImageCount after re-toggle = %d, want 2", got)
	}
	time.Sleep(500 * time.Millisecond)
}

// TestPreviewTapAssociation checks that tapping an image or video tile in
// preview mode opens the configured file association instead of the built-in
// viewers, and that tiles keep their built-in behavior without one.
func TestPreviewTapAssociation(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)
	targets := captureLaunch(t)

	dir := t.TempDir()
	writeTestJPEG(t, filepath.Join(dir, "pic.jpg"))

	registry := fs.NewRegistry(fs.NewLocalFS(), nil)
	ops := fs.NewOperations(registry)
	cfg := config.Default()
	fm := NewFileManager(dir, registry, ops, &cfg, win)

	galCfg := testGalleryConfig()
	galCfg.General.ThumbnailDir = t.TempDir()
	fm.InitPreview(galCfg, nil)
	fm.preview.setOn(true)

	pic := fs.Entry{Name: "pic.jpg", Path: filepath.Join(dir, "pic.jpg")}
	tile := func(e fs.Entry) *gallery.Tile {
		r := entryReaderFor(fm, e)
		info := gallery.NewImageInfoCustomReader(0, r)
		info.Path = r.Path()
		return &gallery.Tile{Info: info}
	}

	// With an association configured, the tap launches the app with the
	// materialized (here: already local) path and does not open the image.
	cfg.SetApp("jpg", config.AppAssoc{Command: "img-open %f"})
	fm.preview.onTileTapped(tile(pic))
	if got := targets(); len(got) != 1 || got[0] != pic.Path {
		t.Fatalf("tap with association launched %v, want [%s]", got, pic.Path)
	}
	if fm.preview.gallery.ImageViewActive() {
		t.Fatal("tap with association must not open the built-in image view")
	}

	// Without an association the built-in image view opens.
	cfg.SetApp("jpg", config.AppAssoc{})
	fm.preview.onTileTapped(tile(pic))
	if got := targets(); len(got) != 1 {
		t.Fatalf("tap without association launched %v, want no new launch", got)
	}
	if !fm.preview.gallery.ImageViewActive() {
		t.Fatal("tap without association should open the built-in image view")
	}
	fm.preview.gallery.ShowGrid()

	// A video tile with an association launches the app instead of the
	// in-pane player.
	clip := fs.Entry{Name: "clip.mp4", Path: filepath.Join(dir, "clip.mp4")}
	cfg.SetApp("mp4", config.AppAssoc{Command: "mpv %f", Stream: true})
	vt := tile(clip)
	vt.Info.InputIsVideo = true
	fm.preview.onTileTapped(vt)
	if got := targets(); len(got) != 2 || got[1] != clip.Path {
		t.Fatalf("video tap with association launched %v, want the clip path appended", got)
	}

	fm.preview.setOn(false)
	time.Sleep(300 * time.Millisecond)
}
