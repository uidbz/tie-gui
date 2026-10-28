package ui

import (
	"context"
	"fmt"
	"image/color"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

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
	"github.com/uidbz/tie-gui/gallery"
)

const (
	colIcon = iota
	colName
	colSize
	colModified
	colTags
)

const pageSize = 2000

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

	table          *tablewidget.TableWidget
	toolbar        *widget.Toolbar
	terminalButton *widget.Button
	addr           *shortcutEntry
	breadcrumb     *fyne.Container
	tagPanel       *TagPanel
	content        *fyne.Container
	wrapper        *fyne.Container    // content + active-panel highlight border
	activeBorder   *canvas.Rectangle  // outline shown when this panel is active
	onActivate     func(*FileManager) // called when this panel should become active
	onBookmark     func(path string)  // called by the toolbar bookmark button

	dragGhost *widget.PopUp // floating indicator shown while dragging rows
	dragLabel string        // text for the drag indicator (empty when not dragging)

	// preview is the per-pane thumbnail grid (folders, images, videos),
	// created on first toggle via the toolbar button; nil until InitPreview
	// wires it.
	preview *previewController

	// free-text search state (tie only). searchActive means the table is
	// showing search results rather than a directory listing; searchBasePath is
	// the location to restore when the search is cleared.
	searchEntry    *widget.Entry
	searchStatus   *widget.Label
	searchClear    *widget.Button
	searchActive   bool
	searchQuery    string
	searchTotal    int
	searchBasePath string
	searchCancel   context.CancelFunc

	// reloadGen counts reload requests (UI goroutine only). A background list
	// fetch only publishes its result when its generation is still the latest,
	// so rapid navigation can't have older listings land after newer ones.
	// reloadWG tracks in-flight fetches; waitReload is the test seam.
	reloadGen int
	reloadWG  sync.WaitGroup

	// initialLoad marks the constructor's first setPath so the first listing
	// loads synchronously (see reloadNow).
	initialLoad bool

	// openWG tracks in-flight openEntry materializations; waitOpen is the
	// test seam for the async open path.
	openWG sync.WaitGroup

	// deleteWG tracks in-flight deleteEntries goroutines; waitDeletes is the
	// test seam.
	deleteWG sync.WaitGroup

	history []string
	histIdx int
}

// waitOpen blocks until all in-flight openEntry downloads/launches finished.
func (fm *FileManager) waitOpen() { fm.openWG.Wait() }

// waitDeletes blocks until all in-flight deleteEntries goroutines finished
// (including the reload they trigger). Test seam.
func (fm *FileManager) waitDeletes() { fm.deleteWG.Wait() }

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

	fm.addr = newShortcutEntry(func(s fyne.Shortcut) {
		if s.ShortcutName() == FilterShortcut.ShortcutName() {
			fm.ShowFilter()
		}
	})
	fm.addr.OnSubmitted = func(s string) { fm.navigateTo(strings.TrimSpace(s)) }

	// A text toolbar item: the terminal action is the one location command that
	// users look for by name. It is disabled for tie:/mtp: locations, which
	// have no real filesystem path to open.
	fm.terminalButton = widget.NewButtonWithIcon("Open Terminal Here", theme.ComputerIcon(), fm.openTerminalHere)
	fm.terminalButton.Alignment = widget.ButtonAlignLeading
	fm.terminalButton.Importance = widget.LowImportance

	fm.toolbar = widget.NewToolbar(
		widget.NewToolbarAction(theme.NavigateBackIcon(), fm.goBack),
		widget.NewToolbarAction(theme.NavigateNextIcon(), fm.goForward),
		widget.NewToolbarAction(theme.MoveUpIcon(), fm.goUp),
		widget.NewToolbarAction(theme.ViewRefreshIcon(), func() { fm.reload() }),
		widget.NewToolbarAction(theme.SearchIcon(), fm.focusSearch),
		widget.NewToolbarAction(theme.FolderNewIcon(), fm.newDir),
		toolbarButtonItem{fm.terminalButton},
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(bookmarkResource, fm.bookmarkCurrent),
	)

	fm.breadcrumb = container.NewHBox()
	fm.tagPanel = NewTagPanel(fm)

	// Free-text search: one entry under the breadcrumb, a status line and a
	// Clear button. Search is a temporary view of tie results; navigating away
	// clears it.
	fm.searchEntry = widget.NewEntry()
	fm.searchEntry.SetPlaceHolder("Search filenames and directories…")
	fm.searchEntry.OnSubmitted = fm.runSearch
	fm.searchStatus = widget.NewLabel("")
	fm.searchStatus.Hide()
	fm.searchClear = widget.NewButton("Clear", fm.clearSearch)
	fm.searchClear.Disable()
	searchRow := container.NewBorder(nil, nil, fm.searchClear, nil, fm.searchEntry)

	top := container.NewVBox(fm.toolbar, fm.addr, fm.breadcrumb, searchRow, fm.searchStatus)
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
	fm.initialLoad = true
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

