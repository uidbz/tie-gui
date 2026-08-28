package ui

import (
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/widget/tablewidget"
)

// albumColumn is one selectable track-table column: a stable key persisted in
// config and a human header label.
type albumColumn struct {
	key   string
	title string
}

// allAlbumColumns is the full column set in the default display order. The
// Track-no column always shows the real tag track number (even for a custom
// playlist ordering, where it therefore reads out of sequence).
var allAlbumColumns = []albumColumn{
	{"trackno", "Track no"},
	{"title", "Title"},
	{"artist", "Artist"},
	{"album", "Album"},
	{"year", "Year"},
	{"duration", "Duration"},
}

func lookupAlbumColumn(key string) (albumColumn, bool) {
	for _, c := range allAlbumColumns {
		if c.key == key {
			return c, true
		}
	}
	return albumColumn{}, false
}

func albumColumnTitle(key string) string {
	if c, ok := lookupAlbumColumn(key); ok {
		return c.title
	}
	return key
}

// resolveAlbumColumns maps persisted column keys to columns, dropping unknown or
// duplicate keys and falling back to the full default set when the result is
// empty (no config yet, or a config listing only stale keys).
func resolveAlbumColumns(keys []string) []albumColumn {
	var cols []albumColumn
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		if c, ok := lookupAlbumColumn(k); ok {
			cols = append(cols, c)
			seen[k] = true
		}
	}
	if len(cols) == 0 {
		cols = append(cols, allAlbumColumns...)
	}
	return cols
}

// trackTableOpts configures the optional behaviors that differ between the
// album view and the queue view.
type trackTableOpts struct {
	// indicator, when set, adds a fixed-width leading column (not part of the
	// customizable set) rendering a per-row glyph — used by the queue to mark
	// the currently-playing track.
	indicator func(row int) string
	// sortable enables header-click sorting (album view). The queue leaves this
	// off: its rows carry an intrinsic play order and are reordered by dragging.
	sortable bool
	// onReorder/onDragStart/onDragMove, when set, enable drag-to-reorder.
	onReorder   func(from, to int)
	onDragStart func(row int)
	onDragMove  func(pos fyne.Position)
	// onDoubleTap, when set, fires on a double-tap of a row (queue: play it).
	onDoubleTap func(displayRow int)
	// multiSelect enables row selection on a tap in any column (SelectColumn = -1)
	// with standard modifier semantics. Without it, a tap in any column activates
	// the row (album view's single-tap-to-play) and no selection is tracked.
	multiSelect bool
	// builtinColumnsButton adds a "Columns" button in a built-in toolbar above
	// the table (album view). The queue supplies its own toolbar button instead.
	builtinColumnsButton bool
}

// trackTable renders a slice of tracks as a column-customizable table shared by
// the album view and the queue. It owns the track slice; with sorting enabled
// it re-sorts that slice in place on a header click (via the FlexTable OnSort
// hook), so a row tap always maps to the track at that displayed position.
type trackTable struct {
	win    fyne.Window
	tracks []data.Track
	cols   []albumColumn
	opts   trackTableOpts

	sortKey string
	sortAsc bool

	// lastWidth is the most recent laid-out width of the table container, used to
	// size the stretch (title) column so it fits whatever pane the table lives in
	// (full window, or a split pane beside the queue) and adapts to resizes.
	lastWidth float32

	onPlay           func(displayRow int)
	onColumnsChanged func(keys []string)

	table  *tablewidget.TableWidget
	object fyne.CanvasObject
}

