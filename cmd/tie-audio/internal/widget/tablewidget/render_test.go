package tablewidget

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// tableHarness builds a widget-mode table (like tie-fm's file pane) in a
// headless window, backed by a mutable row list.
type tableHarness struct {
	tw   *TableWidget
	win  fyne.Window
	rows *[]string
}

func newTableHarness(t *testing.T, pageSize int, initial []string) *tableHarness {
	t.Helper()
	test.NewApp()
	h := &tableHarness{rows: &initial}
	h.tw = NewTableWidget("files", pageSize)
	h.tw.RowCount = func() int { return len(*h.rows) }
	h.tw.Data = func(offset, limit int) *TableData {
		rows := *h.rows
		td := NewTableData("files")
		for i := offset; i < len(rows) && i < offset+limit; i++ {
			td.AddStringCell("Name", rows[i])
		}
		return td
	}
	ft := h.tw.GetFlexTable()
	ft.SetCreateCell(func(col, row int) fyne.CanvasObject {
		l := widget.NewLabel("")
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	})
	ft.SetUpdateCell(func(col, row int, obj fyne.CanvasObject) {
		if l, ok := obj.(*widget.Label); ok {
			l.SetText(ft.CellText(col, row))
		}
	})

	h.win = test.NewWindow(container.NewMax(h.tw.Instance))
	h.win.Resize(fyne.NewSize(600, 400))
	h.tw.Refresh()
	return h
}

// setRows replaces the listing and refreshes, like a directory change.
func (h *tableHarness) setRows(rows []string) {
	*h.rows = rows
	h.tw.ResetPage()
	h.tw.Refresh()
}

// cells returns the visible widget-mode cells. Cells are collected in display
// order (row by row), so the first cell is the top-left one.
func (h *tableHarness) cells() []*WidgetCell {
	var cells []*WidgetCell
	collectWidgetCells(h.tw.GetFlexTable(), &cells)
	return cells
}

// collectWidgetCells walks the rendered object tree and collects widget-mode
// cells.
func collectWidgetCells(obj fyne.CanvasObject, out *[]*WidgetCell) {
	if wc, ok := obj.(*WidgetCell); ok {
		*out = append(*out, wc)
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, ch := range c.Objects {
			collectWidgetCells(ch, out)
		}
		return
	}
	if w, ok := obj.(fyne.Widget); ok {
		if r := test.WidgetRenderer(w); r != nil {
			for _, ch := range r.Objects() {
				collectWidgetCells(ch, out)
			}
		}
	}
}

func rowNames(prefix string, n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("%s-%d", prefix, i)
	}
	return rows
}

// TestTableCellsLaidOutOnDataSwap is the regression test for a directory change
// rendering a blank table: cells recycled from the old listing keep their size,
// so their newly created content never went through a layout pass and stayed
// 0x0 until the window was resized or the table was scrolled.
func TestTableCellsLaidOutOnDataSwap(t *testing.T) {
	h := newTableHarness(t, 2000, rowNames("a", 30))

	check := func(tag string) {
		cells := h.cells()
		if len(cells) == 0 {
			t.Fatalf("%s: no visible cells", tag)
		}
		for _, c := range cells {
			if c.content == nil {
				t.Fatalf("%s: cell %v has no content", tag, c.currentID)
			}
			if s := c.content.Size(); s.Width <= 0 || s.Height <= 0 {
				t.Fatalf("%s: cell %v content not laid out: %v", tag, c.currentID, s)
			}
		}
	}
	check("initial listing")

	h.setRows(rowNames("b", 5))
	check("shorter listing after a directory change")

	h.setRows(rowNames("c", 30))
	check("same-size listing after a directory change")

	// An empty directory: no rows, no columns in the page data.
	h.setRows(nil)
	if cells := h.cells(); len(cells) != 0 {
		t.Fatalf("empty listing still shows %d cells", len(cells))
	}

	// And back to a populated listing.
	h.setRows(rowNames("d", 3))
	check("listing after an empty directory")
}

// TestResetPageScrollsToTop checks a reused table is scrolled back to its first
// row: a stale offset from a longer previous listing otherwise leaves the new
// listing showing its tail (or nothing, when it is shorter than the viewport).
func TestResetPageScrollsToTop(t *testing.T) {
	h := newTableHarness(t, 2000, rowNames("a", 100))

	// Scroll to the end of the long listing.
	h.tw.GetFlexTable().table.ScrollToBottom()
	if cells := h.cells(); len(cells) > 0 && cells[0].currentID.Row == 0 {
		t.Fatal("table did not scroll; test setup is wrong")
	}

	h.setRows(rowNames("b", 100))

	cells := h.cells()
	if len(cells) == 0 {
		t.Fatal("no visible cells after changing directory")
	}
	if got := cells[0].currentID.Row; got != 0 {
		t.Fatalf("top visible row after a directory change = %d, want 0", got)
	}
	if l, ok := cells[0].content.(*widget.Label); !ok || l.Text != "b-0" {
		t.Fatalf("top row = %v, want b-0", cells[0].content)
	}
}

// TestPageChangeScrollsToTop checks flipping to the next page starts at its
// first row too.
func TestPageChangeScrollsToTop(t *testing.T) {
	var rows []string
	for i := 0; i < 12; i++ {
		rows = append(rows, fmt.Sprintf("r-%d", i))
	}
	h := newTableHarness(t, 5, rows)
	h.tw.GetFlexTable().table.ScrollToBottom()

	h.tw.nextPage()

	cells := h.cells()
	if len(cells) == 0 {
		t.Fatal("no visible cells on the next page")
	}
	if got := cells[0].currentID.Row; got != 0 {
		t.Fatalf("top visible row on the next page = %d, want 0", got)
	}
	if l, ok := cells[0].content.(*widget.Label); !ok || l.Text != "r-5" {
		t.Fatalf("top row = %v, want r-5", cells[0].content)
	}
}

// TestStringCellsLaidOut checks the plain (non-widget) cell mode lays its label
// out on first render, for the same vendored-Clip reason as the widget cells.
func TestStringCellsLaidOut(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)
	win.Resize(fyne.NewSize(600, 400))

	td := NewTableData("t")
	for i := 0; i < 5; i++ {
		td.AddStringCell("Name", fmt.Sprintf("a-%d", i))
	}
	ft := NewFlexTable(td, nil)
	win.SetContent(container.NewMax(ft))
	win.Resize(fyne.NewSize(601, 401))

	var cells []*TableCell
	collectStringCells(ft, &cells)
	if len(cells) == 0 {
		t.Fatal("no visible cells")
	}
	for _, c := range cells {
		if s := c.label.Size(); s.Width <= 0 || s.Height <= 0 {
			t.Fatalf("cell %v label not laid out: %v", c.Id, s)
		}
	}
}

// collectStringCells walks the rendered object tree and collects plain cells.
func collectStringCells(obj fyne.CanvasObject, out *[]*TableCell) {
	if tc, ok := obj.(*TableCell); ok {
		*out = append(*out, tc)
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, ch := range c.Objects {
			collectStringCells(ch, out)
		}
		return
	}
	if w, ok := obj.(fyne.Widget); ok {
		if r := test.WidgetRenderer(w); r != nil {
			for _, ch := range r.Objects() {
				collectStringCells(ch, out)
			}
		}
	}
}