// InitPreview attaches the pane's preview mode: a toolbar button toggles
// between the table and a gallery grid of folder/image/video thumbnails fed
// from the pane's listing. cfg and thumb are shared between panes (main
// builds them once); each pane gets its own embedded gallery on first use.
func (fm *FileManager) InitPreview(cfg gallery.Config, thumb gallery.Thumbnailer) {
	fm.preview = newPreviewController(fm, cfg, thumb)
	fm.toolbar.Append(widget.NewToolbarAction(theme.MediaPhotoIcon(), fm.togglePreview))
}

// ShowFilter reveals this pane's filter/page bar and focuses the filter box.
// Ignored in preview mode, where there is no table to filter.
func (fm *FileManager) ShowFilter() {
	if fm.preview != nil && fm.preview.on {
		return
	}
	fm.markActive()
	fm.table.ShowFilter()
}

func (fm *FileManager) togglePreview() {
	fm.markActive()
	if fm.preview != nil {
		fm.preview.toggle()
	}
}

// refreshPreview re-feeds the preview grid from the current (sorted) listing;
// a no-op while preview is off.
func (fm *FileManager) refreshPreview() {
	if fm.preview != nil {
		fm.preview.refresh()
	}
}

// PreviewHandlesKey routes key events to the preview grid while it is on.
// Returns true when the key was consumed (always, while preview is on).
func (fm *FileManager) PreviewHandlesKey(key *fyne.KeyEvent) bool {
	if fm.preview == nil {
		return false
	}
	return fm.preview.HandleKey(key)
}

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
	if fm.currentPath == nil {
		return ""
	}
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

// --- free-text search ---

// focusSearch focuses the search entry (toolbar action).
func (fm *FileManager) focusSearch() {
	fm.markActive()
	if fm.win != nil && fm.searchEntry != nil {
		fm.win.Canvas().Focus(fm.searchEntry)
	}
}

// runSearch runs a tie search and displays the ranked results in this pane's
// table. It is the OnSubmitted handler of the search entry and also the reload
// path while a search is active.
func (fm *FileManager) runSearch(query string) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 {
		fm.clearSearch()
		return
	}
	base := fm.searchBasePath
	if !fm.searchActive || base == "" {
		base = fm.pathValue()
		fm.searchBasePath = base
	}
	provider := fm.registry.For(base)
	searcher, ok := provider.(fs.Searcher)
	if !ok {
		dialog.ShowInformation("Search", "Search is available while browsing a tie collection.", fm.win)
		return
	}
	if fm.searchCancel != nil {
		fm.searchCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	fm.searchCancel = cancel
	fm.searchActive = true
	fm.searchQuery = query
	fm.searchClear.Enable()
	fm.searchStatus.SetText("Searching…")
	fm.searchStatus.Show()

	fm.reloadWG.Add(1)
	go func() {
		defer fm.reloadWG.Done()
		entries, total, err := searcher.Search(ctx, query, 0, 500)
		fyne.Do(func() {
			if !fm.searchActive || fm.searchQuery != query {
				return // a newer query won
			}
			if err != nil {
				fm.searchStatus.SetText("")
				fm.searchStatus.Hide()
				dialog.ShowError(err, fm.win)
				return
			}
			showHidden := fm.cfg != nil && fm.cfg.ShowHidden
			entries = visibleEntries(entries, showHidden)
			// Keep the server's rank within each group, but show directories
			// before files: a result list is still a file browser.
			sort.SliceStable(entries, func(i, j int) bool {
				return entries[i].IsDir && !entries[j].IsDir
			})
			fm.entries = entries
			fm.searchTotal = total
			fm.table.ClearFilter()
			fm.table.ResetPage()
			fm.table.Refresh()
			fm.refreshPreview()

			text := fmt.Sprintf("%d result", total)
			if total != 1 {
				text += "s"
			}
			if total > len(entries) {
				text += fmt.Sprintf(" (showing %d)", len(entries))
			}
			fm.searchStatus.SetText(fmt.Sprintf("%s for “%s”", text, query))
			fm.searchStatus.Show()
		})
	}()
}

// clearSearch leaves the result view and restores the location search started
// from.
func (fm *FileManager) clearSearch() {
	base := fm.searchBasePath
	fm.cancelSearchState()
	if fm.searchEntry != nil {
		fm.searchEntry.SetText("")
	}
	if base != "" {
		fm.setPath(base)
	} else {
		fm.reload()
	}
}

// cancelSearchState drops the active query and its in-flight request without
// touching the listing. setPath calls it before navigating, so opening a result
// exits search mode.
func (fm *FileManager) cancelSearchState() {
	if fm.searchCancel != nil {
		fm.searchCancel()
		fm.searchCancel = nil
	}
	fm.searchActive = false
	fm.searchQuery = ""
	fm.searchTotal = 0
	fm.searchBasePath = ""
	if fm.searchStatus != nil {
		fm.searchStatus.SetText("")
		fm.searchStatus.Hide()
	}
	if fm.searchClear != nil {
		fm.searchClear.Disable()
	}
}

