package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

func TestVisibleEntriesHidesDotFiles(t *testing.T) {
	entries := []fs.Entry{
		{Name: "music"},
		{Name: ".config"},
		{Name: ".hidden.txt"},
		{Name: "photo.jpg"},
	}

	got := visibleEntries(entries, false)
	if len(got) != 2 || got[0].Name != "music" || got[1].Name != "photo.jpg" {
		t.Errorf("hidden = %v, want [music photo.jpg]", got)
	}
	got = visibleEntries(entries, true)
	if len(got) != len(entries) {
		t.Errorf("shown = %v, want all %d entries", got, len(entries))
	}
}

// TestTransferItemsIntoTieOffersDirTypes checks that local→tie transfers keep
// the plain copy/move items and gain "as" submenus listing the built-in
// dir-types plus a custom-label prompt.
func TestTransferItemsIntoTieOffersDirTypes(t *testing.T) {
	src := &FileManager{ops: fs.NewOperations(nil)}
	dst := &FileManager{currentPath: binding.NewString()}
	dst.currentPath.Set("tie:/music")

	set := []fs.Entry{{Name: "album", Path: "/tmp/album", IsDir: true}}
	items := src.transferItems(set, dst, false)

	if len(items) != 4 {
		t.Fatalf("items = %d, want 4: %v", len(items), items)
	}
	if items[0].Label != "Copy into tie" || items[1].Label != "Move into tie" {
		t.Errorf("plain items = %q / %q", items[0].Label, items[1].Label)
	}
	for i, want := range map[int]string{2: "Copy into tie as", 3: "Move into tie as"} {
		item := items[i]
		if item.Label != want {
			t.Errorf("items[%d].Label = %q, want %q", i, item.Label, want)
		}
		if item.ChildMenu == nil {
			t.Fatalf("%q has no submenu", want)
		}
		children := item.ChildMenu.Items
		if len(children) != len(fs.BuiltinDirTypes)+1 {
			t.Fatalf("%q submenu = %d items, want %d", want, len(children), len(fs.BuiltinDirTypes)+1)
		}
		for j, dt := range fs.BuiltinDirTypes {
			if children[j].Label != dt {
				t.Errorf("%q submenu[%d] = %q, want %q", want, j, children[j].Label, dt)
			}
		}
		if children[len(children)-1].Label != "Custom…" {
			t.Errorf("%q submenu last = %q, want Custom…", want, children[len(children)-1].Label)
		}
	}
}

// TestTransferItemsWithinLocalHasNoDirTypes checks plain local→local transfers
// are not offered dir-type submenus.
func TestTransferItemsWithinLocalHasNoDirTypes(t *testing.T) {
	src := &FileManager{ops: fs.NewOperations(nil)}
	dst := &FileManager{currentPath: binding.NewString()}
	dst.currentPath.Set("/tmp/dest")

	set := []fs.Entry{{Name: "album", Path: "/tmp/album", IsDir: true}}
	items := src.transferItems(set, dst, false)
	for _, item := range items {
		if item.ChildMenu != nil {
			t.Errorf("%q unexpectedly has a submenu", item.Label)
		}
	}
}

