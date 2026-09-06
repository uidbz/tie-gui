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