func (fm *FileManager) setPath(p string) {
	fm.cancelSearchState()
	if fm.searchEntry != nil {
		fm.searchEntry.SetText("")
	}
	fm.currentPath.Set(p)
	fm.addr.SetText(p)
	fm.updateTerminalButton()
	fm.table.ClearFilter()
	fm.table.ResetPage()
	fm.updateBreadcrumb()
	fm.tagPanel.OnLocationChanged()
	// reload last: it kicks off a background fetch whose result is published
	// via fyne.Do, and nothing after it may touch widgets concurrently with a
	// fetch that could complete instantly (the Fyne test driver even runs Do
	// inline — any later widget work here races the apply).
	fm.reload()
}

func (fm *FileManager) reload() {
	fm.table.ClearSelection()
	fm.selectedEntries = nil
	if fm.searchActive {
		fm.runSearch(fm.searchQuery)
		return
	}
	// The initial listing (during NewFileManager) loads synchronously: the
	// constructor keeps wiring widgets right after setPath, and first paint
	// must see a complete listing. Every later reload is async.
	if fm.initialLoad {
		fm.initialLoad = false
		fm.reloadNow()
		return
	}
	provider := fm.registry.For(fm.pathValue())
	query := fm.queryActive()
	var ts fs.TagStore
	if query {
		ts, _ = provider.(fs.TagStore)
	}
	// Snapshot everything the fetch goroutine reads: the UI may navigate or
	// change the query before the goroutine runs, and these fields belong to
	// the UI goroutine.
	path := fm.pathValue()
	include, exclude := fm.queryInclude, fm.queryExclude
	// Generation guard: when several reloads overlap (rapid navigation), only
	// the newest one may publish its listing.
	gen := fm.reloadGen
	fm.reloadGen++
	fm.reloadWG.Add(1)
	go func() {
		defer fm.reloadWG.Done()
		fm.fetchAndApply(gen, provider, query, ts, path, include, exclude, true)
	}()
}

// reloadNow runs the list fetch and apply directly on the calling (UI)
// goroutine. Reserved for the initial load in NewFileManager.
func (fm *FileManager) reloadNow() {
	provider := fm.registry.For(fm.pathValue())
	query := fm.queryActive()
	var ts fs.TagStore
	if query {
		ts, _ = provider.(fs.TagStore)
	}
	gen := fm.reloadGen
	fm.reloadGen++
	fm.fetchAndApply(gen, provider, query, ts, fm.pathValue(), fm.queryInclude, fm.queryExclude, false)
}

// fetchAndApply fetches a listing (and bulk-fetches its first page's tags) on
// the calling goroutine and publishes it; async publishes via fyne.Do. The
// generation guard drops results of reloads superseded during the fetch.
func (fm *FileManager) fetchAndApply(gen int, provider fs.FileSystem, query bool, ts fs.TagStore, path string, include, exclude []string, async bool) {
	var entries []fs.Entry
	var err error
	if query && ts != nil {
		entries, _, err = ts.FilesWithTags(include, exclude, 0, -1)
	} else {
		entries, err = provider.List(path)
	}
	if err == nil {
		fm.prefetchTags(provider, entries)
	}
	apply := func() {
		if gen != fm.reloadGen-1 {
			return // a newer reload superseded this one
		}
		if err != nil {
			dialog.ShowError(err, fm.win)
			entries = nil
		}
		showHidden := fm.cfg != nil && fm.cfg.ShowHidden
		fm.entries = visibleEntries(entries, showHidden)
		fm.applySort()
		fm.table.Refresh()
		fm.refreshPreview()
	}
	if async {
		fyne.Do(apply)
	} else {
		apply()
	}
}

// waitReload blocks until all in-flight reload fetches have been published.
// Test seam: navigation/reload fetches run on goroutines, so tests set up
// listings by waiting for the reload the navigation started.
func (fm *FileManager) waitReload() {
	fm.reloadWG.Wait()
}

// prefetchTags fills the Tags of tie entries in one bulk round trip
// (fs.BulkTagger) instead of a GetTags per row during table paint. Only the
// first displayed page needs tags eagerly; later pages fall back to the
// bulk-per-page fetch in buildPage.
func (fm *FileManager) prefetchTags(provider fs.FileSystem, entries []fs.Entry) {
	bulk, ok := provider.(fs.BulkTagger)
	if !ok {
		return
	}
	limit := pageSize
	if limit > len(entries) {
		limit = len(entries)
	}
	var keys []string
	for _, e := range entries[:limit] {
		if e.Hash != "" && e.Tags == nil {
			keys = append(keys, e.Hash)
		}
	}
	if len(keys) == 0 {
		return
	}
	tags, err := bulk.TagsFor(keys)
	if err != nil {
		return // buildPage falls back to per-row fetches
	}
	for i := range entries[:limit] {
		if entries[i].Hash == "" {
			continue
		}
		if t, ok := tags[entries[i].Hash]; ok {
			entries[i].Tags = t
		} else {
			entries[i].Tags = []string{}
		}
	}
}

// visibleEntries drops dot-files (names with a leading ".") unless showHidden
// is set. Applies uniformly to every provider (local, tie, mtp).
func visibleEntries(entries []fs.Entry, showHidden bool) []fs.Entry {
	if showHidden {
		return entries
	}
	visible := make([]fs.Entry, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, ".") {
			visible = append(visible, e)
		}
	}
	return visible
}

