package ui

import (
	"fmt"
	"image/color"
	"path"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/widget/tablewidget"
)

const (
	colIcon = iota
	colName
	colSize
	colModified
	colTags
)

const pageSize = 200

// bookmarkResource is the toolbar "bookmark current path" icon (a Material
// bookmark ribbon, colorized by the theme). The SVG fill is replaced at render
// time by theme.NewThemedResource, so only the shape matters.
var bookmarkResource = theme.NewThemedResource(fyne.NewStaticResource("bookmark", []byte(
	`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000000" d="M17 3H7c-1.1 0-1.99.9-1.99 2L5 21l7-3 7 3V5c0-1.1-.9-2-2-2z"/></svg>`,
)))

// FileManager is one browsing panel: breadcrumb + toolbar + table (+ tag panel
// when browsing tie).
type FileManager struct {
	win      fyne.Window
	registry *fs.Registry
	ops      *fs.Operations
	cfg      *config.Config // shared app settings (file-type app associations)
	other    *FileManager   // sibling panel, for copy/move targets

	currentPath binding.String
	entries     []fs.Entry
	pageEntries []fs.Entry // entries shown on the current page (for click mapping)
	// selectedEntries are the rows currently selected (multi-select). Selection
	// is by display row, so it is cleared whenever the view reshapes.
	selectedEntries []fs.Entry

	sortCol string
	sortAsc bool

	// tie tag-filter (query mode) state
	queryInclude []string
	queryExclude []string

	table        *tablewidget.TableWidget
	toolbar      *widget.Toolbar
	addr         *widget.Entry
	breadcrumb   *fyne.Container
	tagPanel     *TagPanel
	content      *fyne.Container
	wrapper      *fyne.Container    // content + active-panel highlight border
	activeBorder *canvas.Rectangle  // outline shown when this panel is active
	onActivate   func(*FileManager) // called when this panel should become active
	onBookmark   func(path string)  // called by the toolbar bookmark button

	dragGhost *widget.PopUp // floating indicator shown while dragging rows
	dragLabel string        // text for the drag indicator (empty when not dragging)

	history []string
	histIdx int
}

func NewFileManager(startPath string, registry *fs.Registry, ops *fs.Operations, cfg *config.Config, win fyne.Window) *FileManager {
	fm := &FileManager{
		win:         win,
		registry:    registry,
		ops:         ops,
		cfg:         cfg,
		currentPath: binding.NewString(),
		sortCol:     "Name",
		sortAsc:     true,
		histIdx:     -1,
	}

	fm.table = tablewidget.NewTableWidget("files", pageSize)
	fm.table.RowCount = func() int { return len(fm.entries) }
	fm.table.Data = fm.buildPage
	fm.table.OnRowSelected = fm.selectRow
	fm.table.OnRowActivated = fm.activate
	fm.table.OnRowMenu = fm.showMenu
	fm.table.OnDragStart = fm.onDragStart
	fm.table.OnDragMove = fm.onDragMove
	fm.table.OnDrop = fm.onDrop

	ft := fm.table.GetFlexTable()
	ft.SetCreateCell(fm.createCell)
	ft.SetUpdateCell(fm.updateCell)
	ft.OnSort = fm.onSort
	ft.SelectColumn = colIcon // click the icon to select; click elsewhere to open/navigate

	ft.SetSort(fm.sortCol, fm.sortAsc)

	ft.SetColumnWidth(colIcon, 36)
	ft.SetColumnWidth(colName, 240)
	ft.SetColumnWidth(colSize, 100)
	ft.SetColumnWidth(colModified, 160)
	ft.SetColumnWidth(colTags, 240)

	fm.addr = widget.NewEntry()
	fm.addr.OnSubmitted = func(s string) { fm.navigateTo(strings.TrimSpace(s)) }

	fm.toolbar = widget.NewToolbar(
		widget.NewToolbarAction(theme.NavigateBackIcon(), fm.goBack),
		widget.NewToolbarAction(theme.NavigateNextIcon(), fm.goForward),
		widget.NewToolbarAction(theme.MoveUpIcon(), fm.goUp),
		widget.NewToolbarAction(theme.ViewRefreshIcon(), func() { fm.reload() }),
		widget.NewToolbarAction(theme.FolderNewIcon(), fm.newDir),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(bookmarkResource, fm.bookmarkCurrent),
	)

	fm.breadcrumb = container.NewHBox()
	fm.tagPanel = NewTagPanel(fm)

	top := container.NewVBox(fm.toolbar, fm.addr, fm.breadcrumb)
	center := fm.table.Instance
	fm.content = container.NewBorder(top, nil, nil, fm.tagPanel.Container, center)

	// A transparent-fill rectangle overlaid on top draws a 2px outline only when
	// this panel is active; canvas objects don't intercept pointer events, so it
	// doesn't interfere with the table below.
	fm.activeBorder = canvas.NewRectangle(color.Transparent)
	fm.activeBorder.StrokeWidth = 2
	fm.activeBorder.StrokeColor = color.Transparent
	// A transparent tappable layer sits behind the content so that clicks on
	// empty whitespace (below the rows, blank breadcrumb area) still activate the
	// panel; taps on real widgets above are consumed before reaching it.
	bg := newTappableBackground(fm.markActive)
	fm.wrapper = container.NewStack(bg, fm.content, fm.activeBorder)

	fm.pushHistory(startPath)
	fm.setPath(startPath)
	return fm
}

