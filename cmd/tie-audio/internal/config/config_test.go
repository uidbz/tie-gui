package config

import (
	"os"
	"path/filepath"
	"testing"
)

// On Android os.UserConfigDir fails (no $HOME/$XDG_CONFIG_HOME), so saving
// must go through $FILESDIR; the previous conf.SaveToUserConfigDir path
// surfaced the platform's "xdg" error to the user on every settings save.
func TestSaveUsesFilesDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FILESDIR", dir)
	t.Cleanup(func() { savePath = "" })

	cfg := Default()
	cfg.TieCollection = "audio"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := filepath.Join(dir, appName, configFile)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("config not written to %s: %v", want, err)
	}

	loaded, path := Load()
	if path != want {
		t.Fatalf("Load path = %q, want %q", path, want)
	}
	if loaded.TieCollection != "audio" {
		t.Fatalf("loaded TieCollection = %q, want audio", loaded.TieCollection)
	}
}

// The [Hotkeys] table merges over the defaults per action: unmentioned
// actions keep their default binding, an explicit empty list unbinds, and a
// custom key list replaces the default.
func TestResolveHotkeysMergesPerAction(t *testing.T) {
	got := ResolveHotkeys(map[string][]string{
		"PlayPause": {"F5"},
		"Stop":      {},
	})
	if keys := got["PlayPause"]; len(keys) != 1 || keys[0] != "F5" {
		t.Errorf("PlayPause = %v, want [F5]", keys)
	}
	if keys := got["Stop"]; len(keys) != 0 {
		t.Errorf("Stop = %v, want unbound (empty)", keys)
	}
	if keys := got["Next"]; len(keys) != 1 || keys[0] != "N" {
		t.Errorf("Next = %v, want the default [N]", keys)
	}
}

// A nil [Hotkeys] table (no section in the file) resolves to the defaults.
func TestResolveHotkeysDefaults(t *testing.T) {
	got := ResolveHotkeys(nil)
	for action, keys := range DefaultHotkeys() {
		if len(got[action]) != len(keys) {
			t.Errorf("%s = %v, want the default %v", action, got[action], keys)
		}
	}
}

// The [Hotkeys] table round-trips through Save and Load.
func TestHotkeysRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FILESDIR", dir)
	t.Cleanup(func() { savePath = "" })

	cfg := Default()
	cfg.Hotkeys = map[string][]string{"PlayPause": {"F5"}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := Load()
	if keys := loaded.Hotkeys["PlayPause"]; len(keys) != 1 || keys[0] != "F5" {
		t.Fatalf("loaded Hotkeys[PlayPause] = %v, want [F5]", keys)
	}
}
