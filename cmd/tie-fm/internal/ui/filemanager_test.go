package ui

import (
	"testing"

	"fyne.io/fyne/v2/data/binding"

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