// SetSibling wires the other panel used as a copy/move destination.
func (fm *FileManager) SetSibling(other *FileManager) { fm.other = other }

func (fm *FileManager) View() *fyne.Container { return fm.wrapper }

// SetOnActive registers the handler called when this panel should become the
// active one (on any user interaction with it).
func (fm *FileManager) SetOnActive(fn func(*FileManager)) { fm.onActivate = fn }

// SetBookmarkHandler registers the handler invoked by the toolbar bookmark
// button with this panel's current path.
func (fm *FileManager) SetBookmarkHandler(fn func(path string)) { fm.onBookmark = fn }

// bookmarkCurrent notifies the registered handler of the panel's current path.
func (fm *FileManager) bookmarkCurrent() {
	fm.markActive()
	if fm.onBookmark != nil {
		fm.onBookmark(fm.CurrentPath())
	}
}

// activePanelColor is the pastel purple outline of the active panel.
var activePanelColor = color.NRGBA{R: 179, G: 157, B: 219, A: 255}

// SetActive toggles the active-panel highlight.
func (fm *FileManager) SetActive(active bool) {
	if active {
		fm.activeBorder.StrokeColor = activePanelColor
	} else {
		fm.activeBorder.StrokeColor = color.Transparent
	}
	fm.activeBorder.Refresh()
}

// markActive notifies the coordinator that this panel was interacted with.
func (fm *FileManager) markActive() {
	if fm.onActivate != nil {
		fm.onActivate(fm)
	}
}

// tappableBackground is a transparent, full-area widget that reports taps. Used
// behind a panel's content so clicks on empty space activate the panel.
type tappableBackground struct {
	widget.BaseWidget
	onTapped func()
}

func newTappableBackground(onTapped func()) *tappableBackground {
	b := &tappableBackground{onTapped: onTapped}
	b.ExtendBaseWidget(b)
	return b
}

func (b *tappableBackground) Tapped(*fyne.PointEvent) {
	if b.onTapped != nil {
		b.onTapped()
	}
}

func (b *tappableBackground) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

// NavigateTo navigates this panel to the given tie-fm path (exported for the
// favorites sidebar).
func (fm *FileManager) NavigateTo(p string) { fm.navigateTo(p) }

func (fm *FileManager) pathValue() string {
	v, _ := fm.currentPath.Get()
	return v
}

