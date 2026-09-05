package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestQuickTagConfigNormalized(t *testing.T) {
	cfg := QuickTagSet{
		Position: "TOP",
		Tags: []quickTagEntry{
			{Tag: "favorite", On: "heart.png", Off: "heart-grey.png"},
			{Tag: ""}, // dropped
			{Tag: "review", Key: "R"},
			{Tag: "trash"},
		},
	}
	n := cfg.normalized()
	if n.Position != "bottom" {
		t.Errorf("Position %q normalized to %q, want bottom (only exact \"top\" is honored)", cfg.Position, n.Position)
	}
	if len(n.Tags) != 3 {
		t.Fatalf("got %d tags, want 3 (blank dropped)", len(n.Tags))
	}
	wantKeys := []string{"1", "R", "3"}
	for i, e := range n.Tags {
		if e.Key != wantKeys[i] {
			t.Errorf("tag %d (%s) key = %q, want %q", i, e.Tag, e.Key, wantKeys[i])
		}
	}
	if (QuickTagSet{Position: "top"}).normalized().Position != "top" {
		t.Error("explicit top position not kept")
	}
}

func TestQuickTagConfigDefaultAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "quicktags.toml")

	cfg := loadQuickTagConfig(path)
	if len(cfg.Tags) != 1 || cfg.Tags[0].Tag != "favorite" || cfg.Tags[0].On != "heart.png" || cfg.Tags[0].Off != "heart-grey.png" {
		t.Fatalf("default config = %+v, want a single favorite/heart entry", cfg.Tags)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default config was not written: %v", err)
	}

	cfg.Tags = append(cfg.Tags, quickTagEntry{Tag: "review", On: "icons/r.png", Key: "R"})
	cfg.Position = "top"
	cfg.IconSize = 48
	cfg.SetOverride("photos", QuickTagSet{Tags: []quickTagEntry{{Tag: "print", On: "icons/p.png"}}})
	if err := saveQuickTagConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got := loadQuickTagConfig(path)
	if got.Position != "top" || got.IconSize != 48 || len(got.Tags) != 2 || got.Tags[1] != cfg.Tags[1] {
		t.Errorf("round trip = %+v, want %+v", got, cfg)
	}
	if ov, ok := got.Collections["photos"]; !ok || len(ov.Tags) != 1 || ov.Tags[0].Tag != "print" || ov.Position != "" {
		t.Errorf("collection override did not round-trip: %+v", got.Collections)
	}

	// A malformed file falls back to the default without being overwritten.
	if err := os.WriteFile(path, []byte("Position = [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := loadQuickTagConfig(path)
	if len(bad.Tags) != 1 || bad.Tags[0].Tag != "favorite" {
		t.Errorf("malformed config did not fall back to default: %+v", bad)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "Position = [broken" {
		t.Error("malformed config file was overwritten")
	}
}

func TestQuickTagConfigFor(t *testing.T) {
	cfg := quickTagConfig{QuickTagSet: QuickTagSet{
		Position: "bottom",
		IconSize: 40,
		Tags:     []quickTagEntry{{Tag: "favorite"}},
	}}
	if got := cfg.For("photos"); !quickTagSetsEqual(got, cfg.QuickTagSet) {
		t.Errorf("collection without override should get the default, got %+v", got)
	}
	cfg.SetOverride("photos", QuickTagSet{Position: "top", Tags: []quickTagEntry{{Tag: "print"}}})
	cfg.SetOverride("empty", QuickTagSet{})

	got := cfg.For("photos")
	if got.Position != "top" || got.IconSize != 40 || len(got.Tags) != 1 || got.Tags[0].Tag != "print" {
		t.Errorf("override not merged over default: %+v", got)
	}
	if got := cfg.For("empty"); len(got.Tags) != 0 || got.Position != "bottom" {
		t.Errorf("empty override should yield no buttons with default position, got %+v", got)
	}
	if got := cfg.For(""); !quickTagSetsEqual(got, cfg.QuickTagSet) {
		t.Errorf("empty collection name should always resolve to the default, got %+v", got)
	}
	if !cfg.HasOverride("photos") || cfg.HasOverride("other") || cfg.HasOverride("") {
		t.Error("HasOverride mismatch")
	}
	cfg.RemoveOverride("photos")
	cfg.RemoveOverride("empty")
	if cfg.Collections != nil {
		t.Error("removing the last override should nil the map so it is omitted from TOML")
	}
}

func TestResolveQuickTagIcon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if resolveQuickTagIcon(dir, "") != nil {
		t.Error("empty name should resolve to nil")
	}
	if r := resolveQuickTagIcon(dir, "heart.png"); r != heartRes {
		t.Error("built-in heart.png not resolved to the embedded resource")
	}
	if r := resolveQuickTagIcon(dir, "star-empty.png"); r != starEmptyRes {
		t.Error("built-in star-empty.png not resolved to the embedded resource")
	}
	r := resolveQuickTagIcon(dir, "custom.png")
	if r == nil || string(r.Content()) != "png" {
		t.Error("relative path not resolved against baseDir")
	}
	if resolveQuickTagIcon(dir, "missing.png") != nil {
		t.Error("missing icon should resolve to nil")
	}
	// A file on disk shadows a built-in of the same name.
	if err := os.WriteFile(filepath.Join(dir, "heart.png"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := resolveQuickTagIcon(dir, "heart.png"); r == nil || string(r.Content()) != "mine" {
		t.Error("on-disk heart.png did not shadow the built-in")
	}
}

// TestQuickTagBarState drives the bar's applied-set bookkeeping without a tie
// server: external SetTags updates repaint the cells and refresh the reader's
// cache, and Keys reflects the normalized bindings.
func TestQuickTagBarState(t *testing.T) {
	test.NewTempApp(t)
	cfg := QuickTagSet{Tags: []quickTagEntry{
		{Tag: "favorite", On: "heart.png", Off: "heart-grey.png"},
		{Tag: "review"},
		{Tag: "other", Key: "X"},
	}}
	bar := newQuickTagBar(nil, cfg, t.TempDir(), false)
	test.NewWindow(bar.Overlay).Resize(fyne.NewSize(400, 300))

	keys := bar.Keys()
	for _, k := range []fyne.KeyName{"1", "2", "X"} {
		if keys[k] == nil {
			t.Errorf("key %q not bound", k)
		}
	}
	if len(keys) != 3 {
		t.Errorf("got %d key bindings, want 3", len(keys))
	}

	r := &tieReader{hash: "abc"}
	bar.hash, bar.reader = r.hash, r
	bar.SetTags("other-hash", []string{"favorite"})
	if bar.Applied("favorite") {
		t.Error("SetTags for a different hash must be ignored")
	}
	bar.SetTags("abc", []string{"favorite", "unrelated"})
	if !bar.Applied("favorite") || bar.Applied("review") {
		t.Error("applied set not updated from SetTags")
	}
	if !r.tagsKnown || len(r.tags) != 2 {
		t.Errorf("reader cache = %v (known=%v), want the applied list", r.tags, r.tagsKnown)
	}
	if got := bar.cells[0].img.Resource; got != heartRes {
		t.Errorf("favorite cell shows %v, want the On icon", got)
	}
	if bar.cells[1].text == nil {
		t.Error("iconless cell should render as text")
	}

	bar.SetImage(nil)
	if bar.hash != "" || bar.Applied("favorite") {
		t.Error("SetImage(nil) should clear the bar")
	}
	if bar.cells[0].img.Resource != heartGreyRes {
		t.Error("cleared favorite cell should show the Off icon")
	}
}
