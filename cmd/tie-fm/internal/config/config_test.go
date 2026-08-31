package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := filepath.Join(dir, "tie-fm", "config.toml")
	if c.Path() != want {
		t.Errorf("path = %q, want %q", c.Path(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if len(c.Bookmarks) == 0 {
		t.Error("expected default bookmarks")
	}

	// Reload should read the written file without recreating it.
	c2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(c2.Bookmarks) != len(c.Bookmarks) {
		t.Errorf("reloaded bookmarks = %d, want %d", len(c2.Bookmarks), len(c.Bookmarks))
	}
}

func TestFileAppHelpers(t *testing.T) {
	var c Config
	if got := c.AppFor("movie.mkv"); got != "" {
		t.Errorf("AppFor on nil map = %q, want empty", got)
	}
	c.SetApp(".MKV", "mpv %f") // leading dot and case are normalized
	if got := c.AppFor("/some/path/MOVIE.mkv"); got != "mpv %f" {
		t.Errorf("AppFor = %q, want %q", got, "mpv %f")
	}
	if got := ExtKey("noext"); got != "" {
		t.Errorf("ExtKey(noext) = %q, want empty", got)
	}
	c.SetApp("mkv", "") // empty command removes
	if got := c.AppFor("a.mkv"); got != "" {
		t.Errorf("after removal AppFor = %q, want empty", got)
	}
}

func TestFileAppsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c.SetApp("pdf", "okular %f")
	c.SetApp("png", "gimp")
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c2.AppFor("doc.pdf") != "okular %f" || c2.AppFor("img.png") != "gimp" {
		t.Errorf("round-trip FileApps = %#v", c2.FileApps)
	}
}

func TestDefaultTieConfigLocal(t *testing.T) {
	c := DefaultTieConfig()
	if c.TripleStoreURL != "http://localhost:1161" {
		t.Errorf("TripleStoreURL = %q", c.TripleStoreURL)
	}
	if fh, ok := c.FileHosts["default"]; !ok || fh.URL != "http://localhost:1162" {
		t.Errorf("default filehost = %+v", c.FileHosts["default"])
	}
}

func TestLoadTieConfigEmptyIsDefault(t *testing.T) {
	c, err := LoadTieConfig("")
	if err != nil {
		t.Fatalf("LoadTieConfig: %v", err)
	}
	if c.TripleStoreURL != "http://localhost:1161" {
		t.Errorf("TripleStoreURL = %q, want local default", c.TripleStoreURL)
	}
}
