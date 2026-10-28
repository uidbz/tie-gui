// Package config persists tie-fm's own settings (sidebar bookmarks and the
// path to the tie client config to use) as a TOML file via github.com/uidbz/conf.
// It is separate from the tie *client* config, which describes how to reach a
// tie server.
package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

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

// MusicBrainz configures tie-fm's MusicBrainz metadata lookups.
type MusicBrainz struct {
	// Source selects the backend: "local" (the MusicBrainz PostgreSQL
	// database) or "web" (the public MusicBrainz web service).
	Source string
	// DBDSN is the connection string for the local backend.
	DBDSN string
	// WebBaseURL is the web service root, e.g. https://musicbrainz.org.
	WebBaseURL string
	// CoverArtBaseURL is the Cover Art Archive root.
	CoverArtBaseURL string
	// Contact is an optional contact URL/address added to the User-Agent.
	Contact string
	// AutoMatch looks releases up automatically when scanning albums.
	AutoMatch bool
	// FetchCoverArt offers to download a front cover when the album has none.
	FetchCoverArt bool
}

// DefaultMusicBrainz returns the local-mirror defaults (the database created
// by the companion MusicBrainz setup), with web service fallbacks filled in.
func DefaultMusicBrainz() MusicBrainz {
	return MusicBrainz{
		Source:          "local",
		DBDSN:           "postgres://musicbrainz:musicbrainz@127.0.0.1:5432/musicbrainz_db?sslmode=disable",
		WebBaseURL:      "https://musicbrainz.org",
		CoverArtBaseURL: "https://coverartarchive.org",
		AutoMatch:       true,
		FetchCoverArt:   true,
	}
}

// WithDefaults fills empty values. It treats a completely empty struct as
// "not configured yet" and returns the defaults wholesale, so a config file
// written before this feature existed still gets sensible auto-match/cover-art
// behaviour.
func (m MusicBrainz) WithDefaults() MusicBrainz {
	d := DefaultMusicBrainz()
	if m.Source == "" && m.DBDSN == "" && m.WebBaseURL == "" {
		return d
	}
	if m.Source == "" {
		m.Source = d.Source
	}
	if m.DBDSN == "" {
		m.DBDSN = d.DBDSN
	}
	if m.WebBaseURL == "" {
		m.WebBaseURL = d.WebBaseURL
	}
	if m.CoverArtBaseURL == "" {
		m.CoverArtBaseURL = d.CoverArtBaseURL
	}
	return m
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
	// Terminal is the command line used by "Open Terminal Here". A "%d" token in
	// any argument is replaced by the directory; without one the directory is
	// appended as the final argument. Empty falls back to $TERMINAL, then the
	// first known terminal emulator found in PATH.
	Terminal string
	// MusicBrainz configures album/track metadata lookups.
	MusicBrainz MusicBrainz
	// LeftPath/RightPath are the paths the two panels were showing when the
	// app quit; they restore on next start (falling back to homeStart).
	LeftPath  string
	RightPath string

	path string // where this config was loaded from / will be saved back to
}

// saveMu serializes config writes: menu handlers, bookmark edits, device
// callbacks and transfer completion all call Save, and conf.WriteConfig is a
// plain file write — concurrent saves interleave into torn or stale files.
var saveMu sync.Mutex

// terminalCandidates are tried (in order) when neither Config.Terminal nor
// $TERMINAL names a terminal.
var terminalCandidates = []string{
	"x-terminal-emulator", "konsole", "gnome-terminal", "xfce4-terminal",
	"mate-terminal", "lxterminal", "alacritty", "kitty", "xterm",
}

// TerminalCommand resolves the terminal command line to use: the configured
// Terminal, else $TERMINAL, else the first terminalCandidates entry found in
// PATH. Returns "" when nothing is available.
func (c Config) TerminalCommand() string {
	if s := strings.TrimSpace(c.Terminal); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("TERMINAL")); s != "" {
		return s
	}
	for _, cand := range terminalCandidates {
		if _, err := exec.LookPath(cand); err == nil {
			return cand
		}
	}
	return ""
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
		MusicBrainz: DefaultMusicBrainz(),
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
// error (so a broken config is never silently clobbered); the returned config
// still carries the file's path so callers know where the problem is.
func Load() (Config, error) {
	c := Config{}
	path, err := conf.LoadConfig(appName, fileName, &c)
	if errors.Is(err, os.ErrNotExist) {
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
		d := Default()
		d.path = path // surface the broken file's location, don't lose it
		return d, err
	}
	c.path = path
	if len(c.Bookmarks) == 0 {
		c.Bookmarks = Default().Bookmarks
	}
	c.MusicBrainz = c.MusicBrainz.WithDefaults()
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
// the user config dir when the path is unknown. Saves are serialized: several
// UI actions (bookmarks, toggles, transfers) persist concurrently.
func (c Config) Save() error {
	saveMu.Lock()
	defer saveMu.Unlock()
	if c.path != "" {
		return conf.WriteConfig(c.path, c)
	}
	return conf.SaveToUserConfigDir(appName, fileName, c)
}