func (fm *FileManager) goUp() {
	if fm.searchActive {
		fm.clearSearch()
		return
	}
	p := fm.pathValue()
	if scheme := fs.SchemeOf(p); scheme != "file" {
		// Any remote scheme (tie:, mtp:) carries a plain slash path after the
		// scheme prefix — handle them uniformly.
		tp := strings.TrimPrefix(p, scheme+":")
		if tp == "/" || tp == "" {
			return
		}
		fm.navigateTo(scheme + ":" + parentPath(tp))
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
	if fm.searchActive {
		fm.clearSearch()
		return
	}
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
	if s := fs.SchemeOf(p); s != "file" {
		scheme = s + ":"
		body = strings.TrimPrefix(p, scheme)
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

	// Lazily load tags for tie entries on this page: one bulk round trip per
	// page (fs.BulkTagger), falling back to per-entry fetches only on backends
	// without bulk support.
	if ts, ok := fm.registry.For(fm.pathValue()).(fs.TagStore); ok {
		var keys []string
		for i := range page {
			e := &fm.entries[offset+i]
			if e.Hash != "" && e.Tags == nil {
				keys = append(keys, e.Hash)
			}
		}
		if len(keys) > 0 {
			if bulk, ok := ts.(fs.BulkTagger); ok {
				if tags, err := bulk.TagsFor(keys); err == nil {
					for i := range page {
						e := &fm.entries[offset+i]
						if e.Hash != "" && e.Tags == nil {
							if t, ok := tags[e.Hash]; ok {
								e.Tags = t
							} else {
								e.Tags = []string{}
							}
						}
					}
				}
			} else {
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
	fm.refreshPreview()
}

func (fm *FileManager) applySort() {
	col := fm.sortCol
	asc := fm.sortAsc
	sort.SliceStable(fm.entries, func(i, j int) bool {
		a, b := fm.entries[i], fm.entries[j]
		// Descending swaps the operands instead of negating the result:
		// `return !less` would report equal rows as mutually less, which
		// violates the strict-weak-ordering contract of SliceStable.
		if !asc {
			a, b = b, a
		}
		switch col {
		case "Size":
			return a.Size < b.Size
		case "Modified":
			return a.ModTime.Before(b.ModTime)
		case "Tags":
			return strings.Join(a.Tags, ",") < strings.Join(b.Tags, ",")
		case "": // type column: directories first
			return a.IsDir && !b.IsDir
		default: // Name
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
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
	fm.openEntry(e)
}

// openEntry opens a file entry. A tie: URL association is handed the entry
// as "tie:<content hash>" (the app, e.g. tie-view, resolves metadata and
// content itself); a streaming association gets the backend's direct URL
// (fs.Streamer, e.g. tie over HTTP); otherwise the entry is materialized to
// a local temp copy first. Requires a configured association for either URL
// form — we never hand a URL to xdg-open (which would open a browser).
func (fm *FileManager) openEntry(e fs.Entry) {
	provider := fm.registry.For(e.Path)
	assoc, hasAssoc := config.AppAssoc{}, false
	if fm.cfg != nil {
		assoc, hasAssoc = fm.cfg.AppFor(e.Name)
	}
	if hasAssoc && assoc.TieURL && fs.IsTie(e.Path) && e.Hash != "" {
		// Hand over the entry's virtual path (e.Path is "tie:/dir/file"):
		// human-readable, and the receiving app (e.g. tie-view) can resolve
		// it to open the containing directory focused on the file. Entries
		// from tag-query listings carry a synthetic root path, but it
		// resolves to the same content either way.
		if err := launch(fm.cfg, e.Path, e.Name); err != nil {
			dialog.ShowError(err, fm.win)
		}
		return
	}
	if hasAssoc && assoc.Stream {
		if s, ok := provider.(fs.Streamer); ok {
			// StreamURL only resolves the configured filehost URL (no network);
			// the player fetches the bytes itself.
			if url, err := s.StreamURL(e); err == nil {
				if err := launch(fm.cfg, url, e.Name); err != nil {
					dialog.ShowError(err, fm.win)
				}
				return
			}
		}
	}
	// Materialize downloads remote (tie, mtp) content to a temp file — a full
	// blob transfer that must not run on the UI goroutine.
	if !fs.IsLocal(e.Path) {
		fm.openWG.Add(1)
		go func() {
			defer fm.openWG.Done()
			local, err := provider.Materialize(e)
			if err != nil {
				fyne.Do(func() { dialog.ShowError(err, fm.win) })
				return
			}
			if err := launch(fm.cfg, local, e.Name); err != nil {
				fyne.Do(func() { dialog.ShowError(err, fm.win) })
			}
		}()
		return
	}
	if err := launch(fm.cfg, fs.LocalPath(e.Path), e.Name); err != nil {
		dialog.ShowError(err, fm.win)
	}
}

// openWith prompts for (and remembers) the app to open the entry's file type
// with, then opens it through that association — streaming directly when the
// association says the app supports it, without a temp download.
func (fm *FileManager) openWith(e fs.Entry) {
	promptOpenWith(fm.win, fm.cfg, e.Name, func() { fm.openEntry(e) })
}

func (fm *FileManager) showMenu(row int, obj fyne.CanvasObject) {
	fm.markActive()
	if row < 0 || row >= len(fm.pageEntries) {
		return
	}
	e := fm.pageEntries[row]
	canvas := fyne.CurrentApp().Driver().CanvasForObject(obj)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu(e.Name, fm.contextMenuItems(e, row)...), canvas, pos)
}

// contextMenuItems builds the right-click menu entries for one row.
func (fm *FileManager) contextMenuItems(e fs.Entry, row int) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open", func() { fm.activate(row) }),
	}
	if !e.IsDir {
		items = append(items, fyne.NewMenuItem("Open with…", func() { fm.openWith(e) }))
	}
	// New file and Open in terminal act on the pane's current directory (the
	// row is only where the menu was opened), so they are offered whenever
	// the location's backend supports them.
	if _, ok := fm.registry.For(fm.pathValue()).(fs.FileMaker); ok {
		items = append(items, fyne.NewMenuItem("New file…", func() { fm.newFile() }))
	}
	// Open in terminal: a local directory opens itself, a local file opens its
	// containing directory (like Dolphin's "Open Terminal" on a file).
	if fs.IsLocal(e.Path) {
		items = append(items, fyne.NewMenuItem("Open Terminal Here", func() { fm.openInTerminal(e) }))
	}
	// Transfers target the other panel's current directory.
	if fm.other != nil {
		items = append(items, fm.transferItems([]fs.Entry{e}, fm.other, fs.IsTie(e.Path))...)
	}
	// Bulk album import scans local directories and imports the discovered
	// albums into tie at their rendered destinations. Right-clicking a row of
	// a multi-selection imports every selected local directory.
	if e.IsDir && fs.IsLocal(e.Path) {
		if _, ok := fm.registry.For("tie:/").(fs.Importer); ok {
			set := fm.menuSet(e)
			items = append(items, fyne.NewMenuItem("Import as albums…", func() { fm.importAsAlbums(set) }))
		}
	}
	// MusicBrainz lookup for already-imported albums/tracks (tie: and tie://
	// style paths). A directory is looked up as a release, an audio file as a
	// recording; a multi-file selection uses the shared parent album.
	if fs.IsTie(e.Path) && e.Hash != "" {
		set := fm.menuSet(e)
		items = append(items, fyne.NewMenuItem("MusicBrainz…", func() { fm.musicBrainzDialog(set) }))
	}
	if item := fm.extractMenuItem(e); item != nil {
		items = append(items, item)
	}
	// Delete covers the whole selection when the clicked row is part of it
	// (local files use os.RemoveAll; tie unlinks triples and keeps blobs).
	if set := fm.menuSet(e); fm.canDeleteAll(set) {
		label := "Delete"
		if len(set) > 1 {
			label = fmt.Sprintf("Delete %d items", len(set))
		}
		items = append(items, fyne.NewMenuItem(label, func() { fm.confirmDelete(set) }))
	}
	if _, ok := fm.registry.For(e.Path).(fs.DirTyper); ok && e.IsDir && e.Hash != "" {
		items = append(items, fyne.NewMenuItem("Directory type…", func() { fm.showDirTypeDialog(e) }))
	}
	if _, ok := fm.registry.For(e.Path).(fs.Stater); ok && e.Hash != "" {
		items = append(items, fyne.NewMenuItem("Properties", func() { fm.showProperties(e) }))
	}
	return items
}

// showProperties opens a read-only dialog summarizing an entry's tie metadata,
// sourced from the shared client Stat aggregation via the fs.Stater backend.
// The Stat aggregation walks the entry's subtree server-side, so it runs off
// the UI goroutine; the dialog opens when the data is in.
func (fm *FileManager) showProperties(e fs.Entry) {
	st, ok := fm.registry.For(e.Path).(fs.Stater)
	if !ok {
		return
	}
	go func() {
		info, err := st.Stat(e)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(err, fm.win)
				return
			}
			fm.showStatDialog(e, info)
		})
	}()
}

func (fm *FileManager) showStatDialog(e fs.Entry, info fs.StatInfo) {
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
	if len(info.DirTypes) > 0 {
		add("Dir type", strings.Join(info.DirTypes, ", "))
	}
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

// promptCustomDirType asks for a custom dir-type label and runs the transfer
// with it (the "Custom…" entry of a copy/move-into-tie-as submenu). Empty or
// cancelled input aborts the transfer.
func (fm *FileManager) promptCustomDirType(run func(dirType string)) {
	entry := widget.NewEntry()
	entry.PlaceHolder = "e.g. live-album"
	dialog.ShowForm("Custom directory type", "OK", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Type label", entry)},
		func(ok bool) {
			if !ok {
				return
			}
			label := strings.TrimSpace(entry.Text)
			if label == "" {
				return
			}
			run(label)
		}, fm.win)
}

// showDirTypeDialog opens a dialog to view and edit a tie directory's type
// labels (audio-dir, image-dir, …). The current labels are fetched in the
// background; Apply replaces them via the fs.DirTyper backend, so a directory
// can be re-classified (e.g. made an audio-dir album) or cleared entirely.
func (fm *FileManager) showDirTypeDialog(e fs.Entry) {
	dt, ok := fm.registry.For(e.Path).(fs.DirTyper)
	if !ok {
		return
	}
	go func() {
		current, err := dt.DirTypes(e)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, fm.win) })
			return
		}
		fyne.Do(func() {
			// Options are the built-in vocabulary plus any custom labels
			// already on the directory; new labels go through the Custom field.
			seen := map[string]bool{}
			var options []string
			for _, o := range append(append([]string{}, fs.BuiltinDirTypes...), current...) {
				if !seen[o] {
					seen[o] = true
					options = append(options, o)
				}
			}
			sort.Strings(options)
			checks := widget.NewCheckGroup(options, nil)
			checks.SetSelected(current)
			custom := widget.NewEntry()
			custom.PlaceHolder = "new custom type, comma-separated…"
			dialog.ShowForm("Directory type: "+e.Name, "Apply", "Cancel",
				[]*widget.FormItem{
					widget.NewFormItem("Types", checks),
					widget.NewFormItem("Custom", custom),
				}, func(ok bool) {
					if !ok {
						return
					}
					labels := append([]string{}, checks.Selected...)
					for _, l := range strings.Split(custom.Text, ",") {
						if l = strings.TrimSpace(l); l != "" {
							labels = append(labels, l)
						}
					}
					go func() {
						err := dt.SetDirTypes(e, labels)
						fyne.Do(func() {
							if err != nil {
								dialog.ShowError(err, fm.win)
								return
							}
							fm.reload()
						})
					}()
				}, fm.win)
		})
	}()
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
	// Each action is one fs.Transfer of the whole set, so Apply to All and
	// Cancel at a conflict prompt cover every selected item. Feed the queue off
	// the UI goroutine: it blocks once full, and the ops goroutine needs the UI
	// goroutine free to show the conflict dialog.
	copyAll := func() {
		tr := fm.ops.NewTransfer(len(set))
		go func() {
			for _, e := range set {
				tr.Copy(e, dest, onDone(false))
			}
		}()
	}
	moveAll := func() {
		tr := fm.ops.NewTransfer(len(set))
		go func() {
			for _, e := range set {
				tr.Move(e, dest, onDone(true))
			}
		}()
	}
	// copyAs/moveAs transfer with a dir-type label stamped on the imported
	// directory: the freshly created directory root when copying directories,
	// the destination directory itself when copying loose files.
	copyAs := func(dirType string) {
		tr := fm.ops.NewTransfer(len(set))
		go func() {
			for _, e := range set {
				tr.CopyAs(e, dest, dirType, onDone(false))
			}
		}()
	}
	moveAs := func(dirType string) {
		tr := fm.ops.NewTransfer(len(set))
		go func() {
			for _, e := range set {
				tr.MoveAs(e, dest, dirType, onDone(true))
			}
		}()
	}
	// dirTypeSubMenu lists the built-in dir-type vocabulary plus a custom-label
	// prompt; picking one runs the transfer with that label.
	dirTypeSubMenu := func(run func(dirType string)) *fyne.Menu {
		items := make([]*fyne.MenuItem, 0, len(fs.BuiltinDirTypes)+1)
		for _, dt := range fs.BuiltinDirTypes {
			dt := dt
			items = append(items, fyne.NewMenuItem(dt, func() { run(dt) }))
		}
		items = append(items, fyne.NewMenuItem("Custom…", func() {
			fm.promptCustomDirType(run)
		}))
		return fyne.NewMenu("", items...)
	}
	// Name the destination folder so the target (the other panel's directory) is
	// unambiguous — "here" wrongly reads as the panel that was clicked.
	destName := dest.Name
	if destName == "" || destName == "." {
		destName = target.pathValue()
	}
	switch {
	case destIsTie && !srcIsTie:
		copyAsItem := fyne.NewMenuItem("Copy into tie as", nil)
		copyAsItem.ChildMenu = dirTypeSubMenu(copyAs)
		moveAsItem := fyne.NewMenuItem("Move into tie as", nil)
		moveAsItem.ChildMenu = dirTypeSubMenu(moveAs)
		return []*fyne.MenuItem{
			fyne.NewMenuItem("Copy into tie", copyAll),
			fyne.NewMenuItem("Move into tie", moveAll),
			copyAsItem,
			moveAsItem,
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
	items := fm.dropMenuItems(set, target)
	if len(items) == 0 {
		return
	}
	canvas := drv.CanvasForObject(target.content)
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("Transfer", items...), canvas, absPos)
}

