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
	if n.Rating != ratingAuto {
		t.Errorf("unset Rating normalized to %q, want auto", n.Rating)
	}
	if got := (QuickTagSet{Rating: "sideways"}).normalized().Rating; got != ratingAuto {
		t.Errorf("unknown Rating normalized to %q, want auto", got)
	}
	if got := (QuickTagSet{Rating: "top"}).normalized().Rating; got != ratingTop {
		t.Errorf("Rating top normalized to %q", got)
	}
	if got := (QuickTagSet{RatingKeys: []string{"1", "2", "3", "4", "5", "6"}}).normalized().RatingKeys; len(got) != 5 {
		t.Errorf("RatingKeys not capped at 5: %v", got)
	}
	if got := n.Size; got != "m" {
		t.Errorf("unset Size normalized to %q, want m", got)
	}
	if got := (QuickTagSet{Size: "XL"}).normalized().Size; got != "xl" {
		t.Errorf("Size XL normalized to %q, want xl", got)
	}
	if got := (QuickTagSet{Size: "huge"}).normalized().Size; got != "m" {
		t.Errorf("unknown Size normalized to %q, want m", got)
	}

	// The rating bar defaults to the edge opposite the tags bar.
	for _, tc := range []struct{ pos, rating, want string }{
		{"bottom", "", "top"}, {"top", "", "bottom"},
		{"bottom", ratingBottom, "bottom"}, {"top", ratingTop, "top"},
		{"bottom", ratingOff, ""},
	} {
		if got := (QuickTagSet{Position: tc.pos, Rating: tc.rating}).normalized().ratingEdge(); got != tc.want {
			t.Errorf("ratingEdge(%s/%s) = %q, want %q", tc.pos, tc.rating, got, tc.want)
		}
	}
}

func TestQuickTagSizeScale(t *testing.T) {
	prev := float32(0)
	for _, s := range quickTagSizes {
		got := quickTagSizeScale(s)
		if got <= prev {
			t.Errorf("size %q scale %v not larger than the previous preset (%v)", s, got, prev)
		}
		prev = got
	}
	if quickTagSizeScale("m") != 1 || quickTagSizeScale("bogus") != 1 {
		t.Error("m and unknown sizes must be the base scale")
	}
	if len(quickTagSizeOptions) != 5 {
		t.Errorf("editor offers %d sizes, want 5", len(quickTagSizeOptions))
	}
}

