package mpvplayer

import (
	"log/slog"

	"github.com/uidbz/tie-gui/tielog"
)

// Logger receives mpv's own log messages. mpv is configured to report error
// level and above, and it emits them from its event-loop goroutine; printing
// them to stdout is not an option, because the Android build has no stdout and
// a desktop build launched from a launcher has no console. Defaults to discard,
// so the package never writes anywhere on its own.
//
// It lives in an untagged file so the -tags nompv build, which has no libmpv,
// still accepts the same wiring from an app's main.
var Logger = tielog.Discard()

// SetLogger installs the logger every player forwards mpv's messages to. Call
// it once at startup; it is not meant to change while running.
func SetLogger(l *slog.Logger) {
	if l != nil {
		Logger = l
	}
}
