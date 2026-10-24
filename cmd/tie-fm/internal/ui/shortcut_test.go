package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// Fyne delivers window shortcuts to a focused Entry rather than the canvas, so
// the path entry forwards the filter shortcut it does not handle.
func TestShortcutEntryForwardsFilterShortcut(t *testing.T) {
	test.NewApp()
	forwarded := false
	entry := newShortcutEntry(func(s fyne.Shortcut) {
		forwarded = s.ShortcutName() == FilterShortcut.ShortcutName()
	})

	entry.TypedShortcut(FilterShortcut)
	if !forwarded {
		t.Fatal("filter shortcut was not forwarded")
	}
}
