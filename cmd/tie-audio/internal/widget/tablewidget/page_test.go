package tablewidget

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

// TestPageClamp: page jumps past the end (and nonsense pages like 0 or huge)
// must clamp to a real page — never a negative Offset or a blank table with a
// bogus page label. An empty listing keeps page 1/1.
func TestPageClamp(t *testing.T) {
	test.NewApp()
	tw := NewTableWidget("t", 10)
	tw.Data = func(offset, limit int) *TableData {
		td := NewTableData("t")
		for i := offset; i < offset+limit && i < 25; i++ {
			td.AddStringCell("Name", string(rune('a'+i%26)))
		}
		return td
	}
	tw.RowCount = func() int { return 25 }
	tw.Refresh() // 3 pages

	tw.currentPage.Set(5000)
	tw.Refresh()
	if p, _ := tw.currentPage.Get(); p != 3 {
		t.Errorf("page after jump = %d, want clamped to 3", p)
	}
	if tw.Offset < 0 || tw.Offset >= 25 {
		t.Errorf("Offset = %d, want inside the listing", tw.Offset)
	}

	tw.currentPage.Set(0)
	tw.Refresh()
	if p, _ := tw.currentPage.Get(); p != 1 {
		t.Errorf("page after 0 = %d, want 1", p)
	}
	if tw.Offset != 0 {
		t.Errorf("Offset after page 0 = %d, want 0", tw.Offset)
	}

	tw.RowCount = func() int { return 0 }
	tw.Refresh()
	if p, _ := tw.currentPage.Get(); p != 1 {
		t.Errorf("page on empty listing = %d, want 1", p)
	}
	if tw.Offset != 0 {
		t.Errorf("Offset on empty listing = %d, want 0", tw.Offset)
	}
	tw.lastPage()
	if p, _ := tw.currentPage.Get(); p != 1 {
		t.Errorf("lastPage on empty listing = %d, want 1", p)
	}
}

// TestRowIndexTranslation: interaction hooks deliver absolute consumer rows
// (page offset + filter mapping), and RowIndex exposes the same translation to
// widget-mode renderers.
func TestRowIndexTranslation(t *testing.T) {
	test.NewApp()
	tw := NewTableWidget("t", 10)
	all := []string{"a", "b", "c", "d", "e", "f"}
	tw.Data = func(offset, limit int) *TableData {
		td := NewTableData("t")
		for i := offset; i < offset+limit && i < len(all); i++ {
			td.AddStringCell("Name", all[i])
		}
		return td
	}
	tw.RowCount = func() int { return len(all) }
	tw.Refresh() // currentData = page rows
	tw.currentFilter = "b"
	tw.filter("b") // keep only "b" (page-relative index 1)
	if got := tw.RowIndex(0); got != 1 {
		t.Errorf("RowIndex through filter = %d, want 1", got)
	}
	tw.Offset = 4
	if got := tw.RowIndex(0); got != 5 {
		t.Errorf("RowIndex with offset = %d, want 5", got)
	}
}
