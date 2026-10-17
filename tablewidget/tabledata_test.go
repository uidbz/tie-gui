package tablewidget

import (
	"reflect"
	"testing"
)

// threeRows is the small fixture both consumers' behaviour is checked against:
// a name column, a numeric column whose display text and sort key differ, and
// a column that is deliberately shorter than the others.
func threeRows() *TableData {
	td := NewTableData("t")
	td.AddStringCell("Name", "a")
	td.AddNumericCell("Size", "100 B", 100)
	td.AddStringCell("Name", "b")
	td.AddNumericCell("Size", "9 B", 9)
	td.AddStringCell("Name", "c")
	td.AddNumericCell("Size", "1000 B", 1000)
	return td
}

// Numeric cells must sort by their numeric key, not lexicographically (so
// "100 B" does not sort before "9 B").
func TestNumericSort(t *testing.T) {
	td := threeRows()

	td.Sort("Size", true)
	wantAsc := []string{"b", "a", "c"} // 9, 100, 1000
	for i, w := range wantAsc {
		if got := td.GetFromColumn("Name", i); got != w {
			t.Errorf("asc row %d = %q, want %q", i, got, w)
		}
	}

	td.Sort("Size", false)
	wantDesc := []string{"c", "a", "b"}
	for i, w := range wantDesc {
		if got := td.GetFromColumn("Name", i); got != w {
			t.Errorf("desc row %d = %q, want %q", i, got, w)
		}
	}
}

// Sorting is a row operation: every column has to move with the sort column,
// otherwise a size ends up next to the wrong name.
func TestSortKeepsRowTuplesAligned(t *testing.T) {
	td := NewTableData("t")
	td.AddStringCell("Name", "gamma")
	td.AddStringCell("Ext", "mp3")
	td.AddNumericCell("Size", "3", 3)
	td.AddStringCell("Name", "alpha")
	td.AddStringCell("Ext", "flac")
	td.AddNumericCell("Size", "1", 1)
	td.AddStringCell("Name", "beta")
	td.AddStringCell("Ext", "ogg")
	td.AddNumericCell("Size", "2", 2)

	td.Sort("Name", true)
	want := []struct{ name, ext, size string }{
		{"alpha", "flac", "1"},
		{"beta", "ogg", "2"},
		{"gamma", "mp3", "3"},
	}
	for i, w := range want {
		if got := td.GetFromColumn("Name", i); got != w.name {
			t.Fatalf("row %d name = %q, want %q", i, got, w.name)
		}
		if got := td.GetFromColumn("Ext", i); got != w.ext {
			t.Errorf("row %d ext = %q, want %q (column did not move with the sort)", i, got, w.ext)
		}
		if got := td.GetFromColumn("Size", i); got != w.size {
			t.Errorf("row %d size = %q, want %q", i, got, w.size)
		}
	}
}

// Mixed cell types in the sort column: an empty cell must sort last in both
// directions. Less has to stay antisymmetric or sort.Sort corrupts the table.
// The empty cell comes from a column that is shorter than rowCount, which Sort
// pads with cellIsEmpty.
func TestSortEmptyCellsAlwaysLast(t *testing.T) {
	td := NewTableData("t")
	td.AddStringCell("Name", "b")
	td.AddNumericCell("Size", "5", 5)
	td.AddStringCell("Name", "a")
	td.AddNumericCell("Size", "7", 7)
	td.AddStringCell("Name", "gap") // no Size for this row

	for _, asc := range []bool{true, false} {
		td.Sort("Size", asc)
		if got := td.GetFromColumn("Size", td.RowCount()-1); got != "" {
			t.Errorf("ascending=%v: last Size row = %q, want the empty cell", asc, got)
		}
		names := []string{td.GetFromColumn("Name", 0), td.GetFromColumn("Name", 1)}
		if asc && !reflect.DeepEqual(names, []string{"b", "a"}) {
			t.Errorf("ascending: names = %v, want [b a] (5 before 7)", names)
		}
		if !asc && !reflect.DeepEqual(names, []string{"a", "b"}) {
			t.Errorf("descending: names = %v, want [a b] (7 before 5)", names)
		}
		if got := td.GetFromColumn("Name", 2); got != "gap" {
			t.Errorf("ascending=%v: row 2 = %q, want the empty-size row to stay last", asc, got)
		}
	}
}

