package tablewidget

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

// The filter/page footer is hidden for listings that fit on one page, appears
// automatically when pagination is needed, and can be revealed on demand for
// the in-page filter.
func TestFooterVisibility(t *testing.T) {
	test.NewApp()
	tw := NewTableWidget("t", 2000)
	tw.Data = func(offset, limit int) *TableData { return NewTableData("t") }
	tw.RowCount = func() int { return 2000 }

	tw.Refresh()
	if !tw.footer.Hidden {
		t.Fatal("footer shown for a single-page listing")
	}

	tw.RowCount = func() int { return 2001 }
	tw.Refresh()
	if tw.footer.Hidden {
		t.Fatal("footer hidden for a multi-page listing")
	}

	tw.RowCount = func() int { return 1 }
	tw.Refresh()
	if !tw.footer.Hidden {
		t.Fatal("footer stayed visible after returning to one page")
	}

	tw.ShowFilter()
	if tw.footer.Hidden {
		t.Fatal("ShowFilter did not reveal the footer")
	}

	tw.ClearFilter()
	tw.Refresh()
	if !tw.footer.Hidden {
		t.Fatal("footer stayed visible after ClearFilter on a one-page listing")
	}
}