// TestContextMenuAlbumImportItem checks "Import as albums…" is offered on
// local directories (where a bulk album scan can run) and nowhere else: not on
// files, and not on tie entries.
func TestContextMenuAlbumImportItem(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	fm := &FileManager{ops: fs.NewOperations(reg), registry: reg}

	hasItem := func(items []*fyne.MenuItem, label string) bool {
		for _, item := range items {
			if item.Label == label {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name  string
		entry fs.Entry
		want  bool
	}{
		{"local dir", fs.Entry{Name: "music", Path: "/tmp/music", IsDir: true}, true},
		{"local file", fs.Entry{Name: "song.flac", Path: "/tmp/song.flac"}, false},
		{"tie dir", fs.Entry{Name: "music", Path: "tie:/music", IsDir: true, Hash: "uid"}, false},
	}
	for _, c := range cases {
		items := fm.contextMenuItems(c.entry, 0)
		if got := hasItem(items, "Import as albums…"); got != c.want {
			t.Errorf("%s: Import as albums… present = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSelectedGroupsAlbumImport checks the plan-dialog selection seam:
// checked groups import, unchecked ones don't, dest-less groups are skipped.
func TestSelectedGroupsAlbumImport(t *testing.T) {
	plan := []client.AlbumGroup{
		{SourceDir: "/a", Dest: "/music/a"},
		{SourceDir: "/b", Dest: "/music/b"},
		{SourceDir: "/c", Dest: ""}, // cannot import; row is disabled
	}
	selected := []bool{true, false, false}

	got := selectedGroups(plan, selected)
	if len(got) != 1 || got[0].SourceDir != "/a" {
		t.Errorf("selectedGroups = %v, want [/a]", got)
	}

	selected = []bool{true, true, false}
	got = selectedGroups(plan, selected)
	if len(got) != 2 {
		t.Errorf("selectedGroups = %v, want 2 groups", got)
	}
}

// TestDropMenuOffersAlbumImport checks the drag-drop menu carries the bulk
// album import when a single local directory is dragged (like the context
// menu's item), in addition to the plain transfer items.
func TestDropMenuOffersAlbumImport(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	src := &FileManager{ops: fs.NewOperations(reg), registry: reg}
	dst := &FileManager{currentPath: binding.NewString()}
	dst.currentPath.Set("tie:/music")

	hasItem := func(items []*fyne.MenuItem, label string) bool {
		for _, item := range items {
			if item.Label == label {
				return true
			}
		}
		return false
	}

	set := []fs.Entry{{Name: "album", Path: "/tmp/album", IsDir: true}}
	items := src.dropMenuItems(set, dst)
	if !hasItem(items, "Import as albums…") {
		t.Errorf("drop menu = %v, want Import as albums…", items)
	}
	// A non-directory (alone or mixed in) keeps the menu transfer-only.
	set = []fs.Entry{{Name: "song.flac", Path: "/tmp/song.flac"}}
	if hasItem(src.dropMenuItems(set, dst), "Import as albums…") {
		t.Errorf("file drag unexpectedly offers Import as albums…")
	}
	set = []fs.Entry{
		{Name: "a", Path: "/tmp/a", IsDir: true},
		{Name: "song.flac", Path: "/tmp/song.flac"},
	}
	if hasItem(src.dropMenuItems(set, dst), "Import as albums…") {
		t.Errorf("mixed drag unexpectedly offers Import as albums…")
	}
	// Several local directories import together.
	set = []fs.Entry{
		{Name: "a", Path: "/tmp/a", IsDir: true},
		{Name: "b", Path: "/tmp/b", IsDir: true},
	}
	if !hasItem(src.dropMenuItems(set, dst), "Import as albums…") {
		t.Errorf("multi-dir drag should offer Import as albums…")
	}
}

// TestContextMenuSetUsesSelection checks a right-click on a selected row acts
// on the whole selection, and on an unselected row only on that row.
func TestContextMenuSetUsesSelection(t *testing.T) {
	a := fs.Entry{Name: "a", Path: "/tmp/a", IsDir: true}
	b := fs.Entry{Name: "b", Path: "/tmp/b", IsDir: true}
	c := fs.Entry{Name: "c", Path: "/tmp/c", IsDir: true}
	fm := &FileManager{selectedEntries: []fs.Entry{a, b}}
	if got := fm.menuSet(b); len(got) != 2 {
		t.Errorf("menuSet(selected) = %v, want both selected entries", got)
	}
	if got := fm.menuSet(c); len(got) != 1 || got[0].Path != c.Path {
		t.Errorf("menuSet(unselected) = %v, want [c]", got)
	}
}

// TestAlbumImportRoots checks the multi-directory filter: non-local and
// non-directory entries drop out, as do duplicates and nested directories.
func TestAlbumImportRoots(t *testing.T) {
	got := albumImportRoots([]fs.Entry{
		{Name: "music", Path: "/tmp/music", IsDir: true},
		{Name: "sub", Path: "/tmp/music/sub", IsDir: true},
		{Name: "other", Path: "/tmp/other", IsDir: true},
		{Name: "dup", Path: "/tmp/other", IsDir: true},
		{Name: "musicx", Path: "/tmp/musicx", IsDir: true},
		{Name: "song.flac", Path: "/tmp/song.flac"},
		{Name: "tie", Path: "tie:/music", IsDir: true},
	})
	var paths []string
	for _, e := range got {
		paths = append(paths, e.Path)
	}
	want := []string{"/tmp/music", "/tmp/other", "/tmp/musicx"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("albumImportRoots = %v, want %v", paths, want)
	}
}

// TestContextMenuExtractItem checks "Extract archive to here" appears for
// archives only, covers every selected archive, and is withheld from tag-query
// listings (no single "here").
func TestContextMenuExtractItem(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	fm := &FileManager{ops: fs.NewOperations(reg), registry: reg, currentPath: binding.NewString()}
	fm.currentPath.Set("/tmp")

	label := func(e fs.Entry) string {
		for _, item := range fm.contextMenuItems(e, 0) {
			if strings.HasPrefix(item.Label, "Extract") {
				return item.Label
			}
		}
		return ""
	}
	zip := fs.Entry{Name: "a.zip", Path: "/tmp/a.zip"}
	tgz := fs.Entry{Name: "b.tar.gz", Path: "/tmp/b.tar.gz"}
	txt := fs.Entry{Name: "c.txt", Path: "/tmp/c.txt"}

	if got := label(zip); got != "Extract archive to here" {
		t.Errorf("archive: label = %q", got)
	}
	if got := label(txt); got != "" {
		t.Errorf("plain file: unexpected %q", got)
	}
	fm.selectedEntries = []fs.Entry{zip, tgz, txt}
	if got := label(tgz); got != "Extract 2 archives to here" {
		t.Errorf("multi-selection: label = %q", got)
	}
	fm.selectedEntries = nil

	fm.currentPath.Set("tie:/")
	fm.queryInclude = []string{"x"}
	if got := label(fs.Entry{Name: "a.zip", Path: "tie:/a.zip", Hash: "h"}); got != "" {
		t.Errorf("tag query: unexpected %q", got)
	}
}