// A sort on a column the data does not have must be a no-op. It used to leave
// td.data[column] nil and panic in Less.
func TestSortUnknownColumnIsNoOp(t *testing.T) {
	td := threeRows()
	before := []string{td.GetFromColumn("Name", 0), td.GetFromColumn("Name", 1), td.GetFromColumn("Name", 2)}

	td.Sort("Nope", true)

	after := []string{td.GetFromColumn("Name", 0), td.GetFromColumn("Name", 1), td.GetFromColumn("Name", 2)}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("sorting by an absent column reordered the rows: %v -> %v", before, after)
	}
	if td.RowCount() != 3 {
		t.Errorf("RowCount = %d after a no-op sort, want 3", td.RowCount())
	}
}

// A column shorter than rowCount must be padded before sorting, or the sorter
// indexes past its end and only part of the table is ordered.
func TestSortPadsShortColumns(t *testing.T) {
	td := NewTableData("t")
	td.AddStringCell("Name", "b")
	td.AddStringCell("Only", "solo") // this column has one row only
	td.AddStringCell("Name", "a")
	td.Sort("Name", true)

	if td.RowCount() != 2 {
		t.Fatalf("RowCount = %d, want 2", td.RowCount())
	}
	if got := td.GetFromColumn("Name", 0); got != "a" {
		t.Errorf("row 0 = %q, want a", got)
	}
	// "solo" belonged to b, so it must travel with b to row 1; the padded cell
	// belongs to a and lands at row 0.
	if got := td.GetFromColumn("Only", 1); got != "solo" {
		t.Errorf("row 1 Only = %q, want solo (the short column must move with its row)", got)
	}
	if got := td.GetFromColumn("Only", 0); got != "" {
		t.Errorf("row 0 Only = %q, want the padded empty cell", got)
	}
}

func TestClearResetsTable(t *testing.T) {
	td := threeRows()
	td.Clear()
	if td.RowCount() != 0 || td.ColumnCount() != 0 {
		t.Errorf("after Clear: %d rows, %d columns; want 0,0", td.RowCount(), td.ColumnCount())
	}
	if got := td.GetFromColumn("Name", 0); got != "" {
		t.Errorf("GetFromColumn after Clear = %q, want empty", got)
	}
}

// InsertStringCell past the current end has to fill the gap with empty cells so
// row indices stay meaningful for every column.
func TestInsertStringCellPadsGap(t *testing.T) {
	td := NewTableData("t")
	td.AddStringCell("Name", "zero")
	td.InsertStringCell("Name", 3, "three")

	if td.RowCount() != 4 {
		t.Fatalf("RowCount = %d, want 4", td.RowCount())
	}
	for i, want := range []string{"zero", "", "", "three"} {
		if got := td.GetFromColumn("Name", i); got != want {
			t.Errorf("row %d = %q, want %q", i, got, want)
		}
	}
}

func TestGetOutOfRange(t *testing.T) {
	td := threeRows()
	if got := td.Get(99, 0); got != "" {
		t.Errorf("Get(99,0) = %q, want empty", got)
	}
	if got := td.GetFromColumn("Name", 99); got != "" {
		t.Errorf("GetFromColumn(Name,99) = %q, want empty", got)
	}
	if got := td.GetFromColumn("Nope", 0); got != "" {
		t.Errorf("GetFromColumn(Nope,0) = %q, want empty", got)
	}
	if got := len(td.GetRows("Nope")); got != 0 {
		t.Errorf("GetRows(Nope) returned %d cells, want 0", got)
	}
}

func TestRenameColumnKeepsCells(t *testing.T) {
	td := threeRows()
	td.RenameColumn("Size", "Bytes")
	if got := td.GetFromColumn("Bytes", 0); got != "100 B" {
		t.Errorf("renamed column row 0 = %q, want 100 B", got)
	}
	if got := td.GetFromColumn("Size", 0); got != "" {
		t.Errorf("old column still holds %q", got)
	}
	td.Sort("Bytes", true)
	if got := td.GetFromColumn("Name", 0); got != "b" {
		t.Errorf("sorting the renamed column put %q first, want b", got)
	}
}

func TestSetIdsValidates(t *testing.T) {
	td := threeRows()
	if err := td.SetIds([]string{"t", "1", "2"}); err == nil {
		t.Error("SetIds accepted the wrong number of ids")
	}
	if err := td.SetIds([]string{"t", "1", "2", "3"}); err != nil {
		t.Fatalf("SetIds: %v", err)
	}
	if td.TableId != "t" || len(td.RowIds) != 3 {
		t.Errorf("ids not applied: table=%q rows=%v", td.TableId, td.RowIds)
	}
}