// extractMenuItem returns the "Extract archive to here" item when e is an
// archive, covering every archive of the selection when e is part of it (the
// non-archive rows of a mixed selection are ignored). "Here" is the pane's
// current directory, so the item is withheld from tag-query listings (their
// rows come from anywhere in the collection) and from locations whose backend
// cannot receive files.
func (fm *FileManager) extractMenuItem(e fs.Entry) *fyne.MenuItem {
	if e.IsDir || !fs.IsArchiveName(e.Name) || fm.queryActive() {
		return nil
	}
	here := fm.destEntry()
	if !fs.IsLocal(here.Path) {
		if _, ok := fm.registry.For(here.Path).(fs.Importer); !ok {
			return nil
		}
	}
	var archives []fs.Entry
	for _, s := range fm.menuSet(e) {
		if !s.IsDir && fs.IsArchiveName(s.Name) {
			archives = append(archives, s)
		}
	}
	label := "Extract archive to here"
	if len(archives) > 1 {
		label = fmt.Sprintf("Extract %d archives to here", len(archives))
	}
	return fyne.NewMenuItem(label, func() { fm.extractArchives(archives, here) })
}

// extractArchives enqueues one extraction op per archive into dest. Each
// finished op reloads the pane (and the sibling pane when it shows the same
// directory); failures are reported per archive.
func (fm *FileManager) extractArchives(archives []fs.Entry, dest fs.Entry) {
	fm.markActive()
	destPath := fm.pathValue()
	done := func(op *fs.Op) {
		fyne.Do(func() {
			if _, err := op.State(); err != nil {
				dialog.ShowError(fmt.Errorf("extracting %s: %w", op.A.Name, err), fm.win)
			}
			if fm.pathValue() == destPath {
				fm.reload()
			}
			if fm.other != nil && fm.other.pathValue() == destPath {
				fm.other.reload()
			}
		})
	}
	// The ops queue is small and feeding it blocks; enqueue off the UI goroutine.
	go func() {
		for _, a := range archives {
			fm.ops.Extract(a, dest, done)
		}
	}()
}

