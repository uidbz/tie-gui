package main

import (
	"bytes"
	"image/png"
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
	if n.Rating != ratingInline {
		t.Errorf("unset Rating normalized to %q, want inline", n.Rating)
	}
	if got := (QuickTagSet{Rating: "sideways"}).normalized().Rating; got != ratingInline {
		t.Errorf("unknown Rating normalized to %q, want inline", got)
	}
	if got := (QuickTagSet{Rating: "top"}).normalized().Rating; got != ratingTop {
		t.Errorf("Rating top normalized to %q", got)
	}
	if got := (QuickTagSet{RatingKeys: []string{"1", "2", "3", "4", "5", "6"}}).normalized().RatingKeys; len(got) != 5 {
		t.Errorf("RatingKeys not capped at 5: %v", got)
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
	cfg.Rating = ratingTop
	cfg.RatingKeys = []string{"F1", "F2"}
	cfg.SetOverride("photos", QuickTagSet{Position: "top", Tags: []quickTagEntry{{Tag: "print"}}})
	cfg.SetOverride("empty", QuickTagSet{})
	cfg.SetOverride("rated", QuickTagSet{Rating: ratingOff, RatingKeys: []string{"A"}})

	got := cfg.For("photos")
	if got.Position != "top" || got.IconSize != 40 || len(got.Tags) != 1 || got.Tags[0].Tag != "print" {
		t.Errorf("override not merged over default: %+v", got)
	}
	if got.Rating != ratingTop || len(got.RatingKeys) != 2 {
		t.Errorf("override without Rating/RatingKeys should inherit them, got %q %v", got.Rating, got.RatingKeys)
	}
	if got := cfg.For("rated"); got.Rating != ratingOff || len(got.RatingKeys) != 1 {
		t.Errorf("override Rating/RatingKeys not applied: %q %v", got.Rating, got.RatingKeys)
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
	cfg.RemoveOverride("rated")
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
	cfg := QuickTagSet{
		RatingKeys: []string{"F1", "F2", "F3", "F4", "F5"},
		Tags: []quickTagEntry{
			{Tag: "favorite", On: "heart.png", Off: "heart-grey.png"},
			{Tag: "review"},
			{Tag: "other", Key: "X"},
			{Tag: "starred", On: "star-filled.png"},
		},
	}
	bar := newQuickTagBar(nil, cfg, t.TempDir(), false)
	test.NewWindow(bar.Overlay).Resize(fyne.NewSize(400, 300))

	keys := bar.Keys()
	for _, k := range []fyne.KeyName{"1", "2", "X", "4", "F1", "F5"} {
		if keys[k] == nil {
			t.Errorf("key %q not bound", k)
		}
	}
	if len(keys) != 9 {
		t.Errorf("got %d key bindings, want 9 (4 tags + 5 rating keys)", len(keys))
	}
	if len(bar.stars) != 2 {
		t.Fatalf("inline rating with tags should create a star widget per rendering, got %d", len(bar.stars))
	}
	if c := bar.cells[3]; c.off == nil || c.off == c.on || c.img.Resource != c.off {
		t.Error("On-only icon should get a generated grayscale Off icon shown while not applied")
	}

	r := &tieReader{hash: "abc"}
	bar.hash, bar.reader = r.hash, r
	bar.SetRating("other-hash", 4)
	if bar.Rating() != 0 {
		t.Error("SetRating for a different hash must be ignored")
	}
	bar.SetRating("abc", 4)
	for i, sr := range bar.stars {
		if bar.Rating() != 4 || sr.Rating() != 4 || r.rating != 4 {
			t.Errorf("rating not propagated: bar=%d stars[%d]=%d reader=%d", bar.Rating(), i, sr.Rating(), r.rating)
		}
	}
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
	if bar.hash != "" || bar.Applied("favorite") || bar.Rating() != 0 || bar.stars[0].Rating() != 0 {
		t.Error("SetImage(nil) should clear the bar")
	}
	if bar.cells[0].img.Resource != heartGreyRes {
		t.Error("cleared favorite cell should show the Off icon")
	}
}

func TestGrayscaleResource(t *testing.T) {
	g := grayscaleResource(heartRes)
	if g == nil {
		t.Fatal("heart.png should be convertible")
	}
	img, err := png.Decode(bytes.NewReader(g.Content()))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := png.Decode(bytes.NewReader(heartPNG))
	if img.Bounds() != src.Bounds() {
		t.Errorf("bounds changed: %v vs %v", img.Bounds(), src.Bounds())
	}
	b := img.Bounds()
	colored := false
	for y := b.Min.Y; y < b.Max.Y; y += 7 {
		for x := b.Min.X; x < b.Max.X; x += 7 {
			r, gg, bb, a := img.At(x, y).RGBA()
			if r != gg || gg != bb {
				colored = true
			}
			_, _, _, sa := src.At(x, y).RGBA()
			if a != sa {
				t.Fatalf("alpha changed at %d,%d: %d vs %d", x, y, a, sa)
			}
		}
	}
	if colored {
		t.Error("grayscale output still has chroma")
	}
	if grayscaleResource(fyne.NewStaticResource("bad", []byte("not an image"))) != nil {
		t.Error("undecodable icon should yield nil")
	}
}

func TestQuickTagBarLayouts(t *testing.T) {
	test.NewTempApp(t)
	tags := []quickTagEntry{{Tag: "favorite", On: "heart.png", Off: "heart-grey.png"}}
	for _, tc := range []struct {
		position, rating string
		wantObjs         int // objects anchored in Overlay
		wantStars        bool
	}{
		{"bottom", ratingInline, 1, true},
		{"bottom", ratingBottom, 1, true},
		{"bottom", ratingTop, 2, true},
		{"top", ratingBottom, 2, true},
		{"top", ratingOff, 1, false},
	} {
		bar := newQuickTagBar(nil, QuickTagSet{Position: tc.position, Rating: tc.rating, Tags: tags}, t.TempDir(), false)
		if got := len(bar.Overlay.Objects); got != tc.wantObjs {
			t.Errorf("%s/%s: %d overlay objects, want %d", tc.position, tc.rating, got, tc.wantObjs)
		}
		if (len(bar.stars) > 0) != tc.wantStars {
			t.Errorf("%s/%s: stars present = %v, want %v", tc.position, tc.rating, len(bar.stars) > 0, tc.wantStars)
		}
		if tc.rating == ratingOff && len(bar.Keys()) != 1 {
			t.Errorf("rating off should bind only the tag key, got %d", len(bar.Keys()))
		}
		// The tags pill must be visible in every configuration.
		lay := bar.root.Objects[0].(*fyne.Container).Layout.(*barLayout)
		shown := 0
		for _, o := range []fyne.CanvasObject{lay.row, lay.tags} {
			if o != nil && o.Visible() {
				shown++
			}
		}
		if shown != 1 {
			t.Errorf("%s/%s: %d tag pills visible, want exactly 1", tc.position, tc.rating, shown)
		}
	}
}

// TestQuickTagBarInlineWraps checks that the inline rating row splits into
// stacked pills when the bar is narrower than the row, and merges back.
func TestQuickTagBarInlineWraps(t *testing.T) {
	test.NewTempApp(t)
	tags := []quickTagEntry{
		{Tag: "favorite", On: "heart.png", Off: "heart-grey.png"},
		{Tag: "review"}, {Tag: "print"},
	}
	bar := newQuickTagBar(nil, QuickTagSet{Tags: tags}, t.TempDir(), true)
	win := test.NewWindow(bar.Overlay)
	lay := bar.root.Objects[0].(*fyne.Container).Layout.(*barLayout)

	win.Resize(fyne.NewSize(800, 400))
	if lay.stacked {
		t.Fatal("wide bar should show the single row")
	}
	rowH := bar.MinSize().Height

	win.Resize(fyne.NewSize(360, 400))
	if !lay.stacked || lay.row.Visible() {
		t.Fatal("narrow bar should stack the stars above the tags and hide the row")
	}
	if bar.MinSize().Height <= rowH {
		t.Error("stacked bar should be taller than the single row")
	}

	win.Resize(fyne.NewSize(800, 400))
	if lay.stacked {
		t.Error("widening again should merge back into one row")
	}
}
