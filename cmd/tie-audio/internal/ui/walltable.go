package ui

import (
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/widget/tablewidget"
)

// The browse wall's table view: the same albums the cover wall shows, listed
// as a sortable, filterable table instead of a cover grid. The user chooses
// between the two renderings (gallery ☰ menu → "Table view", the table's own
// "Cover view" button); the choice persists in AppConfig.BrowseView.

// allWallColumns is the album table's full column set in the default display
// order. The Art column renders the album cover; its cell text is the album
// UID (read back by the cover cell via FlexTable.CellText, so filtering,
// pagination and sorting can never desync the cell from its album).
var allWallColumns = []albumColumn{
	{"cover", "Art"},
	{"title", "Title"},
	{"artist", "Artist"},
	{"year", "Year"},
	{"kind", "Kind"},
}

// defaultWallColumns is the table view's default set (regular layout).
var defaultWallColumns = []albumColumn{
	{"cover", "Art"},
	{"title", "Title"},
	{"artist", "Artist"},
	{"year", "Year"},
	{"kind", "Kind"},
}

// compactWallColumns is the table view's column set in the compact layout: a
// phone has room for little more than the cover and the two lines a wall tile
// would show (title, artist), and the Columns dialog is not offered.
var compactWallColumns = []albumColumn{
	{"cover", "Art"},
	{"title", "Title"},
	{"artist", "Artist"},
}

// wallTable renders the browse wall's albums as a column-customizable table,
// mirroring trackTable. It owns its album slice; header-click sorting re-sorts
// that slice in place (via the FlexTable OnSort hook), so a row tap always
// maps to the album at that displayed position. Sorting is re-applied when a
// fresh listing arrives, so a wall reload keeps the chosen order.
type wallTable struct {
	page   *browsePage
	win    fyne.Window
	albums []data.Album
	cols   []albumColumn

	sortKey string
	sortAsc bool

	// lastWidth is the most recent laid-out width of the table container, used
	// to size the stretch (title) column (see trackTable.lastWidth).
	lastWidth float32

	// open and showMenu default to the page's album opener and context menu;
	// tests substitute them (the real opener needs a tie session).
	open     func(data.Album)
	showMenu func(obj fyne.CanvasObject, a data.Album)

	table  *tablewidget.TableWidget
	object fyne.CanvasObject
}

func newWallTable(page *browsePage) *wallTable {
	wt := &wallTable{page: page, win: page.win, open: page.openAlbum, showMenu: page.showAlbumMenu}
	if page.compact {
		wt.cols = resolveAlbumColumns(nil, compactWallColumns, allWallColumns)
	} else {
		wt.cols = resolveAlbumColumns(page.session.Cfg.WallColumns, defaultWallColumns, allWallColumns)
	}

	wt.table = tablewidget.NewTableWidget("Albums", 1000)
	wt.table.Data = func(offset, limit int) *tablewidget.TableData {
		td := tablewidget.NewTableData("albums")
		end := offset + limit
		if end > len(wt.albums) {
			end = len(wt.albums)
		}
		for i := offset; i < end; i++ {
			for _, c := range wt.cols {
				td.AddStringCell(c.title, wt.cellValue(c.key, wt.albums[i]))
			}
		}
		return td
	}
	wt.table.RowCount = func() int { return len(wt.albums) }

	ft := wt.table.GetFlexTable()
	// Cells render from the displayed TableData (CellText), never from
	// wt.albums by row index: in-page filtering and pagination reorder the
	// display against the slice, and the displayed row is always right.
	ft.SetCreateCell(func(col, row int) fyne.CanvasObject {
		if wt.columnKeyAt(col) == "cover" {
			return newCoverCell(page.covers)
		}
		lbl := widget.NewLabel("")
		lbl.Truncation = fyne.TextTruncateEllipsis
		return lbl
	})
	ft.SetUpdateCell(func(col, row int, obj fyne.CanvasObject) {
		txt := ft.CellText(col, row)
		if cell, ok := obj.(*coverCell); ok {
			cell.show(txt) // the Art column's cell text is the album UID
			return
		}
		if lbl, ok := obj.(*widget.Label); ok {
			lbl.SetText(txt)
		}
	})
	// Re-sort our own album slice rather than the built-in in-place TableData
	// sort, so row activation (albums[Offset+row]) stays in step with the
	// header order.
	ft.OnSort = wt.onSort
	wt.table.OnRowActivated = func(row int) {
		if a, ok := wt.albumAt(row); ok {
			wt.open(a)
		}
	}
	wt.table.OnRowMenu = func(row int, obj fyne.CanvasObject) {
		if a, ok := wt.albumAt(row); ok {
			wt.showMenu(obj, a)
		}
	}

	wt.applyColumnConfig()

	// The button row mirrors the album view's: the view toggle on the left,
	// the Columns dialog beside it (regular layout only — the compact column
	// set is fixed, like the album view's).
	buttons := container.NewHBox(widget.NewButtonWithIcon("Cover view", theme.GridIcon(), page.toggleWallView))
	if !page.compact {
		buttons.Add(widget.NewButtonWithIcon("Columns", theme.MenuIcon(), wt.showColumnsDialog))
	}
	inner := container.NewBorder(buttons, nil, nil, nil, wt.table.Instance)
	// Wrap in a width-tracking layout so the stretch column re-fits whenever
	// the table's actual pane width changes (window resize or split drag).
	wt.object = container.New(&wallTableLayout{wt: wt}, inner)
	return wt
}

