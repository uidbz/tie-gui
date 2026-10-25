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
	pageEntry     *widget.Entry
	currentData   *TableData
	filteredData  *TableData
	totalPages    binding.Int
	totalResults  binding.Int
	footer        *fyne.Container
	filterShown   bool

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
	OnRowSelected  func(row int)                        // single click in the select column
	OnRowActivated func(row int)                        // single click in any other column
	OnRowMenu      func(row int, obj fyne.CanvasObject) // secondary click
	// OnDragStart fires once when a drag gesture begins on a row (translated back
	// through in-page filtering). OnDragMove fires on every drag event with the
	// pointer's absolute position. OnDrop fires on release with the grabbed row
	// and the pointer's last absolute position, for hit-testing against another
	// widget.
	OnDragStart func(row int)
	OnDragMove  func(absPos fyne.Position)
	OnDrop      func(row int, absPos fyne.Position)
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
	ctx.ScrollToTop()
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
	ctx.ScrollToTop()
}

func (ctx *TableWidget) firstPage() {
	ctx.currentPage.Set(1)
	ctx.Refresh()
	ctx.ScrollToTop()
}

// ResetPage jumps back to page 1. Callers use it when the underlying listing is
// replaced (e.g. a directory change) so a stale page number does not land past
// the end of a shorter listing. The scroll position is reset too: the new
// listing starts at its first row.
func (ctx *TableWidget) ResetPage() {
	ctx.currentPage.Set(1)
	ctx.ScrollToTop()
}

// ScrollToTop scrolls the current page back to its first row, so a stale scroll
// offset from the previous listing (or the previous page/filter) cannot leave
// the table showing its tail or a blank region.
func (ctx *TableWidget) ScrollToTop() {
	ctx.table.ScrollToTop()
}

func (ctx *TableWidget) lastPage() {
	max, err := ctx.totalPages.Get()
	if err != nil {
		return
	}
	ctx.currentPage.Set(max)
	ctx.Refresh()
	ctx.ScrollToTop()
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
	ctx.table.OnDragStart = ctx.cellOnDragStart
	ctx.table.OnDragMove = ctx.cellOnDragMove
	ctx.table.OnDrop = ctx.cellOnDrop

	currentPage := widget.NewEntryWithData(binding.IntToString(ctx.currentPage))
	currentPage.Validator = nil
	ctx.pageEntry = currentPage
	currentPage.OnSubmitted = func(s string) {
		i, err := strconv.Atoi(s)
		if err != nil {
			return
		}
		ctx.currentPage.Set(i)
		ctx.Refresh()
		ctx.ScrollToTop()
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
	ctx.footer = container.NewBorder(ctx.newFilterWidget(), nil, leftFooter, nil)

	ctx.table.SelectionColor = color.RGBA{85, 85, 85, 128}
	ctx.Instance = container.NewBorder(nil, ctx.footer, nil, nil, container.NewPadded(ctx.table))
	ctx.Data = func(offset, limit int) *TableData { return NewTableData("empty") }
	ctx.RowCount = func() int { return 0 }
	ctx.Refresh()

	return ctx
}

// setFooterVisible shows or hides the filter/page footer. Show()/Hide() only
// toggle the flag, so the surrounding border must be refreshed to relayout.
func (ctx *TableWidget) setFooterVisible(show bool) {
	if ctx.footer == nil || ctx.footer.Hidden == !show {
		return
	}
	if show {
		ctx.footer.Show()
	} else {
		// A hidden Entry would otherwise keep keyboard focus, swallowing the
		// Ctrl+I shortcut that is supposed to bring the footer back.
		if canvas := fyne.CurrentApp().Driver().CanvasForObject(ctx.footer); canvas != nil {
			if focused := canvas.Focused(); focused == ctx.filterEntry || focused == ctx.pageEntry {
				canvas.Unfocus()
			}
		}
		ctx.footer.Hide()
	}
	ctx.Instance.Refresh()
}

// ShowFilter reveals the filter/page footer and focuses the filter box, even
// when the listing fits on one page. The footer stays visible until the
// listing is replaced (ClearFilter resets the explicit request).
func (ctx *TableWidget) ShowFilter() {
	ctx.filterShown = true
	ctx.setFooterVisible(true)
	if ctx.filterEntry == nil {
		return
	}
	if canvas := fyne.CurrentApp().Driver().CanvasForObject(ctx.filterEntry); canvas != nil {
		canvas.Focus(ctx.filterEntry)
	}
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

	// The footer (filter + page controls) would otherwise take a strip from
	// every listing even though most fit on one page. Keep it out of the way
	// until it is needed, or until the user asks for the filter with Ctrl+I.
	ctx.setFooterVisible(totalCount > ctx.pageSize || ctx.filterShown)

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
// so a stale filter does not silently hide rows in the new listing. An
// explicitly revealed filter bar is hidden again unless pagination needs it.
func (ctx *TableWidget) ClearFilter() {
	ctx.currentFilter = ""
	ctx.filteredData = ctx.currentData
	ctx.filterShown = false
	if ctx.filterEntry != nil {
		ctx.filterEntry.SetText("")
	}
	pages, _ := ctx.totalPages.Get()
	ctx.setFooterVisible(pages > 1)
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
		ctx.ScrollToTop()
	}

	return filter
}
