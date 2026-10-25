package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/test"

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

// menuLabels lists a menu's item labels, for assertions.
func menuLabels(items []*fyne.MenuItem) []string {
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.Label
	}
	return labels
}

// TestDeleteEntriesRemovesSelection checks a multi-item delete removes every
// selected local entry (leaving unselected ones) and runs off the UI goroutine.
func TestDeleteEntriesRemovesSelection(t *testing.T) {
	test.NewApp()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	fm := NewFileManager(dir, reg, fs.NewOperations(reg), nil, nil)

	var set []fs.Entry
	for _, e := range fm.entries {
		if e.Name != "keep.txt" {
			set = append(set, e)
		}
	}
	if len(set) != 2 {
		t.Fatalf("selection = %v, want the two deletable files", set)
	}
	fm.deleteEntries(set)

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, errA := os.Stat(filepath.Join(dir, "a.txt"))
		_, errB := os.Stat(filepath.Join(dir, "b.txt"))
		if errA != nil && errB != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("selected files still present (a err=%v, b err=%v)", errA, errB)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("unselected file was deleted: %v", err)
	}
}

// TestContextMenuDeleteUsesSelection checks Delete acts on the whole selection
// (with a counted label) when the clicked row is part of it, and on just the
// clicked row otherwise — for local and tie listings alike.
func TestContextMenuDeleteUsesSelection(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	localA := fs.Entry{Name: "a.txt", Path: "/tmp/a.txt"}
	localB := fs.Entry{Name: "b.txt", Path: "/tmp/b.txt"}
	fm := &FileManager{
		ops:             fs.NewOperations(reg),
		registry:        reg,
		selectedEntries: []fs.Entry{localA, localB},
	}

	got := menuLabels(fm.contextMenuItems(localA, 0))
	if !slices.Contains(got, "Delete 2 items") {
		t.Errorf("local menu = %v, want Delete 2 items", got)
	}

	// Right-clicking a row outside the selection acts on that row alone.
	localC := fs.Entry{Name: "c.txt", Path: "/tmp/c.txt"}
	got = menuLabels(fm.contextMenuItems(localC, 2))
	if !slices.Contains(got, "Delete") {
		t.Errorf("local menu for an unselected row = %v, want Delete", got)
	}

	// tie rows go through fs.Remover and support the same multi-selection.
	tieA := fs.Entry{Name: "a.flac", Path: "tie:/music/a.flac", Hash: "h1"}
	tieB := fs.Entry{Name: "b.flac", Path: "tie:/music/b.flac", Hash: "h2"}
	fm.selectedEntries = []fs.Entry{tieA, tieB}
	got = menuLabels(fm.contextMenuItems(tieA, 0))
	if !slices.Contains(got, "Delete 2 items") {
		t.Errorf("tie menu = %v, want Delete 2 items", got)
	}
}