func newTrackTable(win fyne.Window, tracks []data.Track, colKeys []string, onPlay func(int), onColumnsChanged func([]string), opts trackTableOpts) *trackTable {
	at := &trackTable{
		win:              win,
		tracks:           tracks,
		cols:             resolveAlbumColumns(colKeys),
		opts:             opts,
		onPlay:           onPlay,
		onColumnsChanged: onColumnsChanged,
	}

	at.table = tablewidget.NewTableWidget("Tracks", 1000)
	at.table.Data = func(offset, limit int) *tablewidget.TableData {
		td := tablewidget.NewTableData("album")
		end := offset + limit
		if end > len(at.tracks) {
			end = len(at.tracks)
		}
		for i := offset; i < end; i++ {
			if at.opts.indicator != nil {
				td.AddStringCell("", at.opts.indicator(i))
			}
			for _, c := range at.cols {
				td.AddStringCell(c.title, at.cellValue(c.key, at.tracks[i]))
			}
		}
		return td
	}
	at.table.RowCount = func() int { return len(at.tracks) }

	ft := at.table.GetFlexTable()
	ft.SetCreateCell(func(col, row int) fyne.CanvasObject {
		lbl := widget.NewLabel("")
		lbl.Truncation = fyne.TextTruncateEllipsis
		return lbl
	})
	ft.SetUpdateCell(func(col, row int, obj fyne.CanvasObject) {
		if row < 0 || row >= len(at.tracks) {
			return
		}
		lbl := obj.(*widget.Label)
		if at.opts.indicator != nil && col == 0 {
			lbl.SetText(at.opts.indicator(row))
			return
		}
		ci := col - at.colOffset()
		if ci < 0 || ci >= len(at.cols) {
			return
		}
		lbl.SetText(at.cellValue(at.cols[ci].key, at.tracks[row]))
	})
	if at.opts.sortable {
		// Re-sort our own track slice rather than the built-in in-place TableData
		// sort, so widget-mode cells (rendered from at.tracks by row index) stay
		// in step with the header order.
		ft.OnSort = at.onSort
	}
	if at.onPlay != nil {
		at.table.OnRowActivated = func(row int) {
			if row >= 0 && row < len(at.tracks) {
				at.onPlay(row)
			}
		}
	}
	if at.opts.onDoubleTap != nil {
		at.table.OnRowDoubleTapped = func(row int) {
			if row >= 0 && row < len(at.tracks) {
				at.opts.onDoubleTap(row)
			}
		}
	}
	if at.opts.onReorder != nil {
		at.table.OnReorder = at.opts.onReorder
	}
	if at.opts.onDragStart != nil {
		at.table.OnDragStart = at.opts.onDragStart
	}
	if at.opts.onDragMove != nil {
		at.table.OnDragMove = at.opts.onDragMove
	}

	at.applyColumnConfig()

	var inner fyne.CanvasObject
	if at.opts.builtinColumnsButton {
		columns := widget.NewButtonWithIcon("Columns", theme.MenuIcon(), at.showColumnsDialog)
		inner = container.NewBorder(container.NewHBox(columns), nil, nil, nil, at.table.Instance)
	} else {
		inner = at.table.Instance
	}
	// Wrap in a width-tracking layout so the stretch column re-fits whenever the
	// table's actual pane width changes (window resize or split-divider drag).
	at.object = container.New(&trackTableLayout{tt: at}, inner)
	return at
}

// trackTableLayout fills its child and reports the laid-out width to the table
// so column widths track the pane, not the whole window.
type trackTableLayout struct{ tt *trackTable }

func (l *trackTableLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
	l.tt.applyWidth(size.Width)
}

func (l *trackTableLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m
}

// applyWidth records a new pane width and re-fits columns, ignoring no-op
// callbacks so it doesn't loop against the refresh SetColumnWidth triggers.
func (at *trackTable) applyWidth(w float32) {
	if w <= 0 {
		return
	}
	d := w - at.lastWidth
	if d < 0 {
		d = -d
	}
	if at.lastWidth != 0 && d < 1 {
		return
	}
	at.lastWidth = w
	at.setColumnWidths()
}

// colOffset is 1 when a leading indicator column is present, else 0.
func (at *trackTable) colOffset() int {
	if at.opts.indicator != nil {
		return 1
	}
	return 0
}

// cellValue renders one track's value for a column key.
func (at *trackTable) cellValue(key string, t data.Track) string {
	switch key {
	case "trackno":
		if t.TrackNo > 0 {
			return fmt.Sprintf("%d", t.TrackNo)
		}
		return "–"
	case "title":
		return t.Display()
	case "artist":
		return t.Artist
	case "album":
		return t.Album
	case "year":
		return t.Year
	case "duration":
		if t.Duration > 0 {
			return formatDuration(t.Duration)
		}
		return ""
	}
	return ""
}

// onSort handles a header click: it maps the header label back to a column key,
// records the direction (the FlexTable already toggles it), sorts, and refreshes.
func (at *trackTable) onSort(colTitle string, ascending bool) {
	key := ""
	for _, c := range at.cols {
		if c.title == colTitle {
			key = c.key
			break
		}
	}
	if key == "" {
		return
	}
	at.sortKey, at.sortAsc = key, ascending
	at.sortTracks()
	at.table.Refresh()
}

func (at *trackTable) sortTracks() {
	if at.sortKey == "" {
		return
	}
	sort.SliceStable(at.tracks, func(i, j int) bool {
		c := at.compare(at.sortKey, at.tracks[i], at.tracks[j])
		if at.sortAsc {
			return c < 0
		}
		return c > 0
	})
}

func (at *trackTable) compare(key string, a, b data.Track) int {
	switch key {
	case "trackno":
		return a.TrackNo - b.TrackNo
	case "duration":
		switch {
		case a.Duration < b.Duration:
			return -1
		case a.Duration > b.Duration:
			return 1
		default:
			return 0
		}
	case "year":
		return strings.Compare(a.Year, b.Year)
	case "title":
		return strings.Compare(strings.ToLower(a.Display()), strings.ToLower(b.Display()))
	case "artist":
		return strings.Compare(strings.ToLower(a.Artist), strings.ToLower(b.Artist))
	case "album":
		return strings.Compare(strings.ToLower(a.Album), strings.ToLower(b.Album))
	}
	return 0
}

// applyColumnConfig sizes the visible columns and configures tap routing. With
// multiSelect, SelectColumn = -1 so a tap in any column selects the row (the
// queue, which plays on double-tap). Otherwise SelectColumn is set out of range
// so every tap routes to OnActivate (the album view's single-tap-to-play).
func (at *trackTable) applyColumnConfig() {
	if at.opts.multiSelect {
		at.table.GetFlexTable().SelectColumn = -1
	} else {
		at.table.GetFlexTable().SelectColumn = at.colOffset() + len(at.cols)
	}
	at.setColumnWidths()
}

