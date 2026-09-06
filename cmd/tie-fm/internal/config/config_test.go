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
	if _, ok := c.AppFor("movie.mkv"); ok {
		t.Errorf("AppFor on nil map returned an association")
	}
	c.SetApp(".MKV", AppAssoc{Command: "mpv %f", Stream: true}) // leading dot and case are normalized
	assoc, ok := c.AppFor("/some/path/MOVIE.mkv")
	if !ok || assoc.Command != "mpv %f" || !assoc.Stream {
		t.Errorf("AppFor = %+v, %v, want Command+Stream, true", assoc, ok)
	}
	if got := ExtKey("noext"); got != "" {
		t.Errorf("ExtKey(noext) = %q, want empty", got)
	}
	c.SetApp("mkv", AppAssoc{}) // empty command removes
	if _, ok := c.AppFor("a.mkv"); ok {
		t.Errorf("after removal AppFor returned an association")
	}
}

func TestFileAppsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c.SetApp("pdf", AppAssoc{Command: "okular %f"})
	c.SetApp("mkv", AppAssoc{Command: "mpv %f", Stream: true})
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	pdf, pdfOK := c2.AppFor("doc.pdf")
	mkv, mkvOK := c2.AppFor("movie.mkv")
	if !pdfOK || pdf.Command != "okular %f" || pdf.Stream {
		t.Errorf("round-trip pdf = %+v, %v", pdf, pdfOK)
	}
	if !mkvOK || mkv.Command != "mpv %f" || !mkv.Stream {
		t.Errorf("round-trip mkv = %+v, %v", mkv, mkvOK)
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