// CurrentPath returns the tie-fm URI this panel is showing.
func (fm *FileManager) CurrentPath() string { return fm.pathValue() }

// Reload re-lists the current location (e.g. after the tie client is swapped).
func (fm *FileManager) Reload() { fm.reload() }

func (fm *FileManager) isTie() bool { return fs.IsTie(fm.pathValue()) }

func (fm *FileManager) queryActive() bool {
	return fm.isTie() && (len(fm.queryInclude) > 0 || len(fm.queryExclude) > 0)
}

// --- navigation ---

func (fm *FileManager) navigateTo(p string) {
	if p == "" {
		return
	}
	fm.markActive()
	fm.queryInclude, fm.queryExclude = nil, nil
	fm.pushHistory(p)
	fm.setPath(p)
}

func (fm *FileManager) setPath(p string) {
	fm.currentPath.Set(p)
	fm.addr.SetText(p)
	fm.table.ClearFilter()
	fm.table.ResetPage()
	fm.reload()
	fm.updateBreadcrumb()
	fm.tagPanel.OnLocationChanged()
}

func (fm *FileManager) reload() {
	fm.table.ClearSelection()
	fm.selectedEntries = nil
	provider := fm.registry.For(fm.pathValue())
	var entries []fs.Entry
	var err error
	if fm.queryActive() {
		if ts, ok := provider.(fs.TagStore); ok {
			entries, _, err = ts.FilesWithTags(fm.queryInclude, fm.queryExclude, 0, -1)
		}
	} else {
		entries, err = provider.List(fm.pathValue())
	}
	if err != nil {
		dialog.ShowError(err, fm.win)
		entries = nil
	}
	fm.entries = entries
	fm.applySort()
	fm.table.Refresh()
}

func (fm *FileManager) goUp() {
	p := fm.pathValue()
	if fs.IsTie(p) {
		tp := strings.TrimPrefix(p, "tie:")
		if tp == "/" || tp == "" {
			return
		}
		fm.navigateTo("tie:" + parentPath(tp))
		return
	}
	if p == "/" {
		return
	}
	fm.navigateTo(path.Dir(p))
}

func parentPath(p string) string {
	d := path.Dir(p)
	if d == "." {
		return "/"
	}
	return d
}

func (fm *FileManager) pushHistory(p string) {
	// drop any forward history
	if fm.histIdx >= 0 && fm.histIdx < len(fm.history)-1 {
		fm.history = fm.history[:fm.histIdx+1]
	}
	fm.history = append(fm.history, p)
	fm.histIdx = len(fm.history) - 1
}

func (fm *FileManager) goBack() {
	if fm.histIdx <= 0 {
		return
	}
	fm.histIdx--
	fm.setPath(fm.history[fm.histIdx])
}

func (fm *FileManager) goForward() {
	if fm.histIdx >= len(fm.history)-1 {
		return
	}
	fm.histIdx++
	fm.setPath(fm.history[fm.histIdx])
}

// --- breadcrumb ---

func (fm *FileManager) updateBreadcrumb() {
	fm.breadcrumb.RemoveAll()
	for _, seg := range breadcrumbSegments(fm.pathValue()) {
		target := seg.target
		fm.breadcrumb.Add(widget.NewButton(seg.label, func() { fm.navigateTo(target) }))
	}
	fm.breadcrumb.Refresh()
}

type crumb struct {
	label  string
	target string
}

func breadcrumbSegments(p string) []crumb {
	scheme := ""
	body := p
	if fs.IsTie(p) {
		scheme = "tie:"
		body = strings.TrimPrefix(p, "tie:")
	}
	if body == "" {
		body = "/"
	}
	crumbs := []crumb{{label: scheme + "/", target: scheme + "/"}}
	acc := ""
	for _, part := range strings.Split(strings.Trim(body, "/"), "/") {
		if part == "" {
			continue
		}
		acc += "/" + part
		crumbs = append(crumbs, crumb{label: part, target: scheme + acc})
	}
	return crumbs
}

