package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// FilterShortcut reveals the active pane's in-page filter.
var FilterShortcut fyne.Shortcut = &desktop.CustomShortcut{
	KeyName:  fyne.KeyI,
	Modifier: fyne.KeyModifierShortcutDefault,
}

// shortcutEntry forwards shortcuts that Entry does not consume to a callback.
// Fyne sends a window shortcut to the focused Shortcutable widget instead of
// the canvas, so without this a shortcut such as Ctrl+I is lost while the path
// entry has focus.
type shortcutEntry struct {
	widget.Entry
	onShortcut func(fyne.Shortcut)
}

func newShortcutEntry(onShortcut func(fyne.Shortcut)) *shortcutEntry {
	e := &shortcutEntry{onShortcut: onShortcut}
	e.ExtendBaseWidget(e)
	return e
}

func (e *shortcutEntry) TypedShortcut(s fyne.Shortcut) {
	e.Entry.TypedShortcut(s)
	if e.onShortcut != nil {
		e.onShortcut(s)
	}
}
