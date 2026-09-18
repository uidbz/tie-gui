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
	// BrowseView selects how the browse wall renders its albums: BrowseCovers
	// (the cover grid) or BrowseTable (a sortable table listing the same
	// albums). Empty means BrowseCovers. Toggled from the gallery ☰ menu and
	// the table view's own button row.
	BrowseView string
	// WallColumns is the ordered set of visible column keys for the browse
	// wall's table view (e.g. "cover", "title", "artist", "year", "kind").
	// Empty means the built-in default set and order.
	WallColumns []string
	// Layout pins the UI layout instead of deriving it from the window width:
	// LayoutCompact for the phone layout (drawer sidebar, grouped playlist,
	// mini bar + Now Playing page), LayoutRegular for the split layout.
	// Empty or LayoutAuto picks by width, which cannot always guess right for
	// a tablet — hence the override.
	Layout string
	// StartupPage selects what the cover wall shows at launch (and after a
	// collection switch): StartupNone leaves it empty until a tag or folder
	// is picked; the others feed it immediately. Empty means StartupNone.
	StartupPage string
	// StartupTag is the tag shown at launch when StartupPage is StartupTag.
	StartupTag string
	// Hotkeys maps a playback action name (the Hotkey* constants) to the
	// Fyne key names bound to it, from the config file's [Hotkeys] table.
	// Desktop only — there is no keyboard on mobile. Actions the file does
	// not mention keep their DefaultHotkeys binding; an action bound to an
	// empty list is left unbound. Example:
	//
	//	[Hotkeys]
	//	PlayPause = ["Space"]
	//	Next = ["N", "Right"]
	//	Stop = []            # unbound
	Hotkeys map[string][]string
	// Backend selects who plays the audio: BackendPwplay (a pwplay-server
	// over HTTP, PwplayServer below) or BackendLocal (on this device, via
	// pwplay's player engine). Empty means the platform default: local on
	// Android, pwplay elsewhere.
	Backend string
}

// Backend values for AppConfig.Backend.
const (
	BackendPwplay = "pwplay" // remote pwplay-server over HTTP
	BackendLocal  = "local"  // on-device playback (pwplay player engine)
)

// DefaultBackend returns the backend used when the config sets none:
// local playback on Android (a phone rarely runs a pwplay-server), the
// pwplay remote everywhere else.
func DefaultBackend() string {
	if os.Getenv("FILESDIR") != "" {
		return BackendLocal
	}
	return BackendPwplay
}

// Hotkey action names for AppConfig.Hotkeys.
const (
	HotkeyPlayPause    = "PlayPause"    // toggle play/pause
	HotkeyStop         = "Stop"         // stop and rewind
	HotkeyNext         = "Next"         // next track
	HotkeyPrevious     = "Previous"     // previous track
	HotkeySeekForward  = "SeekForward"  // seek 10s ahead
	HotkeySeekBackward = "SeekBackward" // seek 10s back
	HotkeyVolumeUp     = "VolumeUp"     // volume +10%
	HotkeyVolumeDown   = "VolumeDown"   // volume -10%
)

// DefaultHotkeys is the built-in desktop key binding set, used for every
// action the config file's [Hotkeys] table does not mention.
func DefaultHotkeys() map[string][]string {
	return map[string][]string{
		HotkeyPlayPause:    {"Space"},
		HotkeyStop:         {"X"},
		HotkeyNext:         {"N"},
		HotkeyPrevious:     {"P"},
		HotkeySeekForward:  {"Right"},
		HotkeySeekBackward: {"Left"},
		HotkeyVolumeUp:     {"="},
		HotkeyVolumeDown:   {"-"},
	}
}

// ResolveHotkeys merges the config file's [Hotkeys] table over the defaults:
// an action the file does not mention keeps its default binding, an action
// bound to an empty list is left unbound, and unknown actions are kept as
// given (they simply never fire).
func ResolveHotkeys(cfg map[string][]string) map[string][]string {
	out := DefaultHotkeys()
	for action, keys := range cfg {
		out[action] = keys
	}
	return out
}

// Layout values for AppConfig.Layout.
const (
	LayoutAuto    = "auto"
	LayoutCompact = "compact"
	LayoutRegular = "regular"
)

// StartupPage values for AppConfig.StartupPage.
const (
	StartupNone      = ""          // empty wall until a tag or folder is picked
	StartupLatest    = "latest"    // every album, most recently imported first
	StartupFavorites = "favorites" // albums tagged "favorite"
	StartupPlaylists = "playlists" // saved playlists
	StartupTag       = "tag"       // albums tagged StartupTag
)

// BrowseView values for AppConfig.BrowseView.
const (
	BrowseCovers = "covers" // cover grid (the default)
	BrowseTable  = "table"  // sortable table of the same albums
)

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
			return fillDefaults(cfg), path
		}
	}
	cfg := Default()
	if p, err := conf.LoadFromUserConfigDir(appName, configFile, &cfg); err == nil {
		return fillDefaults(cfg), p
	}
	return fillDefaults(Default()), path
}

// fillDefaults resolves values left empty in the config file.
func fillDefaults(cfg AppConfig) AppConfig {
	if cfg.Backend == "" {
		cfg.Backend = DefaultBackend()
	}
	return cfg
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
