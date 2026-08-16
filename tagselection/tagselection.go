// TestIncludeExclude project main.go
package tagselection

import (
	"image/color"
	"slices"
	"sort"
	"strings"

	"fyne.io/fyne/v2/theme"

	"fyne.io/fyne/v2/canvas"

	"git.sr.ht/~uid/imgview/tagselection/trie"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type SearchItem struct {
	widget.BaseWidget
	results     []string
	resultList  *fyne.Container
	searchList  *AutoExpandingList
	entry       *queryEntry
	query       string
	content     fyne.CanvasObject
	showResults bool
	focusIndex  int // -1 = nothing highlighted
}

type queryEntry struct {
	widget.Entry
	onDown      func()
	onUp        func()
	onEnter     func()
	onEscape    func()
	onSpace     func() bool // returns true if the space was consumed (selected a row)
	onFocusLost func()
}

func newQueryEntry() *queryEntry {
	entry := &queryEntry{}
	entry.PlaceHolder = "Search tags"
	entry.ExtendBaseWidget(entry)
	return entry
}

func (e *queryEntry) Clear() {
	e.SetText("")
	e.PlaceHolder = "Search tags"
}

// TypedKey intercepts navigation keys so they drive the result list instead of
// the entry text/cursor. Returning early prevents the key (and any Space rune on
// selection) from leaking into the search field.
func (e *queryEntry) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyEscape:
		if e.onEscape != nil {
			e.onEscape()
		}
	case fyne.KeyDown:
		if e.onDown != nil {
			e.onDown()
		}
	case fyne.KeyUp:
		if e.onUp != nil {
			e.onUp()
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		if e.onEnter != nil {
			e.onEnter()
		}
	default:
		e.Entry.TypedKey(key)
	}
}

// TypedRune intercepts the space rune to select the highlighted row instead of
// typing it. Any other rune types normally.
func (e *queryEntry) TypedRune(r rune) {
	if r == ' ' && e.onSpace != nil && e.onSpace() {
		return
	}
	e.Entry.TypedRune(r)
}

func (e *queryEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusLost != nil {
		e.onFocusLost()
	}
}