// --- table data + cell rendering ---

func (fm *FileManager) buildPage(offset, limit int) *tablewidget.TableData {
	end := offset + limit
	if end > len(fm.entries) {
		end = len(fm.entries)
	}
	if offset > len(fm.entries) {
		offset = len(fm.entries)
	}
	page := fm.entries[offset:end]
	fm.pageEntries = page

	// Lazily load tags for tie entries on this page.
	if ts, ok := fm.registry.For(fm.pathValue()).(fs.TagStore); ok {
		for i := range page {
			e := &fm.entries[offset+i]
			if e.Hash != "" && e.Tags == nil {
				if tags, err := ts.GetTags(*e); err == nil {
					if tags == nil {
						tags = []string{}
					}
					e.Tags = tags
				}
			}
		}
		page = fm.entries[offset:end]
		fm.pageEntries = page
	}

	td := tablewidget.NewTableData("files")
	for _, e := range page {
		icon := "file"
		if e.IsDir {
			icon = "dir"
		}
		td.AddStringCell("", icon)
		td.AddStringCell("Name", e.Name)
		if e.IsDir {
			td.AddNumericCell("Size", "", 0)
		} else {
			td.AddNumericCell("Size", sizeString(e.Size), float64(e.Size))
		}
		mod := ""
		var modKey float64
		if !e.ModTime.IsZero() {
			mod = e.ModTime.Format("2006-01-02 15:04:05")
			modKey = float64(e.ModTime.Unix())
		}
		td.AddNumericCell("Modified", mod, modKey)
		td.AddStringCell("Tags", strings.Join(e.Tags, ", "))
	}
	return td
}

func (fm *FileManager) createCell(col, row int) fyne.CanvasObject {
	if col == colIcon {
		return widget.NewIcon(theme.FileIcon())
	}
	lbl := widget.NewLabel("")
	lbl.Truncation = fyne.TextTruncateEllipsis
	return lbl
}

func (fm *FileManager) updateCell(col, row int, obj fyne.CanvasObject) {
	ft := fm.table.GetFlexTable()
	if col == colIcon {
		if ic, ok := obj.(*widget.Icon); ok {
			if ft.CellText(colIcon, row) == "dir" {
				ic.SetResource(theme.FolderIcon())
			} else {
				variant := fyne.CurrentApp().Settings().ThemeVariant()
				ic.SetResource(icons.FileIcon(ft.CellText(colName, row), variant))
			}
		}
		return
	}
	if lbl, ok := obj.(*widget.Label); ok {
		lbl.SetText(ft.CellText(col, row))
	}
}

// --- sorting (owns the entry slice; see FlexTable.OnSort) ---

func (fm *FileManager) onSort(colId string, ascending bool) {
	fm.sortCol = colId
	fm.sortAsc = ascending
	fm.table.ClearSelection()
	fm.selectedEntries = nil
	fm.tagPanel.OnSelectionChanged()
	fm.applySort()
	fm.table.Refresh()
}

