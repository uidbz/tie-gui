package tagselection

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

// SetSelected replaces the list atomically, refreshes it, and — crucially —
// does not fire OnSelectedChanged (it reflects externally loaded state, not
// a user edit). AddSelected keeps firing for user-driven adds.
func TestSetSelected(t *testing.T) {
	test.NewApp()
	w := test.NewWindow(nil)
	ts := NewTagSelection(w)

	fired := 0
	ts.OnSelectedChanged = func() { fired++ }

	ts.SetSelected([]string{"a", "b"})
	if fired != 0 {
		t.Fatalf("SetSelected fired OnSelectedChanged %d times, want 0", fired)
	}
	if got := len(ts.selected); got != 2 {
		t.Fatalf("selected = %d, want 2", got)
	}
	included, _ := ts.SelectedTags()
	if len(included) != 2 || included[0] != "a" || included[1] != "b" {
		t.Fatalf("SelectedTags = %v", included)
	}

	ts.SetSelected([]string{"c"})
	if got := len(ts.selected); got != 1 || ts.selected[0].text != "c" {
		t.Fatalf("after replace: %v", ts.selected)
	}
	if fired != 0 {
		t.Fatalf("SetSelected fired on replace")
	}

	ts.AddSelected(NewTagItemData("d"))
	if fired != 1 {
		t.Fatalf("AddSelected fired %d times, want 1", fired)
	}
}

// RemoveSelected is the out-of-widget path used by the gallery's filter chips
// (the only view of the selection when the sidebar is a drawer). It must fire
// OnSelectedChanged — the removal *is* a user edit — and it must preserve the
// include/exclude sense of the tags it keeps, which SetSelected cannot.
func TestRemoveSelected(t *testing.T) {
	test.NewApp()
	w := test.NewWindow(nil)
	ts := NewTagSelection(w)

	fired := 0
	ts.OnSelectedChanged = func() { fired++ }

	ts.SetSelected([]string{"a", "b", "c"})
	ts.selected[1].include = false // "b" is an exclusion

	if !ts.RemoveSelected("a") {
		t.Fatal(`RemoveSelected("a") = false, want true`)
	}
	if fired != 1 {
		t.Fatalf("RemoveSelected fired OnSelectedChanged %d times, want 1", fired)
	}
	included, excluded := ts.SelectedTags()
	if len(included) != 1 || included[0] != "c" {
		t.Errorf("included = %v, want [c]", included)
	}
	if len(excluded) != 1 || excluded[0] != "b" {
		t.Errorf("excluded = %v, want [b] (the exclusion must survive)", excluded)
	}

	if ts.RemoveSelected("missing") {
		t.Error(`RemoveSelected("missing") = true, want false`)
	}
	if fired != 1 {
		t.Errorf("a no-op removal fired the callback (%d times total)", fired)
	}
}

// Chip mode renders the selection as one chip per tag below the search
// entry, grows its MinSize with the first chip, and reports size changes.
func TestChipSelection(t *testing.T) {
	test.NewApp()
	w := test.NewWindow(nil)
	ts := NewTagChipSelection(w)
	w.SetContent(ts)

	sizeEvents := 0
	ts.OnMinSizeChanged = func() { sizeEvents++ }
	empty := ts.MinSize().Height

	ts.AddSelected(NewTagItemData("rock"))
	ts.AddSelected(NewTagItemData("jazz"))
	ts.AddSelected(NewTagItemData("rock")) // duplicate: ignored
	if got := len(ts.chipBox.Objects); got != 2 {
		t.Fatalf("chips = %d, want 2", got)
	}
	if ts.MinSize().Height <= empty {
		t.Fatalf("MinSize height %v did not grow past empty %v", ts.MinSize().Height, empty)
	}
	if sizeEvents == 0 {
		t.Fatalf("OnMinSizeChanged never fired")
	}

	ts.RemoveSelected("rock")
	included, _ := ts.SelectedTags()
	if len(included) != 1 || included[0] != "jazz" || len(ts.chipBox.Objects) != 1 {
		t.Fatalf("after remove: tags %v, chips %d", included, len(ts.chipBox.Objects))
	}
	ts.ClearSelected()
	if len(ts.chipBox.Objects) != 0 || ts.MinSize().Height != empty {
		t.Fatalf("after clear: chips %d, height %v", len(ts.chipBox.Objects), ts.MinSize().Height)
	}
}
