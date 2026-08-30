package tablewidget

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"image/color"
	"sort"

	"fyne.io/fyne/v2/theme"
)

// currentKeyModifiers returns the keyboard modifiers held right now, or 0 on a
// driver without a keyboard (mobile). Used to give taps standard shift/ctrl
// multi-select semantics.
func currentKeyModifiers() fyne.KeyModifier {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		return d.CurrentKeyModifiers()
	}
	return 0
}

// stripeAltColor returns the alternate-row background for the current theme.
// It blends the theme background toward the foreground by a small factor so the
// stripe is always visible in both light and dark variants (unlike a fixed theme
// color name such as OverlayBackground, which equals Background in light mode).
func stripeAltColor() color.Color {
	bg := theme.Color(theme.ColorNameBackground)
	fg := theme.Color(theme.ColorNameForeground)
	return blendColor(bg, fg, 0.07)
}

func blendColor(base, over color.Color, t float64) color.NRGBA {
	br, bg, bb, ba := base.RGBA()
	or, og, ob, _ := over.RGBA()
	lerp := func(a, b uint32) uint8 {
		return uint8(float64(a>>8)*(1-t) + float64(b>>8)*t)
	}
	return color.NRGBA{R: lerp(br, or), G: lerp(bg, og), B: lerp(bb, ob), A: uint8(ba >> 8)}
}

type FlexTable struct {
	widget.BaseWidget
	data  *TableData
	table *widget.Table
	// Row-interaction hooks. Row is the display-row index into the currently
	// set data. They fire from both string-mode (TableCell) and widget-mode
	// (WidgetCell) cells. OnClickSecondary receives the cell object so callers
	// can position a popup menu.
	//
	// A single tap in SelectColumn toggles row selection and fires OnClick; a
	// single tap in any other column fires OnActivate. When SelectColumn is < 0
	// (the default) every column selects and OnActivate never fires. Cells are
	// intentionally NOT DoubleTappable so a single tap fires immediately instead
	// of being delayed to disambiguate a double tap.
	OnClick          func(row int)
	OnClickSecondary func(row int, obj fyne.CanvasObject)
	OnActivate       func(row int)
	SelectColumn     int
	// OnSort, when set, is called on a header click instead of the built-in
	// in-place TableData sort. It lets a consumer that owns the backing data
	// (e.g. an external []Entry) re-sort its own source and rebuild the table.
	OnSort func(colId string, ascending bool)
	// Drag-and-drop. A drag gesture starting on a row fires OnDragStart(row); on
	// release OnDrop(row, absPos) fires with the pointer's last absolute position
	// so a consumer can hit-test it against another widget's bounds (Fyne has no
	// cross-widget drop target). Drag events keep flowing to the source cell for
	// the whole gesture, so dragPos tracks the pointer across the window.
	OnDragStart  func(row int)
	OnDragMove   func(absPos fyne.Position)
	OnDrop       func(row int, absPos fyne.Position)
	dragging     bool
	dragStartRow int
	dragPos      fyne.Position
	selectedRows map[int]bool
	// selAnchor is the pivot row for shift-range selection (the last row selected
	// without shift). -1 when there is no anchor.
	selAnchor      int
	minWidth       float32
	scroll         *container.Scroll
	w              fyne.Window
	SelectionColor color.Color
	CellBgColor    color.Color
	CellBgColorAlt color.Color
	headerBgColor  color.Color

	// Widget mode support
	widgetMode     bool
	createCellFunc func(col, row int) fyne.CanvasObject
	updateCellFunc func(col, row int, obj fyne.CanvasObject)

	// dataGen bumps on every SetData (a real data change). A WidgetCell records
	// the generation it last ran UpdateCell at; a geometry-only refresh (e.g. a
	// column-resize drag, which forces a full re-render of every visible cell)
	// then skips the per-cell UpdateCell VM round-trip because the content is
	// unchanged. This is the difference between fluid and choppy column resizing.
	dataGen int

	// sortCol/sortAsc hold the current header sort state. They live on the
	// FlexTable (not the TableData) so they survive SetData, which swaps in a
	// fresh TableData on every page load and would otherwise reset the sort
	// direction and erase the header arrow.
	sortCol string
	sortAsc bool
}

