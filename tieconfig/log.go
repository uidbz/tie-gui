package tieconfig

import (
	"log/slog"

	"github.com/uidbz/tie-gui/tielog"
)

// logger is where non-fatal config problems are reported: an unreadable config
// file falls back to the built-in defaults, but the user should still learn
// that happened.
//
// It defaults to discard because a library may not write to stdout or stderr —
// the Android builds have no stdout, and a desktop app launched from a launcher
// has no stderr either. Apps install their tielog logger with SetLogger.
var logger = tielog.Discard()

// SetLogger installs the logger this package reports to. Call it once at
// startup, before the first Load; it is not meant to change while running.
func SetLogger(l *slog.Logger) {
	if l != nil {
		logger = l
	}
}
