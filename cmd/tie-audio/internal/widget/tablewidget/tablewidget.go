package tablewidget

import (
	_ "embed"
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed img/navigate-first.svg
var navigateFirst []byte

//go:embed img/navigate-last.svg
var navigateLast []byte

// TableWidget is a paginated, filterable table. Data is supplied lazily through
// the Data and RowCount callbacks so large backing stores (e.g. a tie tag query)
// are fetched a page at a time.
type TableWidget struct {
	table         *FlexTable
	currentPage   binding.Int
	currentFilter string
	filterEntry   *widget.Entry
	currentData   *TableData
	filteredData  *TableData
	totalPages    binding.Int
	totalResults  binding.Int

	Instance *fyne.Container
	Title    string
	pageSize int
	Offset   int

	// RowCount returns the total number of rows across all pages.
	RowCount func() int
	// Data returns one page of rows for the given offset and limit.
	Data func(offset, limit int) *TableData

	// Row hooks receive the page-relative row index into the data returned by
	// Data (filtering is already translated back to the original row).
	OnRowSelected     func(row int)                        // single click in the select column
	OnRowActivated    func(row int)                        // single click in any other column
	OnRowDoubleTapped func(row int)                        // double click on a row
	OnRowMenu         func(row int, obj fyne.CanvasObject) // secondary click
	// OnDragStart fires once when a drag gesture begins on a row (translated back
	// through in-page filtering). OnDragMove fires on every drag event with the
	// pointer's absolute position. OnDrop fires on release with the grabbed row
	// and the pointer's last absolute position, for hit-testing against another
	// widget.
	OnDragStart func(row int)
	OnDragMove  func(absPos fyne.Position)
	OnDrop      func(row int, absPos fyne.Position)
	// OnReorder fires when a row is dragged to a new position within the table,
	// with both indices translated back through in-page filtering. When set it
	// takes precedence over OnDrop.
	OnReorder func(from, to int)
}

func (ctx *TableWidget) GetFlexTable() *FlexTable {
	return ctx.table
}

func (ctx *TableWidget) prevPage() {
	page, err := ctx.currentPage.Get()
	if err != nil {
		return
	}
	page -= 1
	if page < 1 {
		return
	}
	ctx.currentPage.Set(page)
	ctx.Refresh()
}

func (ctx *TableWidget) nextPage() {
	page, err := ctx.currentPage.Get()
	if err != nil {
		return
	}
	page += 1
	max, err := ctx.totalPages.Get()
	if err != nil || page > max {
		return
	}
	ctx.currentPage.Set(page)
	ctx.Refresh()
}

func (ctx *TableWidget) firstPage() {
	ctx.currentPage.Set(1)
	ctx.Refresh()
}

// ResetPage jumps back to page 1. Callers use it when the underlying listing is
// replaced (e.g. a directory change) so a stale page number does not land past
// the end of a shorter listing.
func (ctx *TableWidget) ResetPage() {
	ctx.currentPage.Set(1)
}

func (ctx *TableWidget) lastPage() {
	max, err := ctx.totalPages.Get()
	if err != nil {
		return
	}
	ctx.currentPage.Set(max)
	ctx.Refresh()
}

func NewTableWidget(title string, pageSize int) *TableWidget {
	ctx := &TableWidget{
		currentPage:  binding.NewInt(),
		totalPages:   binding.NewInt(),
		totalResults: binding.NewInt(),
		Title:        title,
		pageSize:     pageSize,
	}

	ctx.currentPage.Set(1)
	ctx.table = NewFlexTable(NewTableData("empty"), ctx.cellOnClick)
	ctx.table.OnClickSecondary = ctx.cellOnSecondaryClick
	ctx.table.OnActivate = ctx.cellOnActivate
	ctx.table.OnDoubleTap = ctx.cellOnDoubleTap
	ctx.table.OnDragStart = ctx.cellOnDragStart
	ctx.table.OnDragMove = ctx.cellOnDragMove
	ctx.table.OnDrop = ctx.cellOnDrop
	ctx.table.OnReorder = ctx.cellOnReorder

	currentPage := widget.NewEntryWithData(binding.IntToString(ctx.currentPage))
	currentPage.Validator = nil
	currentPage.OnSubmitted = func(s string) {
		i, err := strconv.Atoi(s)
		if err != nil {
			return
		}
		ctx.currentPage.Set(i)
		ctx.Refresh()
	}
	totalPages := widget.NewLabelWithData(binding.IntToString(ctx.totalPages))
	first := theme.NewThemedResource(fyne.NewStaticResource("navigateFirst", navigateFirst))
	last := theme.NewThemedResource(fyne.NewStaticResource("navigateLast", navigateLast))

	leftFooter := container.NewHBox(
		widget.NewButtonWithIcon("", first, ctx.firstPage),
		widget.NewButtonWithIcon("", theme.NavigateBackIcon(), ctx.prevPage),
		currentPage,
		widget.NewLabel("/"),
		totalPages,
		widget.NewButtonWithIcon("", theme.NavigateNextIcon(), ctx.nextPage),
		widget.NewButtonWithIcon("", last, ctx.lastPage),
		widget.NewLabel("Count:"),
		widget.NewLabelWithData(binding.IntToString(ctx.totalResults)),
	)
	footer := container.NewBorder(ctx.newFilterWidget(), nil, leftFooter, nil)

	ctx.table.SelectionColor = color.RGBA{85, 85, 85, 128}
	ctx.Instance = container.NewBorder(nil, footer, nil, nil, container.NewPadded(ctx.table))
	ctx.Data = func(offset, limit int) *TableData { return NewTableData("empty") }
	ctx.RowCount = func() int { return 0 }
	ctx.Refresh()

	return ctx
}

func (ctx *TableWidget) Refresh() {
	page, err := ctx.currentPage.Get()
	if err != nil {
		return
	}
	ctx.Offset = (page - 1) * ctx.pageSize
	data := ctx.Data(ctx.Offset, ctx.pageSize)
	totalCount := ctx.RowCount()

	ctx.totalResults.Set(totalCount)
	ctx.totalPages.Set((totalCount + ctx.pageSize - 1) / ctx.pageSize)

	ctx.currentData = data
	if ctx.currentFilter != "" {
		ctx.filter(ctx.currentFilter)
		ctx.table.SetData(ctx.filteredData)
	} else {
		ctx.table.SetData(data)
	}
	ctx.table.Refresh()
}

func (ctx *TableWidget) SetColumnWidth(id int, width float32) {
	ctx.table.SetColumnWidth(id, width)
}

// origRow maps a row index in the currently displayed table (which may be
// filtered) back to the row index in the unfiltered page returned by Data.
func (ctx *TableWidget) origRow(displayRow int) int {
	if ctx.currentFilter != "" && ctx.filteredData != nil &&
		ctx.filteredData.RowMapping != nil && displayRow < len(ctx.filteredData.RowMapping) {
		return ctx.filteredData.RowMapping[displayRow]
	}
	return displayRow
}

// SelectedRows returns the selected rows as indices into the page returned by
// Data (in-page filtering is translated back via origRow).
func (ctx *TableWidget) SelectedRows() []int {
	rows := ctx.table.SelectedRows()
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = ctx.origRow(r)
	}
	return out
}