func (fm *FileManager) applySort() {
	col := fm.sortCol
	asc := fm.sortAsc
	sort.SliceStable(fm.entries, func(i, j int) bool {
		a, b := fm.entries[i], fm.entries[j]
		var less bool
		switch col {
		case "Size":
			less = a.Size < b.Size
		case "Modified":
			less = a.ModTime.Before(b.ModTime)
		case "Tags":
			less = strings.Join(a.Tags, ",") < strings.Join(b.Tags, ",")
		case "": // type column: directories first
			less = a.IsDir && !b.IsDir
		default: // Name
			less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if asc {
			return less
		}
		return !less
	})
}

// --- interaction ---

func (fm *FileManager) selectRow(row int) {
	fm.markActive()
	fm.selectedEntries = fm.selectedEntries[:0]
	for _, r := range fm.table.SelectedRows() {
		if r >= 0 && r < len(fm.pageEntries) {
			fm.selectedEntries = append(fm.selectedEntries, fm.pageEntries[r])
		}
	}
	fm.tagPanel.OnSelectionChanged()
}

// clearSelection deselects all rows and updates the tag panel.
func (fm *FileManager) clearSelection() {
	fm.table.ClearSelection()
	fm.selectedEntries = nil
	fm.tagPanel.OnSelectionChanged()
}

func (fm *FileManager) activate(row int) {
	fm.markActive()
	if row < 0 || row >= len(fm.pageEntries) {
		return
	}
	e := fm.pageEntries[row]
	if e.IsDir {
		fm.navigateTo(e.Path)
		return
	}
	provider := fm.registry.For(e.Path)
	// Stream media through a configured player (mpv/vlc handle URLs) rather than
	// downloading the whole file first. Requires a configured app so we never
	// hand a URL to xdg-open (which would open a browser).
	if isStreamable(e.Name) && fm.cfg != nil && fm.cfg.AppFor(e.Name) != "" {
		if s, ok := provider.(fs.Streamer); ok {
			if url, err := s.StreamURL(e); err == nil {
				if err := openLocal(fm.cfg, url, e.Name); err != nil {
					dialog.ShowError(err, fm.win)
				}
				return
			}
		}
	}
	local, err := provider.Materialize(e)
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	if err := openLocal(fm.cfg, local, e.Name); err != nil {
		dialog.ShowError(err, fm.win)
	}
}

// openWith materializes the entry then prompts for (and remembers) the app to
// open its file type with, opening it immediately on save.
func (fm *FileManager) openWith(e fs.Entry) {
	local, err := fm.registry.For(e.Path).Materialize(e)
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	promptOpenWith(fm.win, fm.cfg, e.Name, local, true, nil)
}

func (fm *FileManager) showMenu(row int, obj fyne.CanvasObject) {
	fm.markActive()
	if row < 0 || row >= len(fm.pageEntries) {
		return
	}
	e := fm.pageEntries[row]
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open", func() { fm.activate(row) }),
	}
	if !e.IsDir {
		items = append(items, fyne.NewMenuItem("Open with…", func() { fm.openWith(e) }))
	}
	// Transfers target the other panel's current directory.
	if fm.other != nil {
		items = append(items, fm.transferItems([]fs.Entry{e}, fm.other, fs.IsTie(e.Path))...)
	}
	if !fs.IsTie(e.Path) {
		items = append(items,
			fyne.NewMenuItem("Delete", func() { fm.confirmDelete(e) }),
		)
	}
	if _, ok := fm.registry.For(e.Path).(fs.Stater); ok && e.Hash != "" {
		items = append(items, fyne.NewMenuItem("Properties", func() { fm.showProperties(e) }))
	}

	canvas := fyne.CurrentApp().Driver().CanvasForObject(obj)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu(e.Name, items...), canvas, pos)
}

// showProperties opens a read-only dialog summarizing an entry's tie metadata,
// sourced from the shared client Stat aggregation via the fs.Stater backend.
func (fm *FileManager) showProperties(e fs.Entry) {
	st, ok := fm.registry.For(e.Path).(fs.Stater)
	if !ok {
		return
	}
	info, err := st.Stat(e)
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}

	var items []*widget.FormItem
	add := func(label, value string) {
		if value != "" {
			items = append(items, widget.NewFormItem(label, widget.NewLabel(value)))
		}
	}
	typ := info.Kind
	if info.TieType != "" && info.TieType != "directory" {
		typ = info.Kind + " (" + info.TieType + ")"
	}
	add("Type", typ)
	add("Filename", info.Filename)
	add("Media type", info.MediaType)
	if info.Size > 0 || info.Kind != "directory" {
		add("Size", fmt.Sprintf("%s (%d bytes)", humanizeBytes(info.Size), info.Size))
	}
	if len(info.Tags) > 0 {
		add("Tags", strings.Join(info.Tags, ", "))
	}
	if !info.TagDate.IsZero() {
		add("Tagged", info.TagDate.Format("2006-01-02 15:04:05"))
	}
	for _, k := range []string{"title", "artist", "album", "year", "track"} {
		if v, ok := info.Meta[k]; ok {
			add(strings.ToUpper(k[:1])+k[1:], v)
		}
	}
	if len(info.Paths) > 0 {
		add("Path", strings.Join(info.Paths, ", "))
	}
	if info.Kind == "directory" {
		add("Children", fmt.Sprintf("%d dirs, %d files, %d archives", info.SubDirCount, info.FileCount, info.ArchiveCount))
	}
	if info.TotalSize > 0 {
		add("Total size", fmt.Sprintf("%s (%d bytes)", humanizeBytes(info.TotalSize), info.TotalSize))
	}
	if info.VersionCount > 0 {
		add("Versions", fmt.Sprintf("%d", info.VersionCount))
	}

	dialog.ShowCustom("Properties: "+e.Name, "Close", widget.NewForm(items...), fm.win)
}

