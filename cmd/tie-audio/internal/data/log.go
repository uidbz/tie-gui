package data

import (
	"log/slog"

	"github.com/uidbz/tie-gui/tielog"
)

// log is this package's logger: cover uploads that failed, queries that came
// back wrong. It defaults to discard so the package never writes to stdout or
// stderr on its own; main installs the app logger with SetLogger.
var log = tielog.Discard()

// SetLogger installs the logger this package reports to. Call it once at
// startup.
func SetLogger(l *slog.Logger) {
	if l != nil {
		log = l
	}
}
