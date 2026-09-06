// Package config loads and persists tie-audio's own settings and
// resolves the tie client configuration it depends on.
package config

import (
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

// Load reads the app config from the user config dir, returning defaults (and
// the path it would be saved to) if no config file exists yet.
func Load() (AppConfig, string) {
	cfg := Default()
	if path, err := conf.LoadFromUserConfigDir(appName, configFile, &cfg); err == nil {
		return cfg, path
	}
	path, _ := conf.PathUserConfigDir(appName, configFile)
	return Default(), path
}

// Save writes the app config to the user config dir.
func Save(cfg AppConfig) error {
	return conf.SaveToUserConfigDir(appName, configFile, &cfg)
}

// LoadTieConfig resolves the tie client config named by AppConfig.TieConfig via
// the shared tieconfig loader (Android-safe path, path-aware, normalized),
// matching tie-view. Falls back to tie defaults when no config file exists.
func LoadTieConfig(name string) tieclient.Config {
	return tieconfig.Load(name)
}