// humanizeBytes formats a byte count with a binary (KiB/MiB/…) unit, e.g.
// "1.4 MiB"; values under 1 KiB are shown as plain bytes.
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// transferItems builds the copy/move menu items for transferring set into the
// target panel's current directory. The supported directions are: local→local
// (copy + move), local→tie (copy + move, via the tie Importer), and tie→local
// (copy only — moving out of tie is unsupported). tie→tie yields no items. Each
// completed op reloads the destination panel (and, for a move, this source
// panel) so both views reflect the result.
func (fm *FileManager) transferItems(set []fs.Entry, target *FileManager, srcIsTie bool) []*fyne.MenuItem {
	if len(set) == 0 {
		return nil
	}
	dest := target.destEntry()
	destIsTie := target.isTie()
	onDone := func(alsoSource bool) func(*fs.Op) {
		return func(*fs.Op) {
			fyne.Do(func() {
				target.reload()
				if alsoSource {
					fm.reload()
				}
			})
		}
	}
	copyAll := func() {
		for _, e := range set {
			fm.ops.Copy(e, dest, onDone(false))
		}
	}
	moveAll := func() {
		for _, e := range set {
			fm.ops.Move(e, dest, onDone(true))
		}
	}
	// Name the destination folder so the target (the other panel's directory) is
	// unambiguous — "here" wrongly reads as the panel that was clicked.
	destName := dest.Name
	if destName == "" || destName == "." {
		destName = target.pathValue()
	}
	switch {
	case destIsTie && !srcIsTie:
		return []*fyne.MenuItem{
			fyne.NewMenuItem("Copy into tie", copyAll),
			fyne.NewMenuItem("Move into tie", moveAll),
		}
	case !destIsTie && srcIsTie:
		return []*fyne.MenuItem{
			fyne.NewMenuItem(fmt.Sprintf("Copy to %q", destName), copyAll),
		}
	case !destIsTie && !srcIsTie:
		return []*fyne.MenuItem{
			fyne.NewMenuItem(fmt.Sprintf("Copy to %q", destName), copyAll),
			fyne.NewMenuItem(fmt.Sprintf("Move to %q", destName), moveAll),
		}
	}
	return nil
}

// dragSet returns the entries a drag carries: the current multi-selection, or
// the grabbed row when nothing is selected.
func (fm *FileManager) dragSet(row int) []fs.Entry {
	if len(fm.selectedEntries) > 0 {
		return append([]fs.Entry(nil), fm.selectedEntries...)
	}
	if row >= 0 && row < len(fm.pageEntries) {
		return []fs.Entry{fm.pageEntries[row]}
	}
	return nil
}

// onDragStart records what is being dragged. The indicator itself is created
// lazily on the first move (that is when we first have a cursor position).
func (fm *FileManager) onDragStart(row int) {
	fm.markActive()
	set := fm.dragSet(row)
	if len(set) == 0 {
		fm.dragLabel = ""
		return
	}
	if len(set) > 1 {
		fm.dragLabel = fmt.Sprintf("%d items", len(set))
	} else {
		fm.dragLabel = set[0].Name
	}
}