// selectedRows returns the currently selected display-row indices, sorted.
func (at *trackTable) selectedRows() []int { return at.table.SelectedRows() }

// clearSelection deselects all rows.
func (at *trackTable) clearSelection() { at.table.ClearSelection() }

// showInsertionLineAt draws the drop-indicator line for an external drag at the
// given absolute position and returns the row insertion gap it points to.
func (at *trackTable) showInsertionLineAt(pos fyne.Position) int {
	return at.table.GetFlexTable().ShowInsertionLineAt(pos)
}

// gapAt returns the row insertion gap under an external drag's absolute position
// without drawing anything.
func (at *trackTable) gapAt(pos fyne.Position) int {
	return at.table.GetFlexTable().InsertionGapAt(pos)
}

// hideInsertionLine clears the drop-indicator line after an external drag.
func (at *trackTable) hideInsertionLine() { at.table.GetFlexTable().HideInsertionLine() }

// indicatorWidth is the fixed width of the leading currently-playing column.
const indicatorWidth float32 = 32

func columnFixedWidth(key string) float32 {
	fixed := map[string]float32{"trackno": 70, "year": 70, "duration": 90, "artist": 180, "album": 180}
	if w := fixed[key]; w > 0 {
		return w
	}
	return 140
}

func (at *trackTable) setColumnWidths() {
	off := at.colOffset()
	// Prefer the table's own laid-out width (accurate inside a split pane); fall
	// back to the window canvas before the first layout pass.
	canvasW := at.lastWidth
	if canvasW == 0 && at.win != nil && at.win.Canvas() != nil {
		canvasW = at.win.Canvas().Size().Width
	}
	var used float32
	if at.opts.indicator != nil {
		at.table.SetColumnWidth(0, indicatorWidth)
		used += indicatorWidth
	}
	for _, c := range at.cols {
		if c.key != "title" {
			used += columnFixedWidth(c.key)
		}
	}
	for i, c := range at.cols {
		idx := off + i
		if c.key == "title" {
			w := canvasW - used - 60
			if w < 200 {
				w = 200
			}
			at.table.SetColumnWidth(idx, w)
			continue
		}
		at.table.SetColumnWidth(idx, columnFixedWidth(c.key))
	}
}

// show sizes the Title column to the now-known canvas width. Called after the
// view is placed as window content.
func (at *trackTable) show() {
	at.setColumnWidths()
	at.table.Refresh()
}

// setTracks replaces the rendered track slice and refreshes. Used by the queue,
// which rebuilds its rows from the live playlist on each status poll.
func (at *trackTable) setTracks(tracks []data.Track) {
	at.tracks = tracks
	at.table.Refresh()
}

// setColumns applies a new visible-column set and refreshes.
func (at *trackTable) setColumns(keys []string) {
	at.cols = resolveAlbumColumns(keys)
	at.applyColumnConfig()
	at.table.Refresh()
}

// showColumnsDialog lets the user toggle and reorder columns. The working list
// holds every column (visible ones first, in their current order, then hidden
// ones); a checkbox selects visibility and the up/down buttons reorder. The
// applied visible set is the checked columns in list order.
func (at *trackTable) showColumnsDialog() {
	order := make([]string, 0, len(allAlbumColumns))
	checks := map[string]bool{}
	for _, c := range at.cols {
		order = append(order, c.key)
		checks[c.key] = true
	}
	for _, c := range allAlbumColumns {
		if !checks[c.key] {
			order = append(order, c.key)
		}
	}

	list := container.NewVBox()
	var rebuild func()
	rebuild = func() {
		list.RemoveAll()
		for i, key := range order {
			key, idx := key, i
			chk := widget.NewCheck(albumColumnTitle(key), func(b bool) { checks[key] = b })
			chk.SetChecked(checks[key])
			up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				if idx > 0 {
					order[idx-1], order[idx] = order[idx], order[idx-1]
					rebuild()
				}
			})
			down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
				if idx < len(order)-1 {
					order[idx+1], order[idx] = order[idx], order[idx+1]
					rebuild()
				}
			})
			if idx == 0 {
				up.Disable()
			}
			if idx == len(order)-1 {
				down.Disable()
			}
			list.Add(container.NewBorder(nil, nil, nil, container.NewHBox(up, down), chk))
		}
		list.Refresh()
	}
	rebuild()

	d := dialog.NewCustomConfirm("Columns", "Apply", "Cancel", container.NewVScroll(list), func(ok bool) {
		if !ok {
			return
		}
		var keys []string
		for _, k := range order {
			if checks[k] {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			dialog.ShowInformation("Columns", "Select at least one column.", at.win)
			return
		}
		at.setColumns(keys)
		if at.onColumnsChanged != nil {
			at.onColumnsChanged(keys)
		}
	}, at.win)
	d.Resize(fyne.NewSize(360, 420))
	d.Show()
}