// menuSet is the entry set a context-menu action on e applies to: the whole
// selection when e is part of it, else just e.
func (fm *FileManager) menuSet(e fs.Entry) []fs.Entry {
	for _, s := range fm.selectedEntries {
		if s.Path == e.Path {
			return append([]fs.Entry(nil), fm.selectedEntries...)
		}
	}
	return []fs.Entry{e}
}

// dropMenuItems builds the drag-drop menu: the plain copy/move transfer items,
// plus "Import as albums…" when the drag carries only local directories
// (the bulk-scan flow, like the context menu's item).
func (fm *FileManager) dropMenuItems(set []fs.Entry, target *FileManager) []*fyne.MenuItem {
	items := fm.transferItems(set, target, fs.IsTie(set[0].Path))
	allLocalDirs := true
	for _, e := range set {
		if !e.IsDir || !fs.IsLocal(e.Path) {
			allLocalDirs = false
			break
		}
	}
	if allLocalDirs {
		set := append([]fs.Entry(nil), set...)
		items = append(items, fyne.NewMenuItem("Import as albums…", func() { fm.importAsAlbums(set) }))
	}
	return items
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
			go func() {
				err := maker.Mkdir(fm.pathValue(), name)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, fm.win)
						return
					}
					fm.reload()
				})
			}()
		}, fm.win)
}