// wallTableLayout fills its child and reports the laid-out width to the table
// so column widths track the pane, not the whole window.
type wallTableLayout struct{ wt *wallTable }

func (l *wallTableLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
	l.wt.applyWidth(size.Width)
}

func (l *wallTableLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m
}

// applyWidth records a new pane width and re-fits columns, ignoring no-op
// callbacks so it doesn't loop against the refresh SetColumnWidth triggers.
func (wt *wallTable) applyWidth(w float32) {
	if w <= 0 {
		return
	}
	d := w - wt.lastWidth
	if d < 0 {
		d = -d
	}
	if wt.lastWidth != 0 && d < 1 {
		return
	}
	wt.lastWidth = w
	wt.setColumnWidths()
}

// columnKeyAt maps a table column index to its column key.
func (wt *wallTable) columnKeyAt(col int) string {
	if col < 0 || col >= len(wt.cols) {
		return ""
	}
	return wt.cols[col].key
}

// cellValue renders one album's value for a column key. The Art column's value
// is the album UID — the cover cell reads it back via FlexTable.CellText.
func (wt *wallTable) cellValue(key string, a data.Album) string {
	switch key {
	case "cover":
		return a.UID
	case "title":
		return a.Display()
	case "artist":
		return a.Artist
	case "year":
		return a.Year
	case "kind":
		return albumKindLabel(a.Kind)
	}
	return ""
}

// albumKindLabel is the table's display label for an album's tie shape.
func albumKindLabel(kind data.AlbumKind) string {
	switch kind {
	case data.AlbumDir:
		return "Album"
	case data.AlbumArchive:
		return "Archive"
	case data.AlbumTrack:
		return "Track"
	}
	return ""
}

// albumAt maps a page-relative display row (already translated back through
// in-page filtering by the TableWidget) to its album.
func (wt *wallTable) albumAt(pageRow int) (data.Album, bool) {
	idx := wt.table.Offset + pageRow
	if idx < 0 || idx >= len(wt.albums) {
		return data.Album{}, false
	}
	return wt.albums[idx], true
}

// onSort handles a header click: it maps the header label back to a column
// key, records the direction (the FlexTable already toggles it), sorts, and
// refreshes.
func (wt *wallTable) onSort(colTitle string, ascending bool) {
	key := ""
	for _, c := range wt.cols {
		if c.title == colTitle {
			key = c.key
			break
		}
	}
	if key == "" {
		return
	}
	wt.sortKey, wt.sortAsc = key, ascending
	wt.sortAlbums()
	wt.table.Refresh()
}

func (wt *wallTable) sortAlbums() {
	if wt.sortKey == "" {
		return
	}
	sort.SliceStable(wt.albums, func(i, j int) bool {
		c := compareAlbums(wt.sortKey, wt.albums[i], wt.albums[j])
		if wt.sortAsc {
			return c < 0
		}
		return c > 0
	})
}

func compareAlbums(key string, a, b data.Album) int {
	switch key {
	case "title":
		return strings.Compare(strings.ToLower(a.Display()), strings.ToLower(b.Display()))
	case "artist":
		return strings.Compare(strings.ToLower(a.Artist), strings.ToLower(b.Artist))
	case "year":
		return strings.Compare(a.Year, b.Year)
	case "kind":
		return strings.Compare(albumKindLabel(a.Kind), albumKindLabel(b.Kind))
	}
	return 0
}

// setAlbums replaces the rendered album slice and refreshes. A chosen sort is
// re-applied to the new listing so a wall reload keeps the display order. The
// page and in-page filter reset like the cover wall's own reload, which always
// lands on the first page.
func (wt *wallTable) setAlbums(albums []data.Album) {
	wt.albums = albums
	if wt.sortKey != "" {
		wt.sortAlbums()
	}
	wt.table.ResetPage()
	wt.table.ClearFilter()
	wt.table.Refresh()
}

// applyColumnConfig sizes the visible columns and configures tap routing:
// SelectColumn is set out of range so every tap routes to OnActivate (single
// tap opens the album) and no selection is tracked, like the album view.
func (wt *wallTable) applyColumnConfig() {
	wt.table.GetFlexTable().SelectColumn = len(wt.cols)
	wt.setColumnWidths()
}

