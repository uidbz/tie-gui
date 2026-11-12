package main

// TestSiteCapture renders imgview's real gallery through the headless Fyne
// driver for the tiedb.com screenshot pipeline. It only runs when
// SITE_CAPTURE_DIR is set:
//
//	XDG_CONFIG_HOME=$(mktemp -d) \
//	SITE_DEMO_IMAGES=$PWD/demo-images \
//	SITE_CAPTURE_DIR=/tmp/shots \
//	go test ./cmd/imgview -run TestSiteCapture -count=1
//
// The capture is rendered at 2x, so a 1280x800 window produces a 2560x1600 PNG.

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/tiestyle"
)

func TestSiteCapture(t *testing.T) {
	out := os.Getenv("SITE_CAPTURE_DIR")
	if out == "" {
		t.Skip("set SITE_CAPTURE_DIR to render site captures")
	}
	dir := os.Getenv("SITE_DEMO_IMAGES")
	if dir == "" {
		t.Skip("set SITE_DEMO_IMAGES to a folder of JPEGs")
	}
	scene := os.Getenv("SITE_CAPTURE_SCENES")

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	app := test.NewApp()
	t.Cleanup(app.Quit)
	tiestyle.Apply(app)
	win := app.NewWindow("imgview")
	if c, ok := win.Canvas().(interface{ SetScale(float32) }); ok {
		c.SetScale(2)
	}
	cfg := gallery.LoadConfig(win, "")
	cfg.General.ThumbnailDir = filepath.Join(t.TempDir(), "thumbs")
	cfg.General.ImagesPerPage = 16

	var viewer *gallery.Gallery
	viewer = gallery.NewGallery(app, win, cfg, func(tile *gallery.Tile) {
		switch {
		case tile.Info.ShowArchive:
			viewer.ShowImageArchive(tile.Info.FullPath)
		case tile.Info.InputIsDir:
			viewer.ShowImageDir(filepath.Dir(tile.Info.Path))
		default:
			viewer.ChangeImage(tile.Info)
		}
	})
	viewer.Init()
	win.Canvas().SetOnTypedKey(viewer.KeyPress)

	viewer.ReadImageDir(dir, nil)
	win.SetContent(viewer.Content)
	win.Resize(fyne.NewSize(1280, 800))
	viewer.LoadGallery()
	viewer.CreateView()
	win.SetContent(viewer.Content)
	win.Resize(fyne.NewSize(1280, 800))

	// Give the thumbnail workers time to land. The software driver has no
	// compositor to poke us, so poll for tiles whose images are no longer the
	// 50x50 loading placeholder.
	waitForGallery(t, win, 15*time.Second)

	if scene == "" || scene == "gallery" {
		viewer.ToggleLabels()
		capture(t, win, filepath.Join(out, "gallery-"+captureTheme()+".png"))
	}

	if scene == "" || scene == "image" {
		var first *gallery.Tile
		walkTiles(win.Content(), func(tile *gallery.Tile) {
			if first == nil && tile != nil && tile.Info != nil {
				first = tile
			}
		})
		if first == nil {
			t.Fatal("no tile found")
		}
		viewer.ChangeImage(first.Info)
		time.Sleep(2500 * time.Millisecond)
		capture(t, win, filepath.Join(out, "image-"+captureTheme()+".png"))
		viewer.ToggleInfoOverlay()
		time.Sleep(700 * time.Millisecond)
		capture(t, win, filepath.Join(out, "info-"+captureTheme()+".png"))
	}
}

func captureTheme() string {
	if v := os.Getenv("TIE_THEME"); v != "" {
		return v
	}
	return "dark"
}

func walkTiles(o fyne.CanvasObject, fn func(*gallery.Tile)) {
	switch v := o.(type) {
	case *gallery.Tile:
		fn(v)
	case *fyne.Container:
		for _, c := range v.Objects {
			walkTiles(c, fn)
		}
	case fyne.Widget:
		if r := test.WidgetRenderer(v); r != nil {
			for _, c := range r.Objects() {
				walkTiles(c, fn)
			}
		}
	}
}

func totalTiles(o fyne.CanvasObject) (total, ready int) {
	walkTiles(o, func(t *gallery.Tile) {
		total++
		if t.Content != nil && t.Content.Image != nil {
			if b := t.Content.Image.Bounds(); b.Dx() != 50 || b.Dy() != 50 {
				ready++
			}
		}
	})
	return
}

func waitForGallery(t *testing.T, win fyne.Window, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		total, ready := totalTiles(win.Content())
		if total > 0 && ready >= total {
			break
		}
		if time.Now().After(deadline) {
			t.Logf("gallery not fully loaded: %d/%d tiles", ready, total)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond) // let the tile label pass settle
}

func capture(t *testing.T, win fyne.Window, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Force the software driver to run the layout pass it skips on refresh.
	win.Content().Refresh()
	time.Sleep(300 * time.Millisecond)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("capture panicked: %v", r)
			}
		}()
		img := win.Canvas().Capture()
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}()
	fmt.Println("wrote", path)
}
