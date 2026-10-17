// Package tieconfig loads, resolves, saves and edits the tie *client* config
// (which tie server/collection/filehosts to use) for the GUI apps. It centralizes
// the Android-safe storage location and the in-app settings editor so tie-view
// and tie-audio share one implementation.
package tieconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/uidbz/conf"
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

// SaveConfig writes cfg to the path ResolvePath(name) names, with permissions
// fit for a file that carries credentials.
//
// The tie client config holds plaintext Username/Password for the triplestore
// and for every filehost. conf.WriteConfig writes with mode 0766, which a
// normal umask lands as 0744 — world-readable, and executable — so on any
// shared machine every local user can read the credentials, and with a
// permissive umask overwrite them.
//
// Writing through an unpredictable temp name closes that window: os.CreateTemp
// creates 0600, conf.WriteConfig's os.WriteFile leaves an existing file's mode
// alone, and the rename is atomic, so a reader either sees the old config or
// the new one and never a half-written file. The config directory is 0700 for
// the same reason.
func SaveConfig(name string, cfg any) error {
	return SaveConfigAt(ResolvePath(name), cfg)
}

// SaveConfigAt is SaveConfig for an explicit file path.
func SaveConfigAt(path string, cfg any) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	tmp.Close()
	if err := conf.WriteConfig(tmpName, cfg); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Load loads the tie client config named by name (see ResolvePath) via
// client.LoadConfig, so an explicit path is read directly and normalizeConfig
// runs. A missing file yields the built-in default; the settings editor then
// writes a real config to ResolvePath(name) on the first Apply.
func Load(name string) client.Config {
	c, err := client.LoadConfig(ResolvePath(name))
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Error("tieconfig: reading config", "path", ResolvePath(name), "err", err)
		}
		return client.DefaultConfig()
	}
	WarnHostURLUserinfo(c)
	return c
}
