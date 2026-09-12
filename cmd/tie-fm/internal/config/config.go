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

	// DefaultTieCollection is tie-fm's own default profile: the [Collections]
	// entry it binds when the user hasn't picked one yet, so each app can use
	// its own profile without fighting over the tie config's shared
	// DefaultCollection.
	DefaultTieCollection = "files"
)

// Bookmark is one entry in the favorites sidebar.
type Bookmark struct {
	Label string
	Path  string
	// Collection names the tie [Collections] entry the bookmark was created
	// under (tie: paths only); empty for local paths and legacy bookmarks —
	// activation then uses whatever collection is currently bound.
	Collection string `toml:",omitempty"`
}

// AppAssoc is a file association: the command used to open files of one type.
// Command may contain a "%f" placeholder for the file path; if absent the path
// is appended as the final argument. Stream marks apps that can open an HTTP
// URL directly (e.g. mpv/vlc): for such apps a tie entry's filehost URL is
// passed instead of a downloaded temporary copy. TieURL marks apps that
// understand tie: URLs (e.g. tie-view): a tie entry is passed as
// "tie:<content hash>" and the app resolves metadata and content itself;
// for tie entries TieURL takes precedence over Stream.
type AppAssoc struct {
	Command string
	Stream  bool
	TieURL  bool
}

// Config is tie-fm's persisted settings.
type Config struct {
	// TieConfig is the path to the tie client config file to load. Empty means
	// use the embedded default (a local tie server; see DefaultTieConfig).
	TieConfig string
	// TieCollection is this app's own tie collection selection (a
	// [Collections] entry name in the loaded tie config). Empty means "not
	// chosen yet": DefaultTieCollection is preferred at startup, then the tie
	// config's DefaultCollection.
	TieCollection string
	// Bookmarks populate the favorites sidebar.
	Bookmarks []Bookmark
	// FileApps maps a lowercase file extension (without the leading dot) to the
	// association used to open files of that type, overriding the xdg-open
	// default.
	FileApps map[string]AppAssoc
	// ShowHidden makes dot-files (names with a leading ".") visible in the
	// listing; hidden (false) by default, toggled from the app menu.
	ShowHidden bool

	path string // where this config was loaded from / will be saved back to
}

// Path reports the file this config was loaded from (or will be written to).
func (c Config) Path() string { return c.path }

// ExtKey normalizes a filename to its file-association key: the lowercase
// extension without the leading dot ("" when the name has no extension).
func ExtKey(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

// AppFor returns the configured association for the given filename's
// extension, or false when none is set.
func (c Config) AppFor(name string) (AppAssoc, bool) {
	if c.FileApps == nil {
		return AppAssoc{}, false
	}
	a, ok := c.FileApps[ExtKey(name)]
	return a, ok
}

// SetApp associates the association with the extension key (a bare extension
// such as "pdf"). An empty command removes the association.
func (c *Config) SetApp(ext string, assoc AppAssoc) {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if ext == "" {
		return
	}
	assoc.Command = strings.TrimSpace(assoc.Command)
	if assoc.Command == "" {
		delete(c.FileApps, ext)
		return
	}
	if c.FileApps == nil {
		c.FileApps = map[string]AppAssoc{}
	}
	c.FileApps[ext] = assoc
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
// It points at a local tie server (triplestore :1161, filehost :1162), matching the
// test-env sandbox. The explicit [Collections] entry keeps the config in the same
// normalized shape client.LoadConfig produces, so collection-aware filehost
// resolution (TieClient.ResolveHosts) finds the default host.
func DefaultTieConfig() client.Config {
	return client.Config{
		Username:         "defaultuser",
		Password:         "defaultpassword",
		Namespace:        "Collections",
		Collection:       "Main",
		TripleStoreURL:   "http://localhost:1161",
		DefaultFileHosts: []string{"default"},
		FileHosts: map[string]client.FileHost{
			"default": {URL: "http://localhost:1162"},
		},
		DefaultCollection: "Main",
		Collections: map[string]client.CollectionEntry{
			"Main": {
				Namespace:  "Collections",
				Collection: "Main",
				FileHosts:  []string{"default"},
			},
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
// DefaultTieConfig when path is empty. It delegates to client.LoadConfig so an
// absolute/relative path is read directly, a bare name is searched in tie's
// config dirs, and normalizeConfig (TripleStoreURL/Webservice aliasing, synthesized
// Collections) runs on the result.
func LoadTieConfig(path string) (client.Config, error) {
	if path == "" {
		return DefaultTieConfig(), nil
	}
	tc, err := client.LoadConfig(path)
	if err != nil {
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