// newFile prompts for a name and creates an empty file in the current
// location, then reloads. It is a no-op for backends that can't create files.
func (fm *FileManager) newFile() {
	fm.markActive()
	maker, ok := fm.registry.For(fm.pathValue()).(fs.FileMaker)
	if !ok {
		return
	}
	entry := widget.NewEntry()
	entry.PlaceHolder = "File name"
	dialog.ShowForm("New file", "Create", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", entry)},
		func(confirmed bool) {
			if !confirmed {
				return
			}
			name := strings.TrimSpace(entry.Text)
			if name == "" {
				return
			}
			go func() {
				err := maker.CreateFile(fm.pathValue(), name)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, fm.win)
						return
					}
					fm.reload()
				})
			}()
		}, fm.win)
}

// openInTerminal opens the configured terminal with its working directory at
// the entry's location: the directory itself for a folder, its containing
// directory for a file.
func (fm *FileManager) openInTerminal(e fs.Entry) {
	fm.markActive()
	dir := fs.LocalPath(e.Path)
	if !e.IsDir {
		dir = path.Dir(dir)
	}
	if err := launchTerminal(fm.cfg, dir); err != nil {
		dialog.ShowError(err, fm.win)
	}
}

// canDelete reports whether the entry's backend supports Delete. Local files
// use os.RemoveAll; tie implements fs.Remover (triples only, blob untouched).
func (fm *FileManager) canDelete(e fs.Entry) bool {
	if fs.IsLocal(e.Path) {
		return true
	}
	_, ok := fm.registry.For(e.Path).(fs.Remover)
	return ok
}

// canDeleteAll reports whether every entry of set can be deleted, so the menu
// never offers Delete for a selection with an unsupported member.
func (fm *FileManager) canDeleteAll(set []fs.Entry) bool {
	if len(set) == 0 {
		return false
	}
	for _, e := range set {
		if !fm.canDelete(e) {
			return false
		}
	}
	return true
}

