// TestIncludeExclude project main.go
package tagselection

import (
	"image/color"
	"slices"
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
			return NewTagItem(false, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			ti := o.(*TagItem)
			ti.SetText(si.results[i])
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
		selectAt(si.focusIndex)
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
	txtSearch.onFocusLost = func() {
		hide() // focus went elsewhere (click outside) -> close
	}
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

type TagItem struct {
	widget.BaseWidget
	label        *widget.Label
	background   *canvas.Rectangle
	includeCheck *widget.Check
	content      fyne.CanvasObject
	okColor      color.Color
	data         *TagItemData
}

type TagItemData struct {
	text    string
	include bool
}

func NewTagItemData(text string) *TagItemData {
	return &TagItemData{text, true}
}

func NewTagItem(showInclude bool, ts *TagSelection) *TagItem {
	ti := &TagItem{}
	ti.label = widget.NewLabel("")
	ti.okColor = color.RGBA{85, 170, 127, 255}
	ti.background = canvas.NewRectangle(ti.okColor)
	ti.background.CornerRadius = 5
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
			if ts.OnSelectedChanged != nil {
				ts.OnSelectedChanged()
			}
		}
		ti.content = container.NewBorder(nil, nil, container.NewStack(ti.background, ti.label), ti.includeCheck)
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
	ti.data = tid
	ti.label.SetText(tid.text)
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
	favorite          []*TagItemData
	OnSelectedChanged func()
	tags              *trie.Trie
	selectedList      *AutoExpandingList
	favoriteList      *AutoExpandingList
	listLabel         *widget.Label
	content           fyne.CanvasObject
	search            *SearchItem
	window            fyne.Window
	caseMap           map[string]string // lowercase tag -> original case
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

	ts.favoriteList = NewAutoExpandingList(
		func() int {
			return len(ts.favorite)
		},
		func() fyne.CanvasObject {
			return NewTagItem(false, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*TagItem).SetData(ts.favorite[i])
		})

	ts.selectedList = NewAutoExpandingList(
		func() int {
			return len(ts.selected)
		},
		func() fyne.CanvasObject {
			return NewTagItem(true, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*TagItem).SetData(ts.selected[i])
		})

	clear := func(i int) {
		ts.selectedList.Unselect(i)
		ts.favoriteList.Unselect(i)
		ts.Refresh()
	}

	ts.favoriteList.OnSelected = func(i int) {
		ts.AddSelected(ts.favorite[i])
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
	ts.content = container.NewVBox(ts.selectedList, canvas.NewLine(theme.ForegroundColor()), ts.listLabel, ts.favoriteList)

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
	ts.favorite = append(ts.favorite, NewTagItemData(tag))
}

// ClearFavorites empties the quick-pick tag list and refreshes it.
func (ts *TagSelection) ClearFavorites() {
	ts.favorite = ts.favorite[:0]
	ts.favoriteList.Refresh()
}

// SetFavorites replaces the quick-pick tag list with tags and refreshes once.
// Prefer this over ClearFavorites + AddFavorite loops: a single Refresh call
// avoids showing an empty list between the clear and the re-population.
func (ts *TagSelection) SetFavorites(tags []string) {
	ts.favorite = ts.favorite[:0]
	for _, tag := range tags {
		ts.favorite = append(ts.favorite, NewTagItemData(tag))
	}
	ts.favoriteList.Refresh()
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