// onDragMove shows/moves a small indicator that follows the cursor (offset so
// it does not sit directly under the pointer).
func (fm *FileManager) onDragMove(absPos fyne.Position) {
	if fm.dragLabel == "" {
		return
	}
	pos := absPos.Add(fyne.NewPos(12, 12))
	if fm.dragGhost == nil {
		content := container.NewHBox(widget.NewIcon(theme.ContentCopyIcon()), widget.NewLabel(fm.dragLabel))
		fm.dragGhost = widget.NewPopUp(content, fm.win.Canvas())
		fm.dragGhost.ShowAtPosition(pos)
		return
	}
	fm.dragGhost.Move(pos)
}

// hideDragGhost removes the drag indicator if present.
func (fm *FileManager) hideDragGhost() {
	if fm.dragGhost != nil {
		fm.dragGhost.Hide()
		fm.dragGhost = nil
	}
	fm.dragLabel = ""
}

// onDrop fires when a row drag is released. If the pointer landed on the sibling
// panel it pops a copy/move menu targeting that panel's current directory.
func (fm *FileManager) onDrop(row int, absPos fyne.Position) {
	fm.hideDragGhost()
	if fm.other == nil {
		return
	}
	set := fm.dragSet(row)
	if len(set) == 0 {
		return
	}
	target := fm.other
	drv := fyne.CurrentApp().Driver()
	origin := drv.AbsolutePositionForObject(target.content)
	size := target.content.Size()
	inside := absPos.X >= origin.X && absPos.X < origin.X+size.Width &&
		absPos.Y >= origin.Y && absPos.Y < origin.Y+size.Height
	if !inside {
		return // dropped outside the sibling panel
	}
	items := fm.transferItems(set, target, fs.IsTie(set[0].Path))
	if len(items) == 0 {
		return
	}
	canvas := drv.CanvasForObject(target.content)
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("Transfer", items...), canvas, absPos)
}

// newDir prompts for a name and creates a directory in the current location,
// then reloads. It is a no-op for backends that can't create directories.
func (fm *FileManager) newDir() {
	fm.markActive()
	maker, ok := fm.registry.For(fm.pathValue()).(fs.DirMaker)
	if !ok {
		return
	}
	entry := widget.NewEntry()
	entry.PlaceHolder = "Folder name"
	dialog.ShowForm("New folder", "Create", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", entry)},
		func(confirmed bool) {
			if !confirmed {
				return
			}
			name := strings.TrimSpace(entry.Text)
			if name == "" {
				return
			}
			if err := maker.Mkdir(fm.pathValue(), name); err != nil {
				dialog.ShowError(err, fm.win)
				return
			}
			fm.reload()
		}, fm.win)
}

func (fm *FileManager) confirmDelete(e fs.Entry) {
	dialog.ShowConfirm("Delete", fmt.Sprintf("Delete %q?", e.Name), func(ok bool) {
		if !ok {
			return
		}
		if err := fs.RemoveLocal(e); err != nil {
			dialog.ShowError(err, fm.win)
			return
		}
		fm.reload()
	}, fm.win)
}

// destEntry describes this panel's current directory as a copy/move target.
func (fm *FileManager) destEntry() fs.Entry {
	p := fm.pathValue()
	return fs.Entry{Path: strings.TrimPrefix(p, "file:"), Name: path.Base(p), IsDir: true}
}

// updateEntryTags updates the cached tags for the entry with the given hash and
// refreshes the table so the Tags column reflects the change.
func (fm *FileManager) updateEntryTags(hash string, tags []string) {
	for i := range fm.entries {
		if fm.entries[i].Hash == hash {
			fm.entries[i].Tags = tags
		}
	}
	fm.table.Refresh()
}

func sizeString(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