// tieDeleteNote is the trailing explanation of a tie delete prompt: only tree
// references go, the content-addressed bytes stay for the filehost's retention.
const tieDeleteNote = "The tree references are removed; content stays in the tie filehost until the filehost's retention policy collects it."

// deletePromptText is the confirmation message for set: the single-item wording
// (naming the item; for tie, saying that only references go) or a counted
// summary listing the names, noting directories and (for tie) the blob policy.
func deletePromptText(set []fs.Entry) string {
	if len(set) == 0 {
		return ""
	}
	// Decide the tie wording from the whole set, not just set[0]: mixed
	// selections are possible through the drag menus.
	tie := false
	for _, e := range set {
		if fs.IsTie(e.Path) {
			tie = true
			break
		}
	}
	if len(set) == 1 {
		e := set[0]
		if !tie {
			return fmt.Sprintf("Delete %q?", e.Name)
		}
		what := "file"
		if e.IsDir {
			what = "directory and everything below it"
		}
		return fmt.Sprintf("Remove the %s %q from tie?\n\n%s", what, e.Name, tieDeleteNote)
	}

	action := fmt.Sprintf("Delete %d items?", len(set))
	if tie {
		action = fmt.Sprintf("Remove %d items from tie?", len(set))
	}
	msg := action + "\n\n" + quotedEntryNames(set)
	for _, e := range set {
		if e.IsDir {
			msg += "\n\nDirectories are removed with everything below them."
			break
		}
	}
	if tie {
		msg += "\n\n" + tieDeleteNote
	}
	return msg
}

// quotedEntryNames lists up to a handful of the entries' names (directories
// with a trailing slash), eliding the rest.
func quotedEntryNames(set []fs.Entry) string {
	const max = 8
	parts := make([]string, 0, len(set))
	for i, e := range set {
		if i == max {
			parts = append(parts, fmt.Sprintf("and %d more", len(set)-max))
			break
		}
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		parts = append(parts, fmt.Sprintf("%q", name))
	}
	return strings.Join(parts, ", ")
}

func (fm *FileManager) confirmDelete(set []fs.Entry) {
	if len(set) == 0 {
		return
	}
	dialog.ShowConfirm("Delete", deletePromptText(set), func(ok bool) {
		if !ok {
			return
		}
		fm.deleteEntries(set)
	}, fm.win)
}

// deleteEntries removes every entry of set, continuing past per-entry failures,
// then reloads the pane once and reports what failed. It runs off the UI
// goroutine: a tie delete is one or more network round-trips (a directory walks
// its whole subtree), and a multi-item delete would otherwise freeze the app.
func (fm *FileManager) deleteEntries(set []fs.Entry) {
	set = append([]fs.Entry(nil), set...) // the caller's selection may change
	fm.deleteWG.Add(1)
	go func() {
		defer fm.deleteWG.Done()
		var failed []string
		for _, e := range set {
			if err := fm.deleteEntry(e); err != nil {
				failed = append(failed, fmt.Sprintf("%q: %v", e.Name, err))
			}
		}
		fyne.Do(func() {
			if len(failed) > 0 {
				dialog.ShowError(fmt.Errorf("could not delete %d of %d items:\n\n%s",
					len(failed), len(set), strings.Join(failed, "\n")), fm.win)
			}
			// reload last (see setPath): it starts a fetch whose apply touches
			// widgets, and the error dialog above must not overlap it.
			fm.reload()
		})
	}()
}

// deleteEntry deletes through the backend's Remover when it has one, falling
// back to os.RemoveAll for local entries.
func (fm *FileManager) deleteEntry(e fs.Entry) error {
	if remover, ok := fm.registry.For(e.Path).(fs.Remover); ok {
		return remover.Remove(e)
	}
	if fs.IsLocal(e.Path) {
		return fs.RemoveLocal(e)
	}
	return fmt.Errorf("deleting %s entries is not supported", fs.SchemeOf(e.Path))
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

// toolbarButtonItem adapts a labelled button for widget.Toolbar, whose
// standard actions are icon-only.
type toolbarButtonItem struct {
	button *widget.Button
}

func (t toolbarButtonItem) ToolbarObject() fyne.CanvasObject { return t.button }

// updateTerminalButton enables the toolbar's terminal action only for local
// paths; tie:/mtp: locations have no filesystem directory to open.
func (fm *FileManager) updateTerminalButton() {
	if fm.terminalButton == nil {
		return
	}
	if fs.IsLocal(fm.pathValue()) {
		fm.terminalButton.Enable()
	} else {
		fm.terminalButton.Disable()
	}
}

// openTerminalHere opens the configured terminal emulator at the pane's
// current local directory.
func (fm *FileManager) openTerminalHere() {
	fm.markActive()
	p := fm.pathValue()
	if !fs.IsLocal(p) {
		dialog.ShowInformation("Terminal",
			"A terminal can only be opened at a local directory.", fm.win)
		return
	}
	if err := launchTerminal(fm.cfg, fs.LocalPath(p)); err != nil {
		dialog.ShowError(err, fm.win)
	}
}
