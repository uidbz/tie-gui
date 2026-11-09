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

// SwitchPaneShortcut moves keyboard focus to the other twin-panel pane.
// Control is used explicitly (rather than the platform shortcut modifier)
// because Cmd+Tab is reserved by macOS.
var SwitchPaneShortcut fyne.Shortcut = &desktop.CustomShortcut{
	KeyName:  fyne.KeyTab,
	Modifier: fyne.KeyModifierControl,
}

// NewTabShortcut opens a tab in the active pane.
var NewTabShortcut fyne.Shortcut = &desktop.CustomShortcut{
	KeyName:  fyne.KeyT,
	Modifier: fyne.KeyModifierShortcutDefault,
}

// CloseTabShortcut closes the active pane's current tab.
var CloseTabShortcut fyne.Shortcut = &desktop.CustomShortcut{
	KeyName:  fyne.KeyW,
	Modifier: fyne.KeyModifierShortcutDefault,
}

// shortcutEntry forwards shortcuts that Entry does not consume to a callback.
// Fyne sends a window shortcut to the focused Shortcutable widget instead of
// the canvas, so without this a shortcut such as Ctrl+I is lost while the path
// entry has focus. Optional hooks handle Escape and focus loss for the
// transient path editor.
type shortcutEntry struct {
	widget.Entry
	onShortcut  func(fyne.Shortcut)
	onEscape    func()
	onFocusLost func()
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

func (e *shortcutEntry) TypedKey(ev *fyne.KeyEvent) {
	e.Entry.TypedKey(ev)
	if ev.Name == fyne.KeyEscape && e.onEscape != nil {
		e.onEscape()
	}
}

func (e *shortcutEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusLost != nil {
		e.onFocusLost()
	}
}
