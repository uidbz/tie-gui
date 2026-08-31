// Package tieconfig loads, resolves, saves and edits the tie *client* config
// (which tie server/collection/filehosts to use) for the GUI apps. It centralizes
// the Android-safe storage location and the in-app settings editor so tie-view
// and tie-audio-player share one implementation.
package tieconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/uidbz/tie/client"
)

// Dir is the directory tie configs live in. On Android os.UserConfigDir has no
// $HOME to resolve, so we use $FILESDIR (the app's internal files dir, set by
// Fyne's native code) — the same tree Fyne writes preferences.json into, which
// Android Auto Backup restores on reinstall. On desktop it is the normal user
// config dir, matching github.com/uidbz/conf.
func Dir() string {
	if d := os.Getenv("FILESDIR"); d != "" {
		return filepath.Join(d, "tie")
	}
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "tie")
}

// withTOML appends the .toml suffix unless the name already carries it.
func withTOML(name string) string {
	if name != "" && !strings.HasSuffix(name, ".toml") {
		return name + ".toml"
	}
	return name
}

// ResolvePath turns a -config value into the concrete file path to load from and
// save to. Empty is config.toml in Dir; a value containing a path separator is
// used verbatim (with a .toml suffix); a bare name resolves under Dir so it is
// writable even on Android, where conf's name search cannot write (no $HOME).
func ResolvePath(name string) string {
	switch {
	case name == "":
		return filepath.Join(Dir(), "config.toml")
	case strings.ContainsRune(name, '/'):
		return withTOML(name)
	default:
		return filepath.Join(Dir(), withTOML(name))
	}
}

// Load loads the tie client config named by name (see ResolvePath) via
// client.LoadConfig, so an explicit path is read directly and normalizeConfig
// runs. A missing file yields the built-in default; the settings editor then
// writes a real config to ResolvePath(name) on the first Apply.
func Load(name string) client.Config {
	c, err := client.LoadConfig(ResolvePath(name))
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Println("Error reading tie config:", err)
		}
		return client.DefaultConfig()
	}
	return c
}
