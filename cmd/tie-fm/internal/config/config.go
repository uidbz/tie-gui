// Package config persists tie-fm's own settings (sidebar bookmarks and the
// path to the tie client config to use) as a TOML file via github.com/uidbz/conf.
// It is separate from the tie *client* config, which describes how to reach a
// tie server.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/uidbz/conf"
	"github.com/uidbz/tie/client"
)

const (
	appName  = "tie-fm"
	fileName = "config.toml"
)

// Bookmark is one entry in the favorites sidebar.
type Bookmark struct {
	Label string
	Path  string
}

// Config is tie-fm's persisted settings.
type Config struct {
	// TieConfig is the path to the tie client config file to load. Empty means
	// use the embedded default (a local tie server; see DefaultTieConfig).
	TieConfig string
	// Bookmarks populate the favorites sidebar.
	Bookmarks []Bookmark
	// FileApps maps a lowercase file extension (without the leading dot) to the
	// command used to open files of that type, overriding the xdg-open default.
	// The command may contain a "%f" placeholder for the file path; if absent
	// the path is appended as the final argument.
	FileApps map[string]string

	path string // where this config was loaded from / will be saved back to
}

// Path reports the file this config was loaded from (or will be written to).
func (c Config) Path() string { return c.path }

// ExtKey normalizes a filename to its file-association key: the lowercase
// extension without the leading dot ("" when the name has no extension).
func ExtKey(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

// AppFor returns the configured open command for the given filename's
// extension, or "" when none is set.
func (c Config) AppFor(name string) string {
	if c.FileApps == nil {
		return ""
	}
	return c.FileApps[ExtKey(name)]
}

// SetApp associates the command with the extension key (a bare extension such
// as "pdf"). An empty command removes the association.
func (c *Config) SetApp(ext, command string) {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if ext == "" {
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		delete(c.FileApps, ext)
		return
	}
	if c.FileApps == nil {
		c.FileApps = map[string]string{}
	}
	c.FileApps[ext] = command
}

// Default returns the built-in tie-fm config: home + tie bookmarks and no
// explicit tie config path (so DefaultTieConfig applies).
func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/"
	}
	return Config{
		Bookmarks: []Bookmark{
			{Label: "home", Path: home},
			{Label: "tie", Path: "tie:/"},
		},
	}
}

// DefaultTieConfig is the tie client config used when Config.TieConfig is empty.
// It points at a local tie server (daemon :1161, filehost :1162), matching the
// test-env sandbox.
func DefaultTieConfig() client.Config {
	return client.Config{
		Username:         "defaultuser",
		Password:         "defaultpassword",
		Namespace:        "Collections",
		Collection:       "Main",
		Webservice:       "http://localhost:1161",
		DefaultFileHosts: []string{"default"},
		FileHosts: map[string]client.FileHost{
			"default": {URL: "http://localhost:1162"},
		},
	}
}

// Load reads the tie-fm config, creating a default one in the user config dir
// when none exists yet. A file that exists but fails to parse is returned as an
// error (so a broken config is never silently clobbered).
func Load() (Config, error) {
	c := Config{}
	path, err := conf.LoadConfig(appName, fileName, &c)
	if os.IsNotExist(err) {
		c = Default()
		userPath, perr := conf.PathUserConfigDir(appName, fileName)
		if perr == nil {
			if werr := conf.WriteConfig(userPath, c); werr == nil {
				path = userPath
			}
		}
		c.path = path
		return c, nil
	}
	if err != nil {
		return Default(), err
	}
	c.path = path
	if len(c.Bookmarks) == 0 {
		c.Bookmarks = Default().Bookmarks
	}
	return c, nil
}

// LoadTieConfig loads a tie client config from path, or returns
// DefaultTieConfig when path is empty.
func LoadTieConfig(path string) (client.Config, error) {
	if path == "" {
		return DefaultTieConfig(), nil
	}
	var tc client.Config
	if err := conf.ReadConfig(path, &tc); err != nil {
		return DefaultTieConfig(), err
	}
	return tc, nil
}

// Save writes the config back to the file it was loaded from, falling back to
// the user config dir when the path is unknown.
func (c Config) Save() error {
	if c.path != "" {
		return conf.WriteConfig(c.path, c)
	}
	return conf.SaveToUserConfigDir(appName, fileName, c)
}