func (wt *wallTable) setColumnWidths() {
	// Prefer the table's own laid-out width (accurate inside a split pane);
	// fall back to the window canvas before the first layout pass.
	canvasW := wt.lastWidth
	if canvasW == 0 && wt.win != nil && wt.win.Canvas() != nil {
		canvasW = wt.win.Canvas().Size().Width
	}
	var used float32
	for _, c := range wt.cols {
		if c.key != "title" {
			used += columnFixedWidth(c.key)
		}
	}
	for i, c := range wt.cols {
		if c.key == "title" {
			w := canvasW - used - 60
			if w < 200 {
				w = 200
			}
			wt.table.SetColumnWidth(i, w)
			continue
		}
		wt.table.SetColumnWidth(i, columnFixedWidth(c.key))
	}
}

// show sizes the Title column to the now-known canvas width. Called after the
// view is placed as window content.
func (wt *wallTable) show() {
	wt.setColumnWidths()
	wt.table.Refresh()
}

// setCompact switches between the compact (fixed) and regular (persisted)
// column sets on a layout-mode change, like the album view's per-layout sets.
func (wt *wallTable) setCompact(compact bool) {
	if compact {
		wt.cols = resolveAlbumColumns(nil, compactWallColumns, allWallColumns)
	} else {
		wt.cols = resolveAlbumColumns(wt.page.session.Cfg.WallColumns, defaultWallColumns, allWallColumns)
	}
	wt.applyColumnConfig()
	wt.table.Refresh()
}

// setColumns applies a new visible-column set and refreshes.
func (wt *wallTable) setColumns(keys []string) {
	wt.cols = resolveAlbumColumns(keys, defaultWallColumns, allWallColumns)
	wt.applyColumnConfig()
	wt.table.Refresh()
}

// showColumnsDialog lets the user toggle and reorder columns (regular layout).
func (wt *wallTable) showColumnsDialog() {
	showTableColumnsDialog(wt.win, wt.cols, allWallColumns, func(keys []string) {
		wt.setColumns(keys)
		wt.page.saveWallColumns(keys)
	})
}

// --- View-mode plumbing (browsePage) ---

// tableMode reports whether the browse wall currently renders as a table
// rather than the cover grid.
func (b *browsePage) tableMode() bool {
	return b.session.Cfg.BrowseView == config.BrowseTable
}

// setWallAlbums records the wall's current listing and feeds it to the table
// view. Runs on the UI goroutine (feed goroutines hand off via fyne.Do).
func (b *browsePage) setWallAlbums(albums []data.Album) {
	b.albums = albums
	if b.wallTable != nil {
		b.wallTable.setAlbums(albums)
	}
}

// showWall pushes the wall to the window in the configured rendering: the
// cover grid, or the album table.
func (b *browsePage) showWall() {
	if b.tableMode() {
		b.showWallTable()
		return
	}
	b.viewer.ChangeGallery()
}

// showWallTable swaps the window content to the album table, building it on
// first use.
func (b *browsePage) showWallTable() {
	if b.wallTable == nil {
		b.wallTable = newWallTable(b)
		b.wallTable.setAlbums(b.albums)
	}
	b.win.SetContent(b.wallTableRoot())
	b.wallTable.show() // size the Title column now that the pane width is known
}

// wallTableRoot composes the table view the way the gallery composes the cover
// wall: in the regular layout the sidebar is a split pane beside the table; in
// the compact layout the tag selection is a chips row above the table and the
// gallery's own sidebar drawer is stacked over it (OpenSidebar/CloseSidebar
// keep working — the drawer object has one on-screen parent at a time).
func (b *browsePage) wallTableRoot() fyne.CanvasObject {
	body := b.wallTable.object
	if !b.compact {
		if sidebar := b.viewer.Sidebar; sidebar != nil {
			split := container.NewHSplit(sidebar, body)
			split.SetOffset(0.2)
			return split
		}
		return body
	}
	var main fyne.CanvasObject = body
	include, exclude := b.ts.SelectedTags()
	chips := gallery.FilterChipRow(gallery.TagFilterChips(include, exclude, func(tag string) {
		b.ts.RemoveSelected(tag)
	}), b.openSidebar)
	if chips != nil {
		main = container.NewBorder(chips, nil, nil, nil, body)
	}
	if drawer := b.viewer.DrawerObject(); drawer != nil {
		return container.NewStack(main, drawer)
	}
	return main
}

// toggleWallView switches the wall between the cover grid and the album table,
// persisting the choice. Bound to the gallery ☰ menu's "Table view" item and
// the table view's "Cover view" button.
func (b *browsePage) toggleWallView() {
	if b.tableMode() {
		b.session.Cfg.BrowseView = config.BrowseCovers
	} else {
		b.session.Cfg.BrowseView = config.BrowseTable
	}
	if err := config.Save(b.session.Cfg); err != nil {
		dialog.ShowError(err, b.win)
	}
	b.showWall()
}

// saveWallColumns persists the album table's visible-column set/order to the
// app config so it survives across sessions.
func (b *browsePage) saveWallColumns(keys []string) {
	b.session.Cfg.WallColumns = keys
	if err := config.Save(b.session.Cfg); err != nil {
		dialog.ShowError(err, b.win)
	}
}