func NewFlexTable(data *TableData, onClick func(row int)) *FlexTable {
	table := &FlexTable{
		OnClick:        onClick,
		SelectColumn:   -1,
		selectedRows:   map[int]bool{},
		selAnchor:      -1,
		minWidth:       float32(data.ColumnCount() * 200),
		SelectionColor: theme.Color(theme.ColorNameSelection),
		CellBgColor:    theme.Color(theme.ColorNameBackground),
		CellBgColorAlt: theme.Color(theme.ColorNameInputBackground),
		// headerBgColor:  color.RGBA{89, 89, 89, 255},
		// headerBgColor: color.RGBA{85, 170, 127, 255},
	}

	table.table = widget.NewTable(
		func() (int, int) {
			return table.data.RowCount(), table.data.ColumnCount()
		},
		func() fyne.CanvasObject {
			if table.widgetMode && table.createCellFunc != nil {
				// Widget mode: create a wrapper that can be replaced
				return NewWidgetCell(table)
			}
			// String mode: use Label cell (current behavior)
			return NewCell(table)
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			if table.widgetMode && table.updateCellFunc != nil {
				// Widget mode: Replace the widget in the cell
				if widgetCell, ok := o.(*WidgetCell); ok {
					// Check if we need to recreate the widget
					needsRecreation := widgetCell.currentID.Col != id.Col ||
						widgetCell.currentID.Row != id.Row

					if needsRecreation {
						cellWidget := table.createCellFunc(id.Col, id.Row)
						widgetCell.SetWidget(cellWidget, id)
					}
					// Skip the UpdateCell VM round-trip when this cell already
					// reflects the current data at its current position. A
					// geometry-only refresh (column-resize drag) re-renders every
					// visible cell but changes no content, so re-running the
					// script callback per cell per drag-pixel is pure waste and is
					// what makes resizing choppy. Recreation or a new data
					// generation forces a real update.
					if widgetCell.content != nil && (needsRecreation || widgetCell.lastGen != table.dataGen) {
						// Render from the currently displayed data by display row.
						// Sorting/filtering reorder t.data itself, so the display
						// row is always the correct index into it.
						table.updateCellFunc(id.Col, id.Row, widgetCell.content)
						widgetCell.lastGen = table.dataGen
					}
					// Apply row striping to widget-mode cells
					if table.selectedRows[id.Row] {
						widgetCell.background.FillColor = table.SelectionColor
					} else {
						if id.Row%2 == 0 {
							widgetCell.background.FillColor = theme.Color(theme.ColorNameBackground)
						} else {
							widgetCell.background.FillColor = stripeAltColor()
						}
					}
					widgetCell.background.Refresh()
				}
			} else {
				// String mode: update Label (current behavior)
				// Handle both TableCell and WidgetCell (for when filtering in widget mode)
				if widgetCell, ok := o.(*WidgetCell); ok {
					// WidgetCell in string mode - replace content with a label showing string data
					txt := table.data.Get(id.Col, id.Row)
					if widgetCell.content == nil || widgetCell.currentID.Col != id.Col || widgetCell.currentID.Row != id.Row {
						label := widget.NewLabel(txt)
						widgetCell.SetWidget(label, id)
					} else if lbl, ok := widgetCell.content.(*widget.Label); ok {
						lbl.SetText(txt)
					}
				} else if cell, ok := o.(*TableCell); ok {
					txt := table.data.Get(id.Col, id.Row)
					if cell.label.Text != txt {
						cell.label.SetText(txt)
					}
					cell.Id = id
					if table.selectedRows[id.Row] {
						cell.background.FillColor = table.SelectionColor
					} else {
						if id.Row%2 == 0 {
							cell.background.FillColor = theme.Color(theme.ColorNameBackground)
						} else {
							cell.background.FillColor = stripeAltColor()
						}
					}
				}
			}
		})
	table.table.ShowHeaderRow = true

	table.SetData(data)

	table.ExtendBaseWidget(table)

	return table
}