func NewSearchItem(ts *TagSelection) *SearchItem {
	si := &SearchItem{focusIndex: -1}
	si.searchList = NewAutoExpandingList(
		func() int {
			return len(si.results)
		},
		func() fyne.CanvasObject {
			// Mirror ShowStars so search results also carry a ☆/★ button
			// when the caller (image tagger) uses the star feature.
			return NewTagItem(false, ts.ShowStars, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			ti := o.(*TagItem)
			tag := si.results[i]
			ti.SetData(&TagItemData{text: tag, include: true, starred: ts.starredSet[tag]})
			ti.SetHighlighted(i == si.focusIndex)
		})
	si.searchList.maxRows = 8

	background := canvas.NewRectangle(theme.BackgroundColor())
	background.CornerRadius = 5
	background.StrokeColor = theme.InputBorderColor()
	background.StrokeWidth = theme.Padding()
	si.resultList = container.NewStack(background, container.NewPadded(si.searchList))
	si.resultList.Hide()

	var txtSearch *queryEntry
	clearing := false

	search := func(query string) {
		query = strings.ToLower(query)
		keys := ts.tags.KeysWithPrefix(query)
		si.results = si.results[:0]
		for _, k := range keys {
			if orig, ok := ts.caseMap[k]; ok {
				si.results = append(si.results, orig)
			} else {
				si.results = append(si.results, k)
			}
		}
		si.focusIndex = -1
		si.searchList.Refresh()
		if len(si.results) == 0 {
			si.resultList.Hide()
		} else {
			si.resultList.Show()
		}
		si.Refresh()
		ts.relayout()
	}

	hide := func() {
		si.focusIndex = -1
		if si.resultList.Hidden {
			return
		}
		si.resultList.Hide()
		defer ts.relayout()
		si.Refresh()
	}

	selectAt := func(i int) {
		if i < 0 || i >= len(si.results) {
			return
		}
		ts.AddSelected(NewTagItemData(si.results[i]))
		clearing = true // stops OnChanged from reopening the dropdown
		si.entry.Clear()
		hide()
		ts.window.Canvas().Focus(si.entry)
		ts.Refresh()
	}

	txtSearch = newQueryEntry()
	txtSearch.onDown = func() {
		if si.resultList.Hidden || len(si.results) == 0 {
			return
		}
		if si.focusIndex < len(si.results)-1 {
			si.focusIndex++
		}
		si.searchList.ScrollTo(si.focusIndex)
		si.searchList.Refresh()
	}
	txtSearch.onUp = func() {
		if si.focusIndex > 0 {
			si.focusIndex--
			si.searchList.ScrollTo(si.focusIndex)
			si.searchList.Refresh()
		}
	}
	txtSearch.onEnter = func() {
		if si.focusIndex >= 0 {
			selectAt(si.focusIndex)
			return
		}
		// No result row highlighted: if the caller supports free-form tag
		// creation and the entry is non-empty, forward the raw text.
		q := strings.TrimSpace(txtSearch.Text)
		if q != "" && ts.OnNewTag != nil {
			ts.OnNewTag(q)
			clearing = true
			si.entry.Clear()
			hide()
			ts.window.Canvas().Focus(si.entry)
			ts.Refresh()
		}
	}
	txtSearch.onSpace = func() bool {
		if si.resultList.Hidden || si.focusIndex < 0 {
			return false // no row highlighted -> type a normal space
		}
		selectAt(si.focusIndex)
		return true
	}
	txtSearch.onEscape = func() {
		hide()
		ts.window.Canvas().Focus(txtSearch)
	}
	// onFocusLost is intentionally NOT wired to hide().
	//
	// When the user clicks a result row, Fyne fires MouseDown first, which
	// transfers focus away from txtSearch (widget.List is Focusable), causing
	// FocusLost. If hide() ran here it would call si.resultList.Hide() before
	// MouseUp arrives. Hidden widgets don't receive events, so the tap on the
	// list item — and therefore OnSelected — would never fire.
	//
	// The dropdown closes correctly via: Escape key, tag selection (selectAt
	// calls hide()), and the search text being cleared (no results → Hide).
	si.entry = txtSearch

	si.searchList.OnSelected = func(i int) {
		si.searchList.Unselect(i)
		selectAt(i)
	}

	txtSearch.OnChanged = func(s string) {
		if clearing { // Do not show result list afer selecting a tag
			clearing = false
		} else {
			search(s)
		}
	}
	si.content = container.NewBorder(txtSearch, nil, nil, nil, si.resultList)
	si.ExtendBaseWidget(si)

	return si
}

func (si *SearchItem) MinSize() fyne.Size {
	if si.resultList.Hidden {
		return fyne.NewSize(100, 35)
	}
	rows := len(si.results)
	if rows > si.searchList.maxRows {
		rows = si.searchList.maxRows
	}
	return fyne.NewSize(100, 35+float32(rows)*35+theme.Padding()*2)
}

func (si *SearchItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(si.content)
}

// starTap is a lightweight tappable ☆/★ label that does NOT implement
// Focusable. Using widget.Button for the star caused it to steal keyboard
// focus from the search entry on click, which fired onFocusLost → hide()
// and collapsed the search dropdown before OnSelected could fire.
type starTap struct {
	widget.BaseWidget
	label *canvas.Text
	onTap func()
}

func newStarTap(onTap func()) *starTap {
	st := &starTap{onTap: onTap}
	st.label = canvas.NewText("☆", theme.ForegroundColor())
	st.label.TextSize = theme.TextSize()
	st.ExtendBaseWidget(st)
	return st
}

func (st *starTap) SetText(t string) {
	st.label.Text = t
	st.label.Refresh()
}

func (st *starTap) Tapped(_ *fyne.PointEvent) {
	if st.onTap != nil {
		st.onTap()
	}
}

func (st *starTap) MinSize() fyne.Size {
	return fyne.NewSize(theme.TextSize()*2, theme.TextSize()*2)
}

func (st *starTap) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(st.label)
}

