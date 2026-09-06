package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

func TestFSBaseName(t *testing.T) {
	for in, want := range map[string]string{
		"/":             "",
		"/music":        "music",
		"/music/album":  "album",
		"/music/album/": "album",
		"no-slash-here": "no-slash-here",
	} {
		if got := fsBaseName(in); got != want {
			t.Errorf("fsBaseName(%q) = %q, want %q", in, got, want)
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

// TestFSTreeIds checks node ID classification and display names without any
// tie triplestore: the root is a branch, unknown IDs are not, and file leaves
// show their filename.
func TestFSTreeIds(t *testing.T) {
	fs := &tieFSTree{
		branches: map[string]bool{"/": true, "/music": true},
		files: map[string]tieFSNode{
			"/music/deadbeef": {File: client.File{Uid: "deadbeef", Filename: "song.flac"}, parent: "/music"},
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
	if got := fs.displayName("/music/deadbeef"); got != "song.flac" {
		t.Errorf("displayName(file leaf) = %q, want \"song.flac\"", got)
	}
}

// TestFSChildUIDs checks child listing against a cached directory (no
// triplestore needed): subdirectories come first, sorted by name; non-audio
// files are skipped; and the root's parent edge to itself must not produce a
// "/" child (the tree would recurse forever).
func TestFSChildUIDs(t *testing.T) {
	fs := &tieFSTree{
		dirs: map[string]*client.Directory{
			"/": {SubDirs: []client.SubDirectory{
				{Paths: []string{"tie:/"}},
				{Paths: []string{"tie:/videos"}},
				{Paths: []string{"tie:/music"}},
			}, Files: []client.File{
				{Uid: "deadbeef", Filename: "song.flac", TieType: client.TieAudioFile},
				{Uid: "cafef00d", Filename: "cover.jpg", TieType: client.TieImageFile},
				// Multi-valued tie-type collapses to unknown-file; the
				// media type still identifies the audio.
				{Uid: "baddc0de", Filename: "song2.FLAC", MediaType: "audio/x-flac"},
				{Uid: "f00dcafe", Filename: "notes.txt", MediaType: "text/plain"},
			}},
		},
		branches: make(map[string]bool),
		files:    make(map[string]tieFSNode),
	}
	want := []string{"/music", "/videos", "/deadbeef", "/baddc0de"}
	got := fs.childUIDs("/")
	if !slices.Equal(got, want) {
		t.Fatalf("childUIDs(\"/\") = %v, want %v", got, want)
	}
	if !fs.isBranch("/music") || !fs.isBranch("/videos") {
		t.Error("subdirectories not registered as branches")
	}
	if f, ok := fs.files["/deadbeef"]; !ok || f.Filename != "song.flac" || f.parent != "/" {
		t.Error("file leaf not registered:", fs.files)
	}
}

// TestFSChildUIDsHiddenDirs checks that hidden directories (leading ".") are
// excluded from the tree by default and appear when showHidden is set.
func TestFSChildUIDsHiddenDirs(t *testing.T) {
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

// TestIsAudioFile checks the audio classification: audio media types and the
// audio tie-type pass; images and unknown files don't.
func TestIsAudioFile(t *testing.T) {
	cases := []struct {
		name string
		f    client.File
		want bool
	}{
		{"flac by media type", client.File{Filename: "a.flac", MediaType: "audio/x-flac"}, true},
		{"mp3 by media type", client.File{Filename: "a.mp3", MediaType: "audio/mpeg"}, true},
		{"audio tie-type", client.File{Filename: "a.flac", TieType: client.TieAudioFile}, true},
		{"image", client.File{Filename: "a.jpg", MediaType: "image/jpeg"}, false},
		{"image tie-type", client.File{Filename: "a.jpg", TieType: client.TieImageFile}, false},
		{"unknown", client.File{Filename: "a.txt", MediaType: "text/plain"}, false},
	}
	for _, c := range cases {
		if got := isAudioFile(c.f); got != c.want {
			t.Errorf("isAudioFile(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestNewTieFSTreeRoot builds the tree widget headlessly (against an
// unreachable triplestore): the top level holds just the tie root, which
// starts expanded so the tab never looks empty, and the failed root read
// yields no children rather than a panic.
func TestNewTieFSTreeRoot(t *testing.T) {
	test.NewApp()
	cfg := client.DefaultConfig()
	cfg.TripleStoreURL = "http://127.0.0.1:1"
	tree := newTieFSTree(&browsePage{session: &data.Session{Tie: client.NewTieClient(cfg)}}).tree
	if got := tree.ChildUIDs(""); len(got) != 1 || got[0] != "/" {
		t.Fatalf("ChildUIDs(\"\") = %v, want [\"/\"]", got)
	}
	if !tree.IsBranchOpen("/") {
		t.Error("root branch not open after construction")
	}
}