func (t *FlexTable) SetData(data *TableData) {
	t.data = data
	t.dataGen++
	t.table.CreateHeader = func() fyne.CanvasObject {
		return NewHeader("", t.headerBgColor, t)
	}
	t.table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		h := o.(*Header)
		h.SetText(data.Columns[id.Col])
		h.colId = data.Columns[id.Col]
		h.RefreshSortIcon()
	}
	// for i, _ := range data.Columns {
	// 	t.table.SetColumnWidth(i, 300)
	// }
	t.table.Refresh()
}

func (t *FlexTable) SetColumnWidth(id int, width float32) {
	t.table.SetColumnWidth(id, width)
}

// SetSort records the current sort column and direction for the header arrow
// without triggering a sort. Consumers that own their own sort state (via
// OnSort) call it to seed the arrow before any header click.
func (t *FlexTable) SetSort(colId string, ascending bool) {
	t.sortCol = colId
	t.sortAsc = ascending
}

// CellText returns the display text at the given column and display row of the
// currently set data. Widget-mode update callbacks use it to render from the
// live (possibly sorted/filtered) data.
func (t *FlexTable) CellText(col, row int) string {
	return t.data.Get(col, row)
}

func (t *FlexTable) Refresh() {
	// Treat an explicit Refresh as a possible content change (e.g. header-click
	// sort mutates data in place and calls this without SetData). Bumping dataGen
	// forces visible cells to re-run UpdateCell. The interactive column-resize
	// drag does NOT come through here — it drives the inner widget.Table directly
	// — so resize still skips the per-cell VM round-trip.
	t.dataGen++
	t.table.Refresh()
}

func (t *FlexTable) SetCreateCell(fn func(col, row int) fyne.CanvasObject) {
	t.createCellFunc = fn
	t.widgetMode = true
}

func (t *FlexTable) SetUpdateCell(fn func(col, row int, obj fyne.CanvasObject)) {
	t.updateCellFunc = fn
}

type tableRenderer struct {
	table   *widget.Table
	objects []fyne.CanvasObject
}

func (tr *tableRenderer) Destroy() {
}

func (tr *tableRenderer) Layout(size fyne.Size) {
	tr.table.Resize(size)
}

func (tr *tableRenderer) MinSize() fyne.Size {
	s := fyne.NewSize(200, 200)
	return s
}

func (tr *tableRenderer) Objects() []fyne.CanvasObject {
	return tr.objects
}

func (tr *tableRenderer) Refresh() {
	canvas.Refresh(tr.table)
}

func (t *FlexTable) CreateRenderer() fyne.WidgetRenderer {
	tr := &tableRenderer{
		table: t.table,
	}
	tr.objects = []fyne.CanvasObject{t.table}
	return tr
}

type TableCell struct {
	widget.BaseWidget
	background *canvas.Rectangle
	label      *widget.Label
	table      *FlexTable
	Id         widget.TableCellID
	isEmpty    bool
	rowCells   []*TableCell
}

func NewCell(table *FlexTable) *TableCell {
	cell := &TableCell{
		background: canvas.NewRectangle(theme.Color(theme.ColorNameBackground)),
		label:      widget.NewLabel(""),
		table:      table,
		isEmpty:    true,
	}
	cell.label.Truncation = fyne.TextTruncateEllipsis

	cell.ExtendBaseWidget(cell)
	return cell
}

func (c *TableCell) CreateRenderer() fyne.WidgetRenderer {
	// Use container.Stack for layering and container.Clip to prevent overflow
	content := container.NewStack(c.background, c.label)
	clipped := container.NewClip(content)
	return widget.NewSimpleRenderer(clipped)
}

// cellTapped routes a single tap: a tap in SelectColumn (or any column when
// SelectColumn < 0) updates the selection (with standard plain/ctrl/shift
// semantics) and fires OnClick; a tap in any other column fires OnActivate.
func (t *FlexTable) cellTapped(row, col int) {
	if t.SelectColumn < 0 || col == t.SelectColumn {
		t.applySelection(row)
		if t.OnClick != nil {
			t.OnClick(row)
		}
		t.Refresh()
		return
	}
	if t.OnActivate != nil {
		t.OnActivate(row)
	}
}

