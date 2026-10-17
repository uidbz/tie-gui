package tieconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/uidbz/tie/client"
)

// TestSaveConfigPermissions pins the review's §3.2. The tie config carries
// plaintext Username/Password for the triplestore and every filehost, and
// conf.WriteConfig writes mode 0766 - a normal umask lands that as 0744,
// world-readable and executable. SaveConfig must leave 0600.
func TestSaveConfigPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")

	cfg := client.DefaultConfig()
	if err := SaveConfigAt(path, cfg); err != nil {
		t.Fatalf("SaveConfigAt: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config mode = %04o, want 0600", perm)
	}

	// The saved file must still load, and still carry the credentials.
	back, err := client.LoadConfig(path)
	if err != nil {
		t.Fatalf("reloading the saved config: %v", err)
	}
	if back.TripleStoreURL != cfg.TripleStoreURL {
		t.Errorf("TripleStoreURL = %q, want %q", back.TripleStoreURL, cfg.TripleStoreURL)
	}

	// No temp file left behind next to it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" {
			t.Errorf("leftover file after save: %q", e.Name())
		}
	}
}

// TestSaveConfigCreatesPrivateDir checks the containing directory: a config
// directory that is world-listed leaks as much as a world-readable file.
func TestSaveConfigCreatesPrivateDir(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "tie", "config.toml")

	if err := SaveConfigAt(nested, client.DefaultConfig()); err != nil {
		t.Fatalf("SaveConfigAt: %v", err)
	}
	info, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("config dir mode = %04o, want no group/other access", perm)
	}
}

// TestSaveConfigOverwriteKeepsPrivate covers the re-save path: saving over an
// existing config must not widen its permissions.
func TestSaveConfigOverwriteKeepsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfigAt(path, client.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("re-saved config mode = %04o, want 0600", perm)
	}
}
