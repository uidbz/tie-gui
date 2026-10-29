package tablewidget

import "testing"

// Numeric cells must sort by their numeric key, not lexicographically (so
// "100 B" does not sort before "9 B").
func TestNumericSort(t *testing.T) {
	td := NewTableData("t")
	rows := []struct {
		name string
		size float64
	}{
		{"a", 100},
		{"b", 9},
		{"c", 1000},
	}
	for _, r := range rows {
		td.AddStringCell("Name", r.name)
		td.AddNumericCell("Size", "", r.size)
	}

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
