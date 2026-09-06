// Package config loads and persists tie-audio's own settings and
// resolves the tie client configuration it depends on.
package config

import (
	"os"
	"path/filepath"

	"github.com/uidbz/conf"
	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tieconfig"
)

const (
	appName    = "tie-audio"
	configFile = "config.toml"

	// DefaultTieCollection is tie-audio's own default profile: the
	// [Collections] entry it binds when the user hasn't picked one yet, so
	// images and sound can live in different profiles without the apps
	// fighting over the tie config's shared DefaultCollection.
	DefaultTieCollection = "audio"
)

// AppConfig is persisted to the user config dir
// (e.g. ~/.config/tie-audio/config.toml).
type AppConfig struct {
	// PwplayServer is the base URL of the pwplay-server that performs playback.
	PwplayServer string
	// TieConfig selects which tie client config to load: "" = tie's default
	// user config, a value containing '/' = that file path, otherwise a named
	// config under the tie app config dir.
	TieConfig string
	// TieCollection is this app's own tie collection selection (a
	// [Collections] entry name in the loaded tie config). Empty means "not
	// chosen yet": DefaultTieCollection is preferred at startup, then the tie
	// config's DefaultCollection.
	TieCollection string
	// FileHost optionally selects a filehost by name; empty uses the tie
	// config's default.
	FileHost string
	// AlbumColumns is the ordered set of visible track-table column keys
	// (e.g. "trackno", "title", "artist", "album", "year", "duration"). Empty
	// means "use the built-in default set and order".
	AlbumColumns []string
	// QueueColumns is the ordered set of visible column keys for the play queue
	// table, configured independently of AlbumColumns. Empty means the default.
	QueueColumns []string
}

// Default returns the built-in defaults used before any config file exists.
func Default() AppConfig {
	return AppConfig{PwplayServer: "http://localhost:8080"}
}

// savePath caches the path Load resolved, so Save never re-derives it via
// os.UserConfigDir — which fails on Android (no $HOME/$XDG_CONFIG_HOME,
// surfaced as an "xdg" error) every time settings are saved. FILESDIR (the
// app's internal files dir, set by Fyne's native code) takes priority, then
// the path Load resolved on this platform.
var savePath string

// resolveSavePath returns the file settings are written to: $FILESDIR on
// Android, else the path Load already resolved (or the user config dir).
func resolveSavePath() string {
	if savePath != "" {
		return savePath
	}
	if d := os.Getenv("FILESDIR"); d != "" {
		savePath = filepath.Join(d, appName, configFile)
		return savePath
	}
	path, _ := conf.PathUserConfigDir(appName, configFile)
	savePath = path
	return savePath
}

// Load reads the app config, preferring $FILESDIR on Android (so it sits in
// the app's internal files dir, which survives reinstalls and is the only
// writable location there), then the user config dir. Returns the config
// and the path it was loaded from (or would be saved to).
func Load() (AppConfig, string) {
	path := resolveSavePath()
	if d := os.Getenv("FILESDIR"); d != "" {
		cfg := Default()
		if err := conf.ReadConfig(path, &cfg); err == nil {
			return cfg, path
		}
	}
	cfg := Default()
	if p, err := conf.LoadFromUserConfigDir(appName, configFile, &cfg); err == nil {
		return cfg, p
	}
	return Default(), path
}

// Save writes the app config back to the file Load resolved (Android-safe:
// never re-derives a path via os.UserConfigDir).
func Save(cfg AppConfig) error {
	if d := os.Getenv("FILESDIR"); d != "" {
		return conf.WriteConfig(filepath.Join(d, appName, configFile), &cfg)
	}
	return conf.WriteConfig(resolveSavePath(), &cfg)
}

// LoadTieConfig resolves the tie client config named by AppConfig.TieConfig via
// the shared tieconfig loader (Android-safe path, path-aware, normalized),
// matching tie-view. Falls back to tie defaults when no config file exists.
func LoadTieConfig(name string) tieclient.Config {
	return tieconfig.Load(name)
}
