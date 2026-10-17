package tieconfig

import (
	"log/slog"
	"net/url"

	"github.com/uidbz/tie/client"

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

// HostURLUserinfo returns the user:pass@ part of a configured filehost URL, or
// "" when it has none.
//
// A filehost URL with credentials in it is a hazard specific to this stack:
// the apps hand baseURL+"/"+contentHash to libmpv and pwplay as a stream URL,
// so the password ends up in the player's command line and in its log. Prefer
// a token in the URL path, a reverse proxy, or the filehost's own auth.
func HostURLUserinfo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return ""
	}
	return u.User.String()
}

// WarnHostURLUserinfo reports every configured filehost whose URL carries
// credentials. Called from Load, so it happens once at startup with the app's
// logger installed.
func WarnHostURLUserinfo(cfg client.Config) {
	for name, h := range cfg.FileHosts {
		if ui := HostURLUserinfo(h.URL); ui != "" {
			logger.Warn("filehost URL carries credentials",
				"host", name, "userinfo", ui,
				"why", "stream URLs built from this host are handed to media players")
		}
	}
}