// ClearSelection deselects all rows.
func (ctx *TableWidget) ClearSelection() { ctx.table.ClearSelection() }

func (ctx *TableWidget) cellOnClick(row int) {
	if ctx.OnRowSelected != nil {
		ctx.OnRowSelected(ctx.origRow(row))
	}
}

func (ctx *TableWidget) cellOnActivate(row int) {
	if ctx.OnRowActivated != nil {
		ctx.OnRowActivated(ctx.origRow(row))
	}
}

func (ctx *TableWidget) cellOnDoubleTap(row int) {
	if ctx.OnRowDoubleTapped != nil {
		ctx.OnRowDoubleTapped(ctx.origRow(row))
	}
}

func (ctx *TableWidget) cellOnSecondaryClick(row int, obj fyne.CanvasObject) {
	if ctx.OnRowMenu != nil {
		ctx.OnRowMenu(ctx.origRow(row), obj)
	}
}

func (ctx *TableWidget) cellOnDragStart(row int) {
	if ctx.OnDragStart != nil {
		ctx.OnDragStart(ctx.origRow(row))
	}
}

func (ctx *TableWidget) cellOnDragMove(absPos fyne.Position) {
	if ctx.OnDragMove != nil {
		ctx.OnDragMove(absPos)
	}
}

func (ctx *TableWidget) cellOnDrop(row int, absPos fyne.Position) {
	if ctx.OnDrop != nil {
		ctx.OnDrop(ctx.origRow(row), absPos)
	}
}

func (ctx *TableWidget) cellOnReorder(from, to int) {
	if ctx.OnReorder != nil {
		ctx.OnReorder(ctx.origRow(from), ctx.origRow(to))
	}
}

func (ctx *TableWidget) filter(text string) {
	ctx.filteredData = NewTableData("filtered data")
	ctx.filteredData.Columns = append([]string{}, ctx.currentData.Columns...)
	ctx.filteredData.RowMapping = []int{}

	for i := 0; i < ctx.currentData.RowCount(); i++ {
		stringRow := make([]string, ctx.currentData.ColumnCount())
		found := false
		for j := 0; j < ctx.currentData.ColumnCount(); j++ {
			val := ctx.currentData.Get(j, i)
			if !found && contains(val, text) {
				found = true
			}
			stringRow[j] = val
		}
		if found {
			ctx.filteredData.AddStringRow(ctx.currentData.Columns, stringRow)
			ctx.filteredData.RowMapping = append(ctx.filteredData.RowMapping, i)
		}
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// ClearFilter removes any active in-page filter and empties the filter box.
// Callers use it when the underlying listing changes (e.g. a directory change)
// so a stale filter does not silently hide rows in the new listing.
func (ctx *TableWidget) ClearFilter() {
	ctx.currentFilter = ""
	ctx.filteredData = ctx.currentData
	if ctx.filterEntry != nil {
		ctx.filterEntry.SetText("")
	}
}

func (ctx *TableWidget) newFilterWidget() *widget.Entry {
	filter := widget.NewEntry()
	ctx.filterEntry = filter
	filter.PlaceHolder = "Filter in page"
	filter.OnChanged = func(text string) {
		if text == "" {
			ctx.filteredData = ctx.currentData
			ctx.currentFilter = ""
		} else {
			ctx.currentFilter = text
			ctx.filter(text)
		}
		ctx.table.SetData(ctx.filteredData)
		ctx.table.Refresh()
	}

	return filter
}