// applySelection mutates selectedRows for a tap on row, honoring the modifiers
// held at tap time: plain replaces the selection with just this row; ctrl/super
// toggles this row; shift selects the contiguous range from the anchor (adding
// to the selection when ctrl is also held, else replacing it).
func (t *FlexTable) applySelection(row int) {
	mods := currentKeyModifiers()
	shift := mods&fyne.KeyModifierShift != 0
	toggle := mods&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0

	switch {
	case shift && t.selAnchor >= 0:
		if !toggle {
			t.selectedRows = map[int]bool{}
		}
		lo, hi := t.selAnchor, row
		if lo > hi {
			lo, hi = hi, lo
		}
		for r := lo; r <= hi; r++ {
			t.selectedRows[r] = true
		}
		// anchor stays put so the range can be re-dragged from the same pivot
	case toggle:
		if t.selectedRows[row] {
			delete(t.selectedRows, row)
		} else {
			t.selectedRows[row] = true
		}
		t.selAnchor = row
	default:
		t.selectedRows = map[int]bool{row: true}
		t.selAnchor = row
	}
}

// rowSecondary fires OnClickSecondary with the cell object. It leaves the
// multi-select set untouched: the context menu targets its own row argument.
func (t *FlexTable) rowSecondary(row int, obj fyne.CanvasObject) {
	if t.OnClickSecondary != nil {
		t.OnClickSecondary(row, obj)
	}
}

// dragStart marks the beginning of a drag gesture on the given display row. It
// is idempotent within a gesture: OnDragStart fires only on the first event so
// dragStartRow captures the row the drag was grabbed from.
func (t *FlexTable) dragStart(row int) {
	if t.dragging {
		return
	}
	t.dragging = true
	t.dragStartRow = row
	if t.OnDragStart != nil {
		t.OnDragStart(row)
	}
}

// dragMove records the pointer position and notifies OnDragMove so a consumer
// can move a drag indicator that follows the cursor.
func (t *FlexTable) dragMove(pos fyne.Position) {
	t.dragPos = pos
	if t.OnDragMove != nil {
		t.OnDragMove(pos)
	}
}

// dragEnd ends the current gesture and fires OnDrop with the last pointer
// position recorded during the drag.
func (t *FlexTable) dragEnd() {
	if !t.dragging {
		return
	}
	t.dragging = false
	if t.OnDrop != nil {
		t.OnDrop(t.dragStartRow, t.dragPos)
	}
}

