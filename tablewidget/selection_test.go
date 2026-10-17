package tablewidget

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// The selection rules both consumers depend on: tie-fm's file list and
// tie-audio's queue and browse wall all route taps through these, and until
// now only tie-fm's copy of this package had any tests at all.
//
// selectRow takes the modifiers explicitly; the driver-derived path
// (applySelection) cannot be exercised here because the software test driver
// reports no keyboard.

func selectionTable() *FlexTable {
	td := NewTableData("t")
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		td.AddStringCell("Name", n)
	}
	tbl := NewFlexTable(td, nil)
	tbl.SelectColumn = 0
	return tbl
}

func TestSelectRowPlainReplaces(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()

	tbl.selectRow(2, 0)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("after a plain tap: %v, want [2]", got)
	}
	tbl.selectRow(0, 0)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("a second plain tap must replace, not add: %v, want [0]", got)
	}
}

func TestSelectRowCtrlToggles(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()

	tbl.selectRow(1, fyne.KeyModifierControl)
	tbl.selectRow(3, fyne.KeyModifierControl)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("ctrl-taps gave %v, want [1 3]", got)
	}
	tbl.selectRow(1, fyne.KeyModifierControl)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("ctrl-tapping a selected row must deselect it: %v, want [3]", got)
	}
}

func TestSelectRowShiftRanges(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()

	tbl.selectRow(1, 0) // anchor
	tbl.selectRow(3, fyne.KeyModifierShift)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("forward shift-range = %v, want [1 2 3]", got)
	}
	// Backwards from the same anchor, and the anchor stays put so the range can
	// be re-dragged.
	tbl.selectRow(0, fyne.KeyModifierShift)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("backward shift-range = %v, want [0 1] (shift replaces the selection)", got)
	}
	tbl.selectRow(4, fyne.KeyModifierShift)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatalf("re-dragged shift-range = %v, want [1 2 3 4]: the anchor stayed on row 1", got)
	}
}

func TestSelectRowShiftCtrlAddsToRange(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()

	tbl.selectRow(0, fyne.KeyModifierControl)
	tbl.selectRow(4, fyne.KeyModifierControl)
	tbl.selectRow(2, fyne.KeyModifierShift|fyne.KeyModifierControl)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{0, 2, 3, 4}) {
		t.Fatalf("shift+ctrl should extend without dropping the earlier picks: %v, want [0 2 3 4]", got)
	}
}

func TestSelectRowShiftWithoutAnchorSelectsOne(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()

	tbl.selectRow(3, fyne.KeyModifierShift)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("shift with no anchor = %v, want [3]", got)
	}
}

func TestClearSelection(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()

	tbl.selectRow(1, fyne.KeyModifierControl)
	tbl.selectRow(2, fyne.KeyModifierControl)
	tbl.ClearSelection()
	if got := tbl.SelectedRows(); len(got) != 0 {
		t.Fatalf("after ClearSelection: %v, want none", got)
	}
	// The anchor has to go too, or a later shift-range would pivot off a row the
	// user has already deselected.
	tbl.selectRow(4, fyne.KeyModifierShift)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("shift after ClearSelection = %v, want [4]", got)
	}
}

// RowSelectable lets a consumer make some rows untappable (tie-audio's wall
// marks header rows). A rejected row must not be selected and must not fire any
// callback.
func TestCellTappedRespectsRowSelectable(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()
	tbl.RowSelectable = func(row int) bool { return row != 2 }
	clicks := 0
	tbl.OnClick = func(row int) { clicks++ }

	tbl.cellTapped(2, 0)
	if got := tbl.SelectedRows(); len(got) != 0 {
		t.Errorf("a rejected row was selected: %v", got)
	}
	if clicks != 0 {
		t.Errorf("a rejected row fired OnClick %d times", clicks)
	}

	tbl.cellTapped(1, 0)
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("an accepted row = %v, want [1]", got)
	}
	if clicks != 1 {
		t.Errorf("OnClick fired %d times, want 1", clicks)
	}
}