// TestDeletePromptText checks the confirmation wording: single items keep their
// name, multi-item deletes count and list the names, directories are called
// out, and tie deletes explain that only references go.
func TestDeletePromptText(t *testing.T) {
	localFile := fs.Entry{Name: "a.txt", Path: "/tmp/a.txt"}
	localDir := fs.Entry{Name: "images", Path: "/tmp/images", IsDir: true}
	tieFile := fs.Entry{Name: "a.flac", Path: "tie:/music/a.flac", Hash: "h1"}
	tieDir := fs.Entry{Name: "album", Path: "tie:/music/album", IsDir: true, Hash: "h2"}

	if got, want := deletePromptText([]fs.Entry{localFile}), `Delete "a.txt"?`; got != want {
		t.Errorf("local single = %q, want %q", got, want)
	}
	if got, want := deletePromptText([]fs.Entry{tieFile}), "Remove the file \"a.flac\" from tie?\n\n"+tieDeleteNote; got != want {
		t.Errorf("tie single file = %q, want %q", got, want)
	}
	if got, want := deletePromptText([]fs.Entry{tieDir}), "Remove the directory and everything below it \"album\" from tie?\n\n"+tieDeleteNote; got != want {
		t.Errorf("tie single dir = %q, want %q", got, want)
	}

	got := deletePromptText([]fs.Entry{localFile, localDir})
	for _, want := range []string{`Delete 2 items?`, `"a.txt"`, `"images/"`, "Directories are removed with everything below them."} {
		if !strings.Contains(got, want) {
			t.Errorf("local multi prompt = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "tie") {
		t.Errorf("local multi prompt mentions tie: %q", got)
	}

	got = deletePromptText([]fs.Entry{tieFile, tieDir})
	for _, want := range []string{`Remove 2 items from tie?`, `"a.flac"`, `"album/"`, tieDeleteNote} {
		if !strings.Contains(got, want) {
			t.Errorf("tie multi prompt = %q, want it to contain %q", got, want)
		}
	}

	// Long selections elide the tail.
	many := make([]fs.Entry, 10)
	for i := range many {
		many[i] = fs.Entry{Name: fmt.Sprintf("f%d", i), Path: fmt.Sprintf("/tmp/f%d", i)}
	}
	if got := deletePromptText(many); !strings.Contains(got, "and 2 more") {
		t.Errorf("long selection prompt = %q, want an elided tail", got)
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

// TestContextMenuNewFileAndTerminal checks the location actions: "New file…"
// appears wherever the backend can create files (local, not tie), and "Open
// in terminal" appears for local entries only.
func TestContextMenuNewFileAndTerminal(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	fm := &FileManager{ops: fs.NewOperations(reg), registry: reg, currentPath: binding.NewString()}
	fm.currentPath.Set("/tmp")

	hasItem := func(items []*fyne.MenuItem, label string) bool {
		for _, item := range items {
			if item.Label == label {
				return true
			}
		}
		return false
	}

	local := fm.contextMenuItems(fs.Entry{Name: "a.txt", Path: "/tmp/a.txt"}, 0)
	if !hasItem(local, "New file…") {
		t.Error("local menu lacks New file…")
	}
	if !hasItem(local, "Open Terminal Here") {
		t.Error("local menu lacks Open in terminal")
	}

	fm.currentPath.Set("tie:/music")
	tie := fm.contextMenuItems(fs.Entry{Name: "a.txt", Path: "tie:/music/a.txt", Hash: "h"}, 0)
	if hasItem(tie, "New file…") {
		t.Error("tie menu unexpectedly offers New file…")
	}
	if hasItem(tie, "Open Terminal Here") {
		t.Error("tie menu unexpectedly offers Open in terminal")
	}
}

// TestNewFileCreatesFile runs the New file flow against the local backend.
func TestNewFileCreatesFile(t *testing.T) {
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	maker, ok := reg.For("/tmp").(fs.FileMaker)
	if !ok {
		t.Fatal("LocalFS must implement fs.FileMaker")
	}
	dir := t.TempDir()
	if err := maker.CreateFile(dir, "notes.txt"); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatalf("file missing: %v", err)
	}
	// An existing name must fail rather than clobber.
	if err := maker.CreateFile(dir, "notes.txt"); err == nil {
		t.Fatal("CreateFile over an existing file must fail")
	}
}

// TestConflictVerdict checks the comparison line of the conflict dialog.
func TestConflictVerdict(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	if got := conflictVerdict(fs.Entry{ModTime: now, Size: 10}, fs.Entry{ModTime: old, Size: 5}); got != "The source is more recent." {
		t.Errorf("verdict = %q, want more recent", got)
	}
	if got := conflictVerdict(fs.Entry{ModTime: old, Size: 10}, fs.Entry{ModTime: now, Size: 5}); got != "The source is older." {
		t.Errorf("verdict = %q, want older", got)
	}
	if got := conflictVerdict(fs.Entry{ModTime: now, Size: 4}, fs.Entry{ModTime: now, Size: 10}); got != "The source is smaller by 6 B." {
		t.Errorf("verdict = %q, want smaller by 6 B", got)
	}
	if got := conflictVerdict(fs.Entry{ModTime: now, Size: 10}, fs.Entry{ModTime: now, Size: 10}); got != "The files seem identical." {
		t.Errorf("verdict = %q, want identical", got)
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

// TestTerminalToolbarItemState checks the labelled toolbar action exists and
// follows the location: enabled for local paths, disabled for tie: paths.
func TestTerminalToolbarItemState(t *testing.T) {
	dir := t.TempDir()
	reg := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(nil))
	fm := NewFileManager(dir, reg, fs.NewOperations(reg), nil, nil)
	if fm.terminalButton == nil {
		t.Fatal("terminal toolbar item missing")
	}
	if got := fm.terminalButton.Text; got != "Open Terminal Here" {
		t.Errorf("toolbar label = %q", got)
	}
	if fm.terminalButton.Disabled() {
		t.Error("terminal action disabled for a local directory")
	}

	fm.currentPath.Set("tie:/music")
	fm.updateTerminalButton()
	if !fm.terminalButton.Disabled() {
		t.Error("terminal action enabled for a tie: path")
	}

	fm.currentPath.Set(dir)
	fm.updateTerminalButton()
	if fm.terminalButton.Disabled() {
		t.Error("terminal action stayed disabled after returning to a local path")
	}
}