// SelectedRows returns the currently selected display-row indices, sorted.
func (t *FlexTable) SelectedRows() []int {
	rows := make([]int, 0, len(t.selectedRows))
	for r := range t.selectedRows {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	return rows
}

// ClearSelection deselects all rows and refreshes.
func (t *FlexTable) ClearSelection() {
	if len(t.selectedRows) == 0 {
		return
	}
	t.selectedRows = map[int]bool{}
	t.selAnchor = -1
	t.Refresh()
}

func (c *TableCell) Tapped(_ *fyne.PointEvent) {
	c.table.cellTapped(c.Id.Row, c.Id.Col)
}

func (c *TableCell) TappedSecondary(_ *fyne.PointEvent) {
	c.table.rowSecondary(c.Id.Row, c)
}

func (c *TableCell) ColumnName() string {
	return c.table.data.Columns[c.Id.Col]
}

func (c *TableCell) Text() string {
	return c.label.Text
}

func (c *TableCell) IsEmpty() bool {
	if c.Text() == "" {
		return true
	}
	return false
}

// func (c *TableCell) SetText(text string) {
// 	c.label.Text = text
// 	c.label.SetText()
// 	c.label.Refresh()
// }

type Header struct {
	TableCell
	bgColor color.Color
	icon    *widget.Icon
	colId   string
}

func NewHeader(columnName string, bgColor color.Color, table *FlexTable) *Header {
	header := &Header{}
	header.background = canvas.NewRectangle(bgColor)
	// header.label = canvas.NewText(columnName, color.RGBA{211, 211, 233, 255})
	header.label = widget.NewLabel(columnName)
	header.label.TextStyle.Bold = true
	header.label.Importance = widget.HighImportance
	// header.label.TextSize = theme.TextSize() * 1.2
	header.table = table
	header.bgColor = theme.Color(theme.ColorNameBackground)

	header.ExtendBaseWidget(header)
	return header
}

func (h *Header) SetText(columnName string) {
	h.label.Text = columnName
	h.label.Refresh()
}

func (h *Header) CreateRenderer() fyne.WidgetRenderer {
	h.icon = widget.NewIcon(nil)
	item := container.NewStack(h.background, container.NewBorder(nil, nil, nil, h.icon, h.label))
	return widget.NewSimpleRenderer(item)
}

func (h *Header) RefreshSortIcon() {
	defer h.Refresh()

	if h.table.sortCol != h.colId {
		h.icon.Hide()
		return
	}
	if h.table.sortAsc {
		h.icon.SetResource(theme.MenuDropDownIcon())
	} else {
		h.icon.SetResource(theme.MenuDropUpIcon())
	}
	h.icon.Show()
}

func (h *Header) Tapped(_ *fyne.PointEvent) {
	h.table.sortAsc = !h.table.sortAsc
	h.table.sortCol = h.colId
	if h.table.OnSort != nil {
		h.table.OnSort(h.colId, h.table.sortAsc)
		return
	}
	h.table.data.Sort(h.colId, h.table.sortAsc)
	h.table.Refresh()
}

// WidgetCell is a cell that can hold any widget (for widget mode)
type WidgetCell struct {
	widget.BaseWidget
	background *canvas.Rectangle
	content    fyne.CanvasObject
	currentID  widget.TableCellID
	table      *FlexTable
	lastGen    int // FlexTable.dataGen this cell last ran UpdateCell at (0 = never)
}

func NewWidgetCell(table *FlexTable) *WidgetCell {
	cell := &WidgetCell{
		background: canvas.NewRectangle(theme.Color(theme.ColorNameBackground)),
		currentID:  widget.TableCellID{Col: -1, Row: -1},
		table:      table,
	}
	cell.ExtendBaseWidget(cell)
	return cell
}

func (w *WidgetCell) Tapped(_ *fyne.PointEvent) {
	w.table.cellTapped(w.currentID.Row, w.currentID.Col)
}

func (w *WidgetCell) TappedSecondary(_ *fyne.PointEvent) {
	w.table.rowSecondary(w.currentID.Row, w)
}

// Dragged / DragEnd implement fyne.Draggable so a row can be dragged onto the
// sibling panel. The row is captured on the first event; the pointer's absolute
// position is recorded on every event for the drop hit-test.
func (w *WidgetCell) Dragged(e *fyne.DragEvent) {
	w.table.dragStart(w.currentID.Row)
	w.table.dragMove(e.AbsolutePosition)
}

func (w *WidgetCell) DragEnd() {
	w.table.dragEnd()
}

func (w *WidgetCell) SetWidget(content fyne.CanvasObject, id widget.TableCellID) {
	// Only replace widget if the cell position changed
	if w.currentID.Col != id.Col || w.currentID.Row != id.Row {
		w.content = content
		w.currentID = id
	}
	w.Refresh()
}

func (w *WidgetCell) CreateRenderer() fyne.WidgetRenderer {
	clip := container.NewClip(w.content)
	stack := container.NewStack(w.background, clip)
	return &widgetCellRenderer{
		cell:  w,
		clip:  clip,
		stack: stack,
	}
}

type widgetCellRenderer struct {
	cell  *WidgetCell
	clip  *container.Clip
	stack *fyne.Container
}

func (r *widgetCellRenderer) Destroy() {}

func (r *widgetCellRenderer) Layout(size fyne.Size) {
	r.stack.Resize(size)
}

func (r *widgetCellRenderer) MinSize() fyne.Size {
	if r.cell.content != nil {
		return r.cell.content.MinSize()
	}
	return fyne.NewSize(0, 0)
}

func (r *widgetCellRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.stack}
}

func (r *widgetCellRenderer) Refresh() {
	// Update the clip container's content when cell content changes
	if r.clip.Content != r.cell.content {
		r.clip.Content = r.cell.content
	}
	r.cell.background.Refresh()
	r.stack.Refresh()
}