type TagItem struct {
	widget.BaseWidget
	label        *widget.Label
	background   *canvas.Rectangle
	includeCheck *widget.Check
	starBtn      *starTap
	content      fyne.CanvasObject
	okColor      color.Color
	data         *TagItemData
	ts           *TagSelection
	settingData  bool // true while SetData runs; suppresses OnSelectedChanged
}

type TagItemData struct {
	text    string
	include bool
	starred bool
}

func NewTagItemData(text string) *TagItemData {
	return &TagItemData{text: text, include: true}
}

func NewTagItem(showInclude bool, showStar bool, ts *TagSelection) *TagItem {
	ti := &TagItem{ts: ts}
	ti.label = widget.NewLabel("")
	ti.okColor = color.RGBA{85, 170, 127, 255}
	ti.background = canvas.NewRectangle(ti.okColor)
	ti.background.CornerRadius = 5

	var right fyne.CanvasObject
	if showInclude {
		ti.includeCheck = widget.NewCheck("", nil)
		ti.includeCheck.SetChecked(true)
		ti.includeCheck.OnChanged = func(b bool) {
			ti.data.include = b
			if b {
				ti.background.FillColor = ti.okColor
			} else {
				ti.background.FillColor = theme.ErrorColor()
			}
			ti.background.Refresh()
			if !ti.settingData && ts.OnSelectedChanged != nil {
				ts.OnSelectedChanged()
			}
		}
		right = ti.includeCheck
	} else if showStar {
		ti.starBtn = newStarTap(func() {
			if ti.data == nil || ti.ts.OnStar == nil {
				return
			}
			ti.data.starred = !ti.data.starred
			if ti.data.starred {
				ti.starBtn.SetText("★")
			} else {
				ti.starBtn.SetText("☆")
			}
			ti.ts.OnStar(ti.data.text, ti.data.starred)
		})
		right = ti.starBtn
	}

	if right != nil {
		ti.content = container.NewBorder(nil, nil, container.NewStack(ti.background, ti.label), right)
	} else {
		ti.content = container.NewBorder(nil, nil, container.NewStack(ti.background, ti.label), nil)
	}
	ti.ExtendBaseWidget(ti)
	return ti
}

func (ti *TagItem) Text() string {
	return ti.label.Text
}

func (ti *TagItem) SetData(tid *TagItemData) {
	ti.settingData = true
	defer func() { ti.settingData = false }()
	ti.data = tid
	ti.label.SetText(tid.text)
	if ti.includeCheck != nil {
		// Restore check and background to match data; the settingData flag
		// prevents this from firing OnSelectedChanged spuriously.
		ti.includeCheck.SetChecked(tid.include)
	}
	if ti.starBtn != nil {
		if tid.starred {
			ti.starBtn.SetText("★")
		} else {
			ti.starBtn.SetText("☆")
		}
	}
}

func (ti *TagItem) SetText(text string) {
	ti.label.SetText(text)
}

// SetHighlighted marks the item as the keyboard-focused row.
func (ti *TagItem) SetHighlighted(on bool) {
	if on {
		ti.background.FillColor = theme.SelectionColor()
	} else {
		ti.background.FillColor = ti.okColor
	}
	ti.background.Refresh()
}

func (ti *TagItem) MinSize() fyne.Size {
	return fyne.NewSize(100, 30)
}

func (ti *TagItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(ti.content)
}