// SelectColumn splits "select this row" from "activate this row": tie-fm opens
// a file by clicking its name and only toggles the checkbox column.
func TestCellTappedSelectColumnVersusActivate(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()
	var selected, activated []int
	tbl.OnClick = func(row int) { selected = append(selected, row) }
	tbl.OnActivate = func(row int) { activated = append(activated, row) }

	tbl.cellTapped(1, 0) // the select column
	tbl.cellTapped(2, 1) // any other column

	if !reflect.DeepEqual(selected, []int{1}) {
		t.Errorf("select-column taps fired OnClick for %v, want [1]", selected)
	}
	if !reflect.DeepEqual(activated, []int{2}) {
		t.Errorf("other-column taps fired OnActivate for %v, want [2]", activated)
	}
	if got := tbl.SelectedRows(); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("activation must not change the selection: %v, want [1]", got)
	}
}

// Double-tap is detected by hand rather than through fyne.DoubleTappable, which
// makes the driver delay every single tap. Two taps on the same row inside the
// window fire OnDoubleTap once; taps on different rows do not.
func TestCellTappedDoubleTap(t *testing.T) {
	test.NewApp()
	tbl := selectionTable()
	var taps, doubles []int
	tbl.OnClick = func(row int) { taps = append(taps, row) }
	tbl.OnDoubleTap = func(row int) { doubles = append(doubles, row) }

	tbl.cellTapped(1, 0)
	tbl.cellTapped(1, 0)
	if !reflect.DeepEqual(doubles, []int{1}) {
		t.Fatalf("double-tap fired %v, want [1]", doubles)
	}
	if !reflect.DeepEqual(taps, []int{1}) {
		t.Errorf("the second tap must not also fire OnClick: %v, want [1]", taps)
	}

	// A tap elsewhere resets the pairing.
	tbl.cellTapped(3, 0)
	tbl.cellTapped(4, 0)
	if len(doubles) != 1 {
		t.Errorf("taps on different rows paired: %v, want one double-tap", doubles)
	}
}

// TableWidget translates display rows back to page rows for every hook, so an
// in-page filter does not make a click act on the wrong entry.
func TestTableWidgetRowMappingThroughFilter(t *testing.T) {
	test.NewApp()
	page := NewTableData("page")
	names := []string{"alpha", "beta", "gamma", "delta"}
	for _, n := range names {
		page.AddStringCell("Name", n)
	}

	tw := NewTableWidget("t", 10)
	tw.Data = func(offset, limit int) *TableData { return page }
	tw.RowCount = func() int { return len(names) }
	tw.Refresh()

	tw.currentFilter = "am"
	tw.Refresh() // Refresh re-runs the filter against the new criteria

	if got := tw.filteredData.RowCount(); got != 1 {
		t.Fatalf("filter matched %d rows, want 1 (gamma)", got)
	}
	if got := tw.origRow(0); got != 2 {
		t.Fatalf("origRow(0) = %d, want 2 (gamma is page row 2)", got)
	}

	tw.GetFlexTable().selectRow(0, 0)
	if got := tw.SelectedRows(); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("SelectedRows = %v, want [2] (translated back through the filter)", got)
	}

	var selected []int
	tw.OnRowSelected = func(row int) { selected = append(selected, row) }
	tw.GetFlexTable().cellTapped(0, 0)
	if !reflect.DeepEqual(selected, []int{2}) {
		t.Errorf("OnRowSelected got %v, want [2]", selected)
	}

	tw.ClearFilter()
	if got := tw.origRow(3); got != 3 {
		t.Errorf("after ClearFilter, origRow(3) = %d, want 3", got)
	}
	if got := tw.filteredData.RowCount(); got != len(names) {
		t.Errorf("after ClearFilter the table shows %d rows, want %d", got, len(names))
	}
}
