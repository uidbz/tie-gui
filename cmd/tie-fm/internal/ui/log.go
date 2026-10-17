package ui

import (
	"log/slog"

	"github.com/uidbz/tie-gui/tielog"
)

// log is this package's logger: file operations that failed, a preview gallery
// that could not load a thumbnail, a device that would not mount. It defaults
// to discard so the package never writes to stdout or stderr on its own;
// main installs the app logger with SetLogger.
var log = tielog.Discard()

// SetLogger installs the logger this package reports to. Call it once at
// startup.
func SetLogger(l *slog.Logger) {
	if l != nil {
		log = l
	}
}
