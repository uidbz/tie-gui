package ui

import (
	"image/png"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

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

// showTestConflict opens the prompt and returns the widgets a user acts on.
func showTestConflict(t *testing.T, multiple bool) (win fyne.Window, out chan fs.ConflictResult, check *widget.Check, button map[string]*widget.Button) {
	t.Helper()
	test.NewApp()
	win = test.NewWindow(nil)
	win.Resize(fyne.NewSize(700, 600))
	out = make(chan fs.ConflictResult, 1)
	showConflictDialog(win, fs.Conflict{
		Source:   fs.Entry{Name: "photo.jpg", Size: 2048, ModTime: time.Now()},
		Existing: fs.Entry{Name: "photo.jpg", Size: 1024, ModTime: time.Now().Add(-time.Hour)},
		DestDir:  "/home/x/pictures",
		Multiple: multiple,
	}, out)

	check, button = dialogControls(win.Canvas().Overlays().Top())
	return win, out, check, button
}

// dialogControls finds the checkbox and the named buttons in a shown dialog.
func dialogControls(obj fyne.CanvasObject) (*widget.Check, map[string]*widget.Button) {
	var check *widget.Check
	buttons := map[string]*widget.Button{}
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch w := o.(type) {
		case *widget.Check:
			check = w
		case *widget.Button:
			buttons[w.Text] = w
		case *fyne.Container:
			for _, child := range w.Objects {
				walk(child)
			}
			return
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(w).Objects() {
				walk(child)
			}
		}
	}
	walk(obj)
	return check, buttons
}

func wantAnswer(t *testing.T, out chan fs.ConflictResult, want fs.ConflictAction) fs.ConflictResult {
	t.Helper()
	select {
	case res := <-out:
		if res.Action != want {
			t.Fatalf("answer = %v, want %v", res.Action, want)
		}
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("dialog delivered no answer")
	}
	return fs.ConflictResult{}
}

// TestConflictDialogApplyAll checks the checkbox turns Skip/Overwrite into the
// transfer-wide answers and disables the per-item rename controls.
func TestConflictDialogApplyAll(t *testing.T) {
	_, out, applyAll, buttons := showTestConflict(t, true)
	if applyAll == nil || applyAll.Text != "Apply to All" {
		t.Fatal("multi-item conflict dialog must offer Apply to All")
	}
	if buttons["Overwrite"].Disabled() {
		t.Fatal("Overwrite must be enabled before Apply to All")
	}
	rename := buttons["Rename"]
	if !rename.Disabled() {
		t.Fatal("Rename must be disabled while the name is unchanged")
	}

	test.Tap(applyAll)
	if !buttons["Rename"].Disabled() {
		t.Fatal("Apply to All must disable Rename")
	}
	test.Tap(buttons["Overwrite"])
	wantAnswer(t, out, fs.ConflictOverwriteAll)
}

// TestConflictDialogApplyAllSkip checks the Skip button sends Skip All when the
// checkbox is ticked.
func TestConflictDialogApplyAllSkip(t *testing.T) {
	_, out, applyAll, buttons := showTestConflict(t, true)
	test.Tap(applyAll)
	test.Tap(buttons["Skip"])
	wantAnswer(t, out, fs.ConflictSkipAll)
}

// TestConflictDialogSingleItemHidesApplyAll checks the single-item prompt has
// no Apply to All (there is nothing to apply it to).
func TestConflictDialogSingleItemHidesApplyAll(t *testing.T) {
	_, _, applyAll, buttons := showTestConflict(t, false)
	if applyAll != nil && applyAll.Visible() {
		t.Fatal("single-item conflict dialog must not show Apply to All")
	}
	if buttons["Overwrite"] == nil {
		t.Fatal("missing Overwrite button")
	}
}

// TestConflictDialogCancel checks Cancel answers with ConflictCancel.
func TestConflictDialogCancel(t *testing.T) {
	_, out, _, buttons := showTestConflict(t, true)
	test.Tap(buttons["Cancel"])
	wantAnswer(t, out, fs.ConflictCancel)
}

// TestConflictDialogDismissIsCancel checks a dialog closed without a button
// answer (window closing under the prompt) still unblocks the ops goroutine,
// as a Cancel.
func TestConflictDialogDismissIsCancel(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)
	out := make(chan fs.ConflictResult, 1)
	d := showConflictDialog(win, fs.Conflict{
		Source:   fs.Entry{Name: "photo.jpg"},
		Existing: fs.Entry{Name: "photo.jpg"},
		DestDir:  "/home/x/pictures",
	}, out)
	d.Hide()
	wantAnswer(t, out, fs.ConflictCancel)
}

// TestConflictDialogRename checks Rename stays disabled until the name is
// actually changed and then answers with the new name.
func TestConflictDialogRename(t *testing.T) {
	_, out, _, buttons := showTestConflict(t, false)

	rename := buttons["Rename"]
	test.Tap(buttons["Suggest New Name"])
	if rename.Disabled() {
		t.Fatal("Rename must enable once a new name is suggested")
	}
	test.Tap(rename)
	res := wantAnswer(t, out, fs.ConflictRename)
	if res.NewName != "photo (2).jpg" {
		t.Fatalf("NewName = %q, want photo (2).jpg", res.NewName)
	}
}
