package ui

import (
	"testing"

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
