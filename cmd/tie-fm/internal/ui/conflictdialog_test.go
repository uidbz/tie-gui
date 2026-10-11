package ui

import (
	"image/png"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

// TestConflictDialogSmoke renders the conflict dialog headless and captures it
// to catch layout/painter issues (the software painter crashes on nil colors).
func TestConflictDialogSmoke(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)
	win.Resize(fyne.NewSize(700, 600))

	out := make(chan fs.ConflictResult, 1)
	showConflictDialog(win, fs.Conflict{
		Source:   fs.Entry{Name: "photo.jpg", Size: 2048, ModTime: time.Now()},
		Existing: fs.Entry{Name: "photo.jpg", Size: 1024, ModTime: time.Now().Add(-time.Hour)},
		DestDir:  "/home/x/pictures",
		Multiple: true,
	}, out)

	img := win.Canvas().Capture()
	f, err := os.Create("/tmp/conflictdialog.png")
	if err == nil {
		png.Encode(f, img)
		f.Close()
	}
}
