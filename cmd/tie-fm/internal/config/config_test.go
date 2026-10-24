package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBookmarkCollectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c.Bookmarks = []Bookmark{
		{Label: "home", Path: "/home/user"},
		{Label: "rock", Path: "tie:/music/rock", Collection: "photos"},
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(c2.Bookmarks) != 2 {
		t.Fatalf("reloaded bookmarks = %d, want 2", len(c2.Bookmarks))
	}
	if got := c2.Bookmarks[1].Collection; got != "photos" {
		t.Errorf("tie bookmark Collection = %q, want %q", got, "photos")
	}
	if got := c2.Bookmarks[0].Collection; got != "" {
		t.Errorf("local bookmark Collection = %q, want empty", got)
	}

	// omitempty keeps local bookmarks free of Collection noise in the file:
	// exactly one "Collection = ..." line (the tie bookmark's; TieCollection
	// is a different key) and no empty one.
	data, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	collLines := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Collection = ") {
			collLines++
			if line != "Collection = 'photos'" {
				t.Errorf("unexpected Collection line %q in saved config:\n%s", line, data)
			}
		}
	}
	if collLines != 1 {
		t.Errorf("saved config has %d bookmark Collection lines, want 1:\n%s", collLines, data)
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

func TestTerminalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Terminal != "" {
		t.Errorf("default Terminal = %q, want empty (auto-detect)", c.Terminal)
	}
	c.Terminal = "konsole --workingdirectory %d"
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c2.Terminal != "konsole --workingdirectory %d" {
		t.Errorf("reloaded Terminal = %q", c2.Terminal)
	}
}

func TestTerminalCommandPrecedence(t *testing.T) {
	c := Config{Terminal: "konsole"}
	t.Setenv("TERMINAL", "kitty")
	if got := c.TerminalCommand(); got != "konsole" {
		t.Errorf("configured Terminal loses to $TERMINAL: %q", got)
	}
	c.Terminal = ""
	if got := c.TerminalCommand(); got != "kitty" {
		t.Errorf("$TERMINAL not honored: %q", got)
	}
	c.Terminal = "  alacritty  " // trimmed
	if got := c.TerminalCommand(); got != "alacritty" {
		t.Errorf("whitespace not trimmed: %q", got)
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

func TestMusicBrainzDefaults(t *testing.T) {
	d := DefaultMusicBrainz()
	if d.Source != "local" || d.DBDSN == "" || d.WebBaseURL == "" {
		t.Fatalf("defaults = %+v", d)
	}
	if Default().MusicBrainz.Source != "local" {
		t.Error("Default() must include MusicBrainz defaults")
	}
	// An old config with no MusicBrainz table gets the defaults wholesale.
	got := (MusicBrainz{}).WithDefaults()
	if got.Source != "local" || !got.AutoMatch || !got.FetchCoverArt {
		t.Errorf("empty WithDefaults = %+v", got)
	}
	// A configured backend keeps its explicit false checkboxes.
	custom := MusicBrainz{Source: "web", WebBaseURL: "https://example.org"}
	got = custom.WithDefaults()
	if got.Source != "web" || got.WebBaseURL != "https://example.org" || got.AutoMatch {
		t.Errorf("custom WithDefaults = %+v", got)
	}
}

func TestMusicBrainzRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	c.MusicBrainz.Source = "web"
	c.MusicBrainz.WebBaseURL = "https://mb.example"
	c.MusicBrainz.AutoMatch = false
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c2.MusicBrainz.Source != "web" || c2.MusicBrainz.WebBaseURL != "https://mb.example" || c2.MusicBrainz.AutoMatch {
		t.Errorf("round trip = %+v", c2.MusicBrainz)
	}
	if c2.MusicBrainz.DBDSN == "" || c2.MusicBrainz.CoverArtBaseURL == "" {
		t.Errorf("missing filled defaults: %+v", c2.MusicBrainz)
	}
}