type TagSelection struct {
	widget.BaseWidget
	selected          []*TagItemData
	quickPick         []*TagItemData
	OnSelectedChanged func()
	// OnNewTag, when non-nil, is called when the user presses Enter in the
	// search box with a non-empty query but no result row highlighted. This
	// allows callers to create a brand-new tag that is not yet in the trie.
	// The sidebar leaves this nil; the image tagger sets it.
	OnNewTag func(tag string)
	// OnStar, when non-nil, is called when the user clicks the ☆/★ button on
	// a quick-pick tag item. starred is the new state after the toggle.
	// The sidebar leaves this nil; the image tagger sets it.
	OnStar func(tag string, starred bool)
	// ShowStars controls whether the quick-pick (favorite) list items display
	// a ☆/★ toggle button. Must be set before the widget is first rendered.
	// The sidebar leaves this false; the image tagger sets it to true.
	ShowStars bool
	// ShowIncludeExclude controls whether the selected-tag list items display
	// an include/exclude checkbox. Must be set before the widget is first
	// rendered. The sidebar sets this to true (for tag filtering); the image
	// tagger leaves it false (applied tags have no include/exclude distinction).
	ShowIncludeExclude bool
	starredSet         map[string]bool // tags whose star button shows ★
	tags               *trie.Trie
	selectedList       *AutoExpandingList
	quickPickList      *AutoExpandingList
	listLabel          *widget.Label
	content            fyne.CanvasObject
	search             *SearchItem
	window             fyne.Window
	caseMap            map[string]string // lowercase tag -> original case
}

type AutoExpandingList struct {
	widget.List
	minWidth float32
	maxRows  int // 0 = uncapped
}

func NewAutoExpandingList(length func() int, createItem func() fyne.CanvasObject, updateItem func(widget.ListItemID, fyne.CanvasObject)) *AutoExpandingList {
	a := &AutoExpandingList{}
	a.minWidth = 200
	a.Length = length
	a.CreateItem = createItem
	a.UpdateItem = updateItem
	a.ExtendBaseWidget(a)

	return a
}

func (a *AutoExpandingList) MinSize() fyne.Size {
	rows := a.Length()
	if a.maxRows > 0 && rows > a.maxRows {
		rows = a.maxRows
	}
	return fyne.NewSize(a.minWidth, float32(rows)*35)
}

