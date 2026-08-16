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