// TestQuickTagConfigMigrateLegacy checks that a pre-Size file (Rating =
// "inline", no RatingBar flags) loads as the new layout with the favorite
// heart in the rating bar, while files that already flag a tag are left as
// they are.
func TestQuickTagConfigMigrateLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quicktags.toml")
	legacy := `Position = "bottom"
Rating = "inline"

[[Tag]]
Tag = "favorite"
On = "heart.png"

[[Tag]]
Tag = "cute"

[Collections.photos]
Rating = "inline"
[[Collections.photos.Tag]]
Tag = "favorite"
[[Collections.photos.Tag]]
Tag = "print"
RatingBar = true
`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadQuickTagConfig(path)
	if cfg.Rating != ratingAuto {
		t.Errorf("inline Rating migrated to %q, want auto", cfg.Rating)
	}
	if !cfg.Tags[0].RatingBar || cfg.Tags[1].RatingBar {
		t.Errorf("favorite should move to the rating bar, cute stay: %+v", cfg.Tags)
	}
	ov := cfg.Collections["photos"]
	if ov.Rating != ratingAuto {
		t.Errorf("override inline Rating migrated to %q, want auto", ov.Rating)
	}
	if ov.Tags[0].RatingBar || !ov.Tags[1].RatingBar {
		t.Errorf("override with an explicit RatingBar flag must keep it as is: %+v", ov.Tags)
	}
	if cfg.Size != "m" {
		t.Errorf("migrated file should be marked with Size m, got %q", cfg.Size)
	}

	// A file from before Rating existed (no Rating, no Size) is legacy too.
	if err := os.WriteFile(path, []byte("Position = \"bottom\"\n[[Tag]]\nTag = \"favorite\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg := loadQuickTagConfig(path); !cfg.Tags[0].RatingBar || cfg.Size != "m" {
		t.Errorf("pre-Rating file not migrated: %+v", cfg.QuickTagSet)
	}

	// A current-schema file that deliberately keeps favorite in the tags bar
	// is untouched.
	if err := os.WriteFile(path, []byte("Size = \"m\"\n[[Tag]]\nTag = \"favorite\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg := loadQuickTagConfig(path); cfg.Tags[0].RatingBar {
		t.Error("migration must not touch files that carry Size")
	}
}

func TestQuickTagConfigDefaultAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "quicktags.toml")

	cfg := loadQuickTagConfig(path)
	if len(cfg.Tags) != 1 || cfg.Tags[0].Tag != "favorite" || cfg.Tags[0].On != "heart.png" || cfg.Tags[0].Off != "heart-grey.png" || !cfg.Tags[0].RatingBar {
		t.Fatalf("default config = %+v, want a single favorite/heart entry in the rating bar", cfg.Tags)
	}
	if cfg.Size != "m" || cfg.Rating != ratingAuto {
		t.Errorf("default Size/Rating = %q/%q, want m/auto", cfg.Size, cfg.Rating)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default config was not written: %v", err)
	}

	cfg.Tags = append(cfg.Tags, quickTagEntry{Tag: "review", On: "icons/r.png", Key: "R"})
	cfg.Position = "top"
	cfg.Size = "xl"
	cfg.IconSize = 48
	cfg.SetOverride("photos", QuickTagSet{Tags: []quickTagEntry{{Tag: "print", On: "icons/p.png"}}})
	if err := saveQuickTagConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got := loadQuickTagConfig(path)
	if got.Position != "top" || got.Size != "xl" || got.IconSize != 48 || len(got.Tags) != 2 || got.Tags[1] != cfg.Tags[1] || !got.Tags[0].RatingBar {
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
		Size:     "l",
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
	cfg.SetOverride("rated", QuickTagSet{Rating: ratingOff, RatingKeys: []string{"A"}, Size: "xs"})

	got := cfg.For("photos")
	if got.Position != "top" || got.Size != "l" || got.IconSize != 40 || len(got.Tags) != 1 || got.Tags[0].Tag != "print" {
		t.Errorf("override not merged over default: %+v", got)
	}
	if got.Rating != ratingTop || len(got.RatingKeys) != 2 {
		t.Errorf("override without Rating/RatingKeys should inherit them, got %q %v", got.Rating, got.RatingKeys)
	}
	if got := cfg.For("rated"); got.Rating != ratingOff || len(got.RatingKeys) != 1 || got.Size != "xs" {
		t.Errorf("override Rating/RatingKeys/Size not applied: %q %v %q", got.Rating, got.RatingKeys, got.Size)
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
	if bar.stars == nil {
		t.Fatal("rating on should create the star widget")
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
	if bar.Rating() != 4 || bar.stars.Rating() != 4 || r.rating != 4 {
		t.Errorf("rating not propagated: bar=%d stars=%d reader=%d", bar.Rating(), bar.stars.Rating(), r.rating)
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
	if bar.hash != "" || bar.Applied("favorite") || bar.Rating() != 0 || bar.stars.Rating() != 0 {
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

// TestQuickTagBarLayouts checks how the two pills are anchored: the rating
// pill goes to the far edge unless it shares the tags' edge (then it joins
// this widget's column), and with no stars and no RatingBar tags it vanishes.
func TestQuickTagBarLayouts(t *testing.T) {
	test.NewTempApp(t)
	tags := []quickTagEntry{
		{Tag: "favorite", On: "heart.png", Off: "heart-grey.png", RatingBar: true},
		{Tag: "review"},
	}
	for _, tc := range []struct {
		position, rating string
		tags             []quickTagEntry
		wantObjs         int // objects anchored in Overlay
		wantStars        bool
	}{
		{"bottom", ratingAuto, tags, 2, true},
		{"top", ratingAuto, tags, 2, true},
		{"bottom", ratingBottom, tags, 1, true},
		{"top", ratingTop, tags, 1, true},
		{"bottom", ratingTop, tags, 2, true},
		{"top", ratingOff, tags, 2, false}, // heart alone still forms the rating pill
		{"top", ratingOff, tags[1:], 1, false},
		{"bottom", ratingAuto, nil, 2, true}, // stars alone
		{"bottom", ratingOff, nil, 1, false}, // placeholder label only
	} {
		bar := newQuickTagBar(nil, QuickTagSet{Position: tc.position, Rating: tc.rating, Tags: tc.tags}, t.TempDir(), false)
		if got := len(bar.Overlay.Objects); got != tc.wantObjs {
			t.Errorf("%s/%s/%d tags: %d overlay objects, want %d", tc.position, tc.rating, len(tc.tags), got, tc.wantObjs)
		}
		if (bar.stars != nil) != tc.wantStars {
			t.Errorf("%s/%s: stars present = %v, want %v", tc.position, tc.rating, bar.stars != nil, tc.wantStars)
		}
		if tc.rating == ratingOff && len(bar.Keys()) != len(tc.tags) {
			t.Errorf("rating off should bind only the tag keys, got %d", len(bar.Keys()))
		}
		if len(bar.cells) != len(tc.tags) {
			t.Errorf("%d cells for %d tags", len(bar.cells), len(tc.tags))
		}
	}
}

// TestQuickTagBarPillsDoNotOverlap renders the default layout (tags bottom,
// rating bar top) and checks that the heart sits with the stars at the top,
// the other buttons at the bottom, and that the two pills' boxes are disjoint
// — the regression where the tag buttons were drawn over the stars.
func TestQuickTagBarPillsDoNotOverlap(t *testing.T) {
	test.NewTempApp(t)
	tags := []quickTagEntry{
		{Tag: "favorite", On: "heart.png", Off: "heart-grey.png", RatingBar: true},
		{Tag: "review"}, {Tag: "print"}, {Tag: "cute", On: "star-filled.png"},
	}
	for _, mobile := range []bool{false, true} {
		bar := newQuickTagBar(nil, QuickTagSet{Tags: tags}, t.TempDir(), mobile)
		win := test.NewWindow(bar.Overlay)
		win.Resize(fyne.NewSize(360, 640)) // phone portrait
		drv := fyne.CurrentApp().Driver()
		box := func(o fyne.CanvasObject) (top, bottom float32) {
			p := drv.AbsolutePositionForObject(o)
			return p.Y, p.Y + o.Size().Height
		}
		starTop, starBottom := box(bar.stars)
		heartTop, heartBottom := box(bar.cells[0])
		if starTop < 0 || starBottom > 200 || heartTop < 0 || heartBottom > 200 {
			t.Errorf("mobile=%v: stars %v-%v / heart %v-%v should be near the top edge", mobile, starTop, starBottom, heartTop, heartBottom)
		}
		if heartTop >= starBottom || starTop >= heartBottom {
			t.Errorf("mobile=%v: heart (%v-%v) should share the row with the stars (%v-%v)", mobile, heartTop, heartBottom, starTop, starBottom)
		}
		for _, c := range bar.cells[1:] {
			cTop, cBottom := box(c)
			if cBottom > 640 || cTop < 440 {
				t.Errorf("mobile=%v: %s cell %v-%v should be near the bottom edge", mobile, c.entry.Tag, cTop, cBottom)
			}
			if cTop < starBottom {
				t.Errorf("mobile=%v: %s cell (%v) overlaps the rating row (ends %v)", mobile, c.entry.Tag, cTop, starBottom)
			}
		}
		// Both pills must fit the phone width.
		for _, o := range bar.Overlay.Objects {
			if w := o.MinSize().Width; w > 360 {
				t.Errorf("mobile=%v: pill min width %v exceeds 360", mobile, w)
			}
		}
	}
}

// TestQuickTagBarSizes checks the five presets scale the buttons and that a
// hand-edited IconSize wins over the preset.
func TestQuickTagBarSizes(t *testing.T) {
	test.NewTempApp(t)
	tags := []quickTagEntry{{Tag: "review"}}
	prev := float32(0)
	for _, s := range quickTagSizes {
		bar := newQuickTagBar(nil, QuickTagSet{Size: s, Tags: tags}, t.TempDir(), false)
		if bar.iconSize <= prev {
			t.Errorf("size %q icon size %v not larger than the previous preset (%v)", s, bar.iconSize, prev)
		}
		if got := bar.cells[0].MinSize().Height; got != bar.iconSize {
			t.Errorf("size %q: cell height %v, want icon size %v", s, got, bar.iconSize)
		}
		if got := bar.stars.cells[0].MinSize().Width; got != bar.iconSize*0.75 {
			t.Errorf("size %q: star %v, want 0.75 x icon size %v", s, got, bar.iconSize)
		}
		prev = bar.iconSize
	}
	if got := newQuickTagBar(nil, QuickTagSet{Tags: tags}, t.TempDir(), false).iconSize; got != quickTagIconSize(false) {
		t.Errorf("default size m should be the platform base %v, got %v", quickTagIconSize(false), got)
	}
	if got := newQuickTagBar(nil, QuickTagSet{Size: "xs", IconSize: 77, Tags: tags}, t.TempDir(), false).iconSize; got != 77 {
		t.Errorf("IconSize override should win over Size, got %v", got)
	}
	if m, l := newQuickTagBar(nil, QuickTagSet{Tags: tags}, t.TempDir(), true).iconSize, newQuickTagBar(nil, QuickTagSet{Tags: tags}, t.TempDir(), false).iconSize; m <= l {
		t.Errorf("mobile base (%v) should exceed desktop base (%v)", m, l)
	}
}