func NewTagSelection(window fyne.Window) *TagSelection {
	ts := &TagSelection{
		window:  window,
		tags:    trie.NewTrie(),
		caseMap: make(map[string]string),
	}

	ts.quickPickList = NewAutoExpandingList(
		func() int {
			return len(ts.quickPick)
		},
		func() fyne.CanvasObject {
			// ShowStars is read at CreateItem time (lazy, when cells are first
			// needed), so setting it before the widget is displayed is enough.
			return NewTagItem(false, ts.ShowStars, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			data := ts.quickPick[i]
			data.starred = ts.starredSet[data.text]
			o.(*TagItem).SetData(data)
		})

	ts.selectedList = NewAutoExpandingList(
		func() int {
			return len(ts.selected)
		},
		func() fyne.CanvasObject {
			// ShowIncludeExclude is read at CreateItem time (lazy), so setting
			// it before the widget is displayed is enough. The sidebar uses
			// true (filter with include/exclude); the tagger uses false
			// (applied tags have no distinction).
			return NewTagItem(ts.ShowIncludeExclude, false, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*TagItem).SetData(ts.selected[i])
		})

	clear := func(i int) {
		ts.selectedList.Unselect(i)
		ts.quickPickList.Unselect(i)
		ts.Refresh()
	}

	ts.quickPickList.OnSelected = func(i int) {
		ts.AddSelected(ts.quickPick[i])
		clear(i)
	}

	ts.selectedList.OnSelected = func(i int) {
		ts.selected = slices.Delete(ts.selected, i, i+1)
		if ts.OnSelectedChanged != nil {
			ts.OnSelectedChanged()
		}
		clear(i)
	}

	ts.listLabel = widget.NewLabel("Favorites")
	ts.listLabel.TextStyle.Bold = true
	ts.search = NewSearchItem(ts)
	ts.content = container.NewVBox(ts.selectedList, canvas.NewLine(theme.ForegroundColor()), ts.listLabel, ts.quickPickList)

	ts.ExtendBaseWidget(ts)

	return ts
}

func (ts *TagSelection) SelectedTags() (included []string, excluded []string) {
	for _, x := range ts.selected {
		if x.include {
			included = append(included, x.text)
		} else {
			excluded = append(excluded, x.text)
		}
	}

	return included, excluded
}

func (ts *TagSelection) MinSize() fyne.Size {
	searchHeight := ts.search.MinSize().Height
	return fyne.NewSize(200, searchHeight+theme.Padding()+ts.content.MinSize().Height)
}

func (ts *TagSelection) Refresh() {
	ts.content.Refresh()
}

// relayout asks the enclosing container to re-measure this widget, so the
// result dropdown can grow/shrink the row instead of being clipped.
func (ts *TagSelection) relayout() {
	ts.Resize(ts.MinSize())
	canvas.Refresh(ts)
}

// AddTag inserts a tag into the search trie (lowercased for case-insensitive
// search) and records its original case in caseMap for display.
//
// Note: If multiple tags differ only by case (e.g. "Test", "TEST"), the trie
// will match all of them on search, but caseMap retains only the last one's
// case. This is a known limitation; case variants are effectively merged.
func (ts *TagSelection) AddTag(tag string) {
	lower := strings.ToLower(tag)
	ts.tags.Put(lower)
	if ts.caseMap == nil {
		ts.caseMap = make(map[string]string)
	}
	ts.caseMap[lower] = tag
}

func (ts *TagSelection) ClearAllTags() {
	ts.tags = trie.NewTrie()
	ts.caseMap = make(map[string]string)
}

func (ts *TagSelection) AddFavorite(tag string) {
	ts.quickPick = append(ts.quickPick, NewTagItemData(tag))
}

// ClearFavorites empties the quick-pick tag list and refreshes it.
func (ts *TagSelection) ClearFavorites() {
	ts.quickPick = ts.quickPick[:0]
	ts.quickPickList.Refresh()
}

// SetFavorites replaces the quick-pick tag list with tags and refreshes once.
// Prefer this over ClearFavorites + AddFavorite loops: a single Refresh call
// avoids showing an empty list between the clear and the re-population.
func (ts *TagSelection) SetFavorites(tags []string) {
	ts.quickPick = ts.quickPick[:0]
	for _, tag := range tags {
		ts.quickPick = append(ts.quickPick, NewTagItemData(tag))
	}
	ts.quickPickList.Refresh()
}

// ClearSelected removes all currently selected tags and refreshes the list.
func (ts *TagSelection) ClearSelected() {
	ts.selected = ts.selected[:0]
	ts.selectedList.Refresh()
}

// SetListLabel changes the bold label above the quick-pick tag list
// ("Favorites" by default).
func (ts *TagSelection) SetListLabel(text string) {
	ts.listLabel.SetText(text)
}

// SetFavoriteMaxRows caps the visible row count of the quick-pick list.
// 0 = uncapped (default). Must be called before SetFavorites to take effect.
func (ts *TagSelection) SetFavoriteMaxRows(n int) {
	ts.quickPickList.maxRows = n
}

// SetSelectedMaxRows caps the visible row count of the selected-tag list.
// 0 = uncapped (default).
func (ts *TagSelection) SetSelectedMaxRows(n int) {
	ts.selectedList.maxRows = n
}

// SetStarred replaces the set of starred tags and refreshes both the
// quick-pick list and the search-result dropdown so ☆/★ buttons are
// consistent everywhere. Only meaningful when ShowStars is true.
func (ts *TagSelection) SetStarred(tags []string) {
	ts.starredSet = make(map[string]bool, len(tags))
	for _, t := range tags {
		ts.starredSet[t] = true
	}
	ts.quickPickList.Refresh()
	if ts.ShowStars && ts.search != nil {
		ts.search.searchList.Refresh()
	}
}

// SetFavoritesWithStars replaces both the quick-pick list and the starred set
// in a single call, refreshing only once. Prefer this over calling SetFavorites
// and SetStarred separately when the same tag list should be shown and starred.
func (ts *TagSelection) SetFavoritesWithStars(tags []string) {
	// Update favorite list
	ts.quickPick = ts.quickPick[:0]
	for _, tag := range tags {
		ts.quickPick = append(ts.quickPick, NewTagItemData(tag))
	}
	// Update starred set
	ts.starredSet = make(map[string]bool, len(tags))
	for _, t := range tags {
		ts.starredSet[t] = true
	}
	// Single refresh for both
	ts.quickPickList.Refresh()
	if ts.ShowStars && ts.search != nil {
		ts.search.searchList.Refresh()
	}
}

// ToggleStar updates the starred state for a tag and refreshes the UI.
// Returns the new state (true if now starred, false if unstarred).
func (ts *TagSelection) ToggleStar(tag string, starred bool) {
	if ts.starredSet == nil {
		ts.starredSet = make(map[string]bool)
	}
	ts.starredSet[tag] = starred
	// Refresh both the quick-pick list and search results
	ts.quickPickList.Refresh()
	if ts.ShowStars && ts.search != nil {
		ts.search.searchList.Refresh()
	}
}

// StarredTags returns a sorted list of all currently starred tags.
func (ts *TagSelection) StarredTags() []string {
	starred := make([]string, 0, len(ts.starredSet))
	for t, v := range ts.starredSet {
		if v {
			starred = append(starred, t)
		}
	}
	sort.Strings(starred)
	return starred
}

func (ts *TagSelection) AddSelected(tid *TagItemData) {
	for _, x := range ts.selected {
		if tid.text == x.text {
			return
		}
	}
	ts.selected = append(ts.selected, tid)
	if ts.OnSelectedChanged != nil {
		ts.OnSelectedChanged()
	}
}

// SetSelected replaces the selected-tag list and refreshes it once, WITHOUT
// firing OnSelectedChanged. Use it to reflect externally loaded state (e.g.
// tags fetched from tie into the image tagger): AddSelected would fire the
// callback per tag, and each partial state would look like a user edit —
// in the tagger that triggers spurious tie delete/add writes. It also
// refreshes the list immediately, which a bare AddSelected loop does not.
func (ts *TagSelection) SetSelected(tags []string) {
	ts.selected = ts.selected[:0]
	for _, tag := range tags {
		ts.selected = append(ts.selected, NewTagItemData(tag))
	}
	ts.selectedList.Refresh()
}

/*****************************
** RENDERER
*****************************/
type TagSelectionRenderer struct {
	ts      *TagSelection
	objects []fyne.CanvasObject
}

func (ts *TagSelection) CreateRenderer() fyne.WidgetRenderer {
	ren := &TagSelectionRenderer{
		ts:      ts,
		objects: []fyne.CanvasObject{ts.content, ts.search},
	}

	return ren
}

// Refresh causes this object to be redrawn in it's current state
func (_ *TagSelectionRenderer) Destroy() {
}

func (_ *TagSelectionRenderer) Refresh() {}

func (tsr *TagSelectionRenderer) Objects() []fyne.CanvasObject {
	return tsr.objects
}

func (tsr *TagSelectionRenderer) MinSize() fyne.Size {
	return tsr.ts.MinSize()
}

func (tsr *TagSelectionRenderer) Layout(s fyne.Size) {
	searchHeight := tsr.ts.search.MinSize().Height
	contentPosY := searchHeight + theme.Padding()
	tsr.ts.search.Resize(fyne.NewSize(s.Width, searchHeight))
	tsr.ts.content.Move(fyne.NewPos(0, contentPosY))
	tsr.ts.content.Resize(fyne.NewSize(s.Width, s.Height-contentPosY))
}
