package ui

import (
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

// NewConflictResolver returns the fs.Operations conflict resolver: it shows a
// Dolphin-style "already exists" prompt on win (the layout and options of
// KIO's RenameDialog, which Dolphin shows on copy/move conflicts) and blocks
// the calling goroutine — the ops engine, never the UI goroutine — until the
// user decides.
func NewConflictResolver(win fyne.Window) func(fs.Conflict) fs.ConflictResult {
	return func(c fs.Conflict) fs.ConflictResult {
		res := make(chan fs.ConflictResult, 1)
		fyne.Do(func() { showConflictDialog(win, c, res) })
		return <-res
	}
}

// conflictVerdict is the comparison line between the source and destination
// panels: recency first, then size, then the identical hint.
func conflictVerdict(src, ex fs.Entry) string {
	if !src.ModTime.IsZero() && !ex.ModTime.IsZero() && !src.ModTime.Equal(ex.ModTime) {
		if src.ModTime.After(ex.ModTime) {
			return "The source is more recent."
		}
		return "The source is older."
	}
	if !src.IsDir && !ex.IsDir {
		switch {
		case src.Size < ex.Size:
			return fmt.Sprintf("The source is smaller by %s.", humanBytes(ex.Size-src.Size))
		case src.Size > ex.Size:
			return fmt.Sprintf("The source is bigger by %s.", humanBytes(src.Size-ex.Size))
		default:
			return "The files seem identical."
		}
	}
	return ""
}

func conflictColumn(e fs.Entry) fyne.CanvasObject {
	name := widget.NewLabel(e.Name)
	name.Truncation = fyne.TextTruncateEllipsis
	size := ""
	if !e.IsDir {
		size = "Size: " + humanBytes(e.Size)
	}
	date := ""
	if !e.ModTime.IsZero() {
		date = "Date: " + e.ModTime.Format("2006-01-02 15:04:05")
	}
	return container.NewVBox(name, widget.NewLabel(size), widget.NewLabel(date))
}

// showConflictDialog builds the prompt. It mirrors KIO's RenameDialog:
// sentence + question, a Source/Destination comparison with a verdict line, a
// Rename row with "Suggest New Name", an "Apply to All" checkbox on
// multi-item transfers, and the button row Skip / Rename / Overwrite (Write
// Into for directory merges) / Cancel. Exactly one answer is delivered, even
// when the dialog is dismissed by other means (the window closing, or a
// programmatic Hide): dismissal means Cancel. It returns the dialog so tests
// can dismiss it.
func showConflictDialog(win fyne.Window, c fs.Conflict, out chan<- fs.ConflictResult) *dialog.CustomDialog {
	isDir := c.Source.IsDir && c.Existing.IsDir

	title := "File Already Exists"
	if isDir {
		title = "Folder Already Exists"
	}

	sentence := fmt.Sprintf("A file named '%s' already exists in '%s'.", c.Existing.Name, c.DestDir)
	if isDir {
		sentence = fmt.Sprintf("A folder named '%s' already exists in '%s'.", c.Existing.Name, c.DestDir)
	}
	question := "Would you like to overwrite the destination?"
	if isDir {
		question = fmt.Sprintf("Would you like to merge the contents of '%s' into '%s'?", c.Source.Name, c.Existing.Name)
	}

	srcTitle := widget.NewLabelWithStyle("Source", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	dstTitle := widget.NewLabelWithStyle("Destination", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	compare := container.NewGridWithColumns(2,
		container.NewVBox(srcTitle, conflictColumn(c.Source)),
		container.NewVBox(dstTitle, conflictColumn(c.Existing)))

	var rows []fyne.CanvasObject
	msg := widget.NewLabel(sentence + "\n\n" + question)
	msg.Wrapping = fyne.TextWrapWord
	rows = append(rows, msg, compare)
	if v := conflictVerdict(c.Source, c.Existing); v != "" {
		rows = append(rows, widget.NewLabel(v))
	}

	// Rename row: entry prefilled with the conflicting name, plus a button
	// that jumps to the next free "name (N)" suggestion.
	renameEntry := widget.NewEntry()
	renameEntry.SetText(c.Existing.Name)
	suggest := widget.NewButton("Suggest New Name", func() {
		renameEntry.SetText(fs.UniqueName(c.Existing.Name, map[string]bool{c.Existing.Name: true}))
	})
	rows = append(rows, container.NewBorder(nil, nil, widget.NewLabel("Rename:"), suggest, renameEntry))

	applyAll := widget.NewCheck("Apply to All", nil)
	applyAll.Hide()
	var d *dialog.CustomDialog
	var once sync.Once
	answer := func(res fs.ConflictResult) {
		once.Do(func() { out <- res })
	}
	send := func(res fs.ConflictResult) {
		// Answer before hiding: Hide() runs the closed hook, whose Cancel
		// answer must lose the race.
		answer(res)
		d.Hide()
	}

	skipBtn := widget.NewButton("Skip", func() {
		if applyAll.Checked {
			send(fs.ConflictResult{Action: fs.ConflictSkipAll})
		} else {
			send(fs.ConflictResult{Action: fs.ConflictSkip})
		}
	})
	renameBtn := widget.NewButton("Rename", func() {
		send(fs.ConflictResult{Action: fs.ConflictRename, NewName: strings.TrimSpace(renameEntry.Text)})
	})
	overwriteLabel := "Overwrite"
	if isDir {
		overwriteLabel = "Write Into"
	}
	overwriteBtn := widget.NewButton(overwriteLabel, func() {
		if applyAll.Checked {
			send(fs.ConflictResult{Action: fs.ConflictOverwriteAll})
		} else {
			send(fs.ConflictResult{Action: fs.ConflictOverwrite})
		}
	})
	cancelBtn := widget.NewButton("Cancel", func() {
		send(fs.ConflictResult{Action: fs.ConflictCancel})
	})

	// renameAllowed: Apply to All is transfer-wide, so a per-item rename makes
	// no sense once it is ticked (like KIO). An unchanged or empty name is not
	// a rename either — without this, pressing Rename with the prefilled name
	// silently overwrote the existing item.
	renameAllowed := func() bool {
		if applyAll.Checked {
			return false
		}
		name := strings.TrimSpace(renameEntry.Text)
		return name != "" && name != c.Existing.Name
	}
	updateRename := func() {
		if applyAll.Checked {
			renameEntry.Disable()
			suggest.Disable()
		} else {
			renameEntry.Enable()
			suggest.Enable()
		}
		if renameAllowed() {
			renameBtn.Enable()
		} else {
			renameBtn.Disable()
		}
	}
	// Like Dolphin, Apply to All makes a per-item rename meaningless: the
	// rename controls disable and Skip/Overwrite apply to every conflict.
	applyAll.OnChanged = func(bool) { updateRename() }
	if c.Multiple {
		applyAll.Show()
	}
	renameEntry.OnChanged = func(string) { updateRename() }
	updateRename()

	rows = append(rows, applyAll)
	d = dialog.NewCustomWithoutButtons(title, container.NewVBox(rows...), win)
	// Any close that is not one of the four buttons (e.g. the window closing
	// under a pending prompt) is a Cancel: the ops goroutine must never be left
	// blocked waiting for an answer that can no longer arrive.
	d.SetOnClosed(func() {
		answer(fs.ConflictResult{Action: fs.ConflictCancel})
	})
	d.SetButtons([]fyne.CanvasObject{skipBtn, renameBtn, overwriteBtn, cancelBtn})
	d.Show()
	return d
}
