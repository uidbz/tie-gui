package main

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"
)

func TestBaseName(t *testing.T) {
	for in, want := range map[string]string{
		"/":             "",
		"/music":        "music",
		"/music/album":  "album",
		"/music/album/": "album",
		"no-slash-here": "no-slash-here",
	} {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinNode(t *testing.T) {
	if got := joinNode("/", "abc"); got != "/abc" {
		t.Errorf("joinNode(\"/\", \"abc\") = %q, want \"/abc\"", got)
	}
	if got := joinNode("/music", "abc"); got != "/music/abc" {
		t.Errorf("joinNode(\"/music\", \"abc\") = %q, want \"/music/abc\"", got)
	}
}

// TestTieFSTreeIds checks node ID classification and display names without
// any tie triplestore: the root is a branch, unknown IDs are not, and file
// leaves show their filename.
func TestTieFSTreeIds(t *testing.T) {
	fs := &tieFSTree{
		branches: map[string]bool{"/": true, "/music": true},
		files: map[string]tieFSNode{
			"/music/deadbeef": {File: client.File{Uid: "deadbeef", Filename: "cover.jpg"}, parent: "/music"},
		},
	}
	for _, id := range []string{"/", "/music"} {
		if !fs.isBranch(id) {
			t.Errorf("isBranch(%q) = false, want true", id)
		}
	}
	if fs.isBranch("/music/deadbeef") {
		t.Error("isBranch(file leaf) = true, want false")
	}
	if got := fs.displayName("/"); got != "/" {
		t.Errorf("displayName(\"/\") = %q, want \"/\"", got)
	}
	if got := fs.displayName("/music"); got != "music" {
		t.Errorf("displayName(\"/music\") = %q, want \"music\"", got)
	}
	if got := fs.displayName("/music/deadbeef"); got != "cover.jpg" {
		t.Errorf("displayName(file leaf) = %q, want \"cover.jpg\"", got)
	}
}

// TestChildUIDs checks child listing against a cached directory (no triplestore
// needed): subdirectories come first, sorted by name; non-image files are
// skipped; and the root's parent edge to itself must not produce a "/"
// child (the tree would recurse forever).
func TestChildUIDs(t *testing.T) {
	fs := &tieFSTree{
		dirs: map[string]*client.Directory{
			"/": {SubDirs: []client.SubDirectory{
				{Paths: []string{"tie:/"}},
				{Paths: []string{"tie:/videos"}},
				{Paths: []string{"tie:/music"}},
			}, Files: []client.File{
				{Uid: "deadbeef", Filename: "cover.jpg", TieType: client.TieImageFile},
				{Uid: "cafef00d", Filename: "song.flac", TieType: client.TieAudioFile},
				// Multi-valued tie-type collapses to unknown-file; the
				// media type still identifies the image.
				{Uid: "baddc0de", Filename: "photo.JPG", MediaType: "image/jpeg"},
				{Uid: "f00dcafe", Filename: "song2.flac", MediaType: "audio/x-flac"},
			}},
		},
		branches: make(map[string]bool),
		files:    make(map[string]tieFSNode),
	}
	want := []string{"/music", "/videos", "/deadbeef", "/baddc0de"}
	got := fs.childUIDs("/")
	if len(got) != len(want) {
		t.Fatalf("childUIDs(\"/\") = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("childUIDs(\"/\") = %v, want %v", got, want)
		}
	}
	if !fs.isBranch("/music") || !fs.isBranch("/videos") {
		t.Error("subdirectories not registered as branches")
	}
	if f, ok := fs.files["/deadbeef"]; !ok || f.Filename != "cover.jpg" || f.parent != "/" {
		t.Error("file leaf not registered:", fs.files)
	}
}

// TestNewTieFSTree builds the widget headlessly: the top level holds just
// the tie root, which starts expanded so the tab never looks empty; a
// failed dir read (unreachable triplestore) yields no children rather than a
// panic.
func TestNewTieFSTree(t *testing.T) {
	test.NewApp()
	config := client.DefaultConfig()
	config.TripleStoreURL = "http://127.0.0.1:1"
	tc := client.NewTieClient(config)
	tree := newTieFSTree(nil, tc).tree
	if got := tree.ChildUIDs(""); len(got) != 1 || got[0] != "/" {
		t.Fatalf("ChildUIDs(\"\") = %v, want [\"/\"]", got)
	}
	if !tree.IsBranchOpen("/") {
		t.Error("root branch not open after construction")
	}
	if got := tree.ChildUIDs("/"); len(got) != 0 {
		t.Errorf("ChildUIDs(\"/\") with unreachable triplestore = %v, want empty", got)
	}
}

// collectLabels gathers the text of every label in an object's renderer
// tree, descending through widgets and containers.
func collectLabels(o fyne.CanvasObject, out *[]string) {
	switch o := o.(type) {
	case *widget.Label:
		*out = append(*out, o.Text)
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(o).Objects() {
			collectLabels(c, out)
		}
	case *fyne.Container:
		for _, c := range o.Objects {
			collectLabels(c, out)
		}
	}
}

// TestTreeRenders renders the tree in a test window: the "/" node must be
// visible. Regression test for isBranch("") returning false — the fyne tree
// walk starts at the root node "" and only descends into ChildUIDs for
// branches, so a non-branch root rendered the whole tree empty. The triplestore
// is unreachable here; ChildUIDs("") needs no I/O, so "/" must still show.
func TestTreeRenders(t *testing.T) {
	test.NewApp()
	config := client.DefaultConfig()
	config.TripleStoreURL = "http://127.0.0.1:1"
	tc := client.NewTieClient(config)
	tree := newTieFSTree(nil, tc).tree
	w := test.NewWindow(tree)
	w.Resize(fyne.NewSize(300, 400))
	tree.Refresh()

	labels := []string{}
	collectLabels(tree, &labels)
	if !slices.Contains(labels, "/") {
		t.Errorf("rendered tree labels = %v, want the \"/\" node", labels)
	}
}

// TestIsImageFileRaw checks that camera raw files are excluded from the
// gallery: they carry an "image/" media type (or image tie-type) but cannot
// be decoded, so isImageFile must reject them via media type or extension.
func TestIsImageFileRaw(t *testing.T) {
	cases := []struct {
		name string
		f    client.File
		want bool
	}{
		{"jpeg", client.File{Filename: "a.jpg", MediaType: "image/jpeg"}, true},
		{"png", client.File{Filename: "a.png", MediaType: "image/png"}, true},
		{"cr2 by media type", client.File{Filename: "a.cr2", MediaType: "image/x-canon-cr2"}, false},
		{"nef by extension (tiff media type)", client.File{Filename: "a.nef", MediaType: "image/tiff"}, false},
		{"raw extension", client.File{Filename: "a.raw", MediaType: "image/tiff"}, false},
		{"dng by extension", client.File{Filename: "a.dng", MediaType: "image/tiff"}, false},
		{"tiff is still an image", client.File{Filename: "a.tif", MediaType: "image/tiff"}, true},
		{"image tie-type fallback", client.File{Filename: "a.jpg", TieType: client.TieImageFile}, true},
		{"raw by tie-type fallback", client.File{Filename: "a.cr2", TieType: client.TieImageFile}, false},
	}
	for _, c := range cases {
		if got := isImageFile(c.f); got != c.want {
			t.Errorf("isImageFile(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestChildUIDsHiddenDirs checks that hidden directories (leading ".") are
// excluded from the tree by default and appear when showHidden is set.
func TestChildUIDsHiddenDirs(t *testing.T) {
	fs := &tieFSTree{
		dirs: map[string]*client.Directory{
			"/": {SubDirs: []client.SubDirectory{
				{Paths: []string{"tie:/videos"}},
				{Paths: []string{"tie:/.cache"}},
				{Paths: []string{"tie:/music"}},
			}},
		},
		branches: make(map[string]bool),
		files:    make(map[string]tieFSNode),
	}

	if got := fs.childUIDs("/"); !slices.Equal(got, []string{"/music", "/videos"}) {
		t.Errorf("childUIDs with hidden dirs = %v, want [/music /videos]", got)
	}

	fs.showHidden = true
	if got := fs.childUIDs("/"); !slices.Equal(got, []string{"/.cache", "/music", "/videos"}) {
		t.Errorf("childUIDs showing hidden dirs = %v, want [/.cache /music /videos]", got)
	}
}
