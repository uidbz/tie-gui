// TestIncludeExclude project main.go
package tagselection

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2/theme"

	"fyne.io/fyne/v2/canvas"

	"git.sr.ht/~uid/imgview/imgviewer/tagselection/trie"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type SearchItem struct {
	widget.BaseWidget
	results     []string
	resultList  *fyne.Container
	searchList  *AutoExpandingList
	query       string
	content     fyne.CanvasObject
	showResults bool
}

type queryEntry struct {
	widget.Entry
	onKeyDown func()
	onEscape  func()
}

func newQueryEntry(onKeyDown func(), onEscape func()) *queryEntry {
	entry := &queryEntry{onKeyDown: onKeyDown, onEscape: onEscape}
	entry.PlaceHolder = "Search tags"
	entry.ExtendBaseWidget(entry)
	return entry
}

func (e *queryEntry) Clear() {
	e.SetText("")
	e.PlaceHolder = "Search tags"
}

func (e *queryEntry) KeyDown(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyEscape:
		e.onEscape()
	case fyne.KeyDown:
		e.onKeyDown()
	}
}

func NewSeachItem(ts *TagSelection) *SearchItem {
	si := &SearchItem{}
	si.searchList = NewAutoExpandingList(
		func() int {
			return len(si.results)
		},
		func() fyne.CanvasObject {
			return NewTagItem(false, ts)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*TagItem).SetText(si.results[i])
		})

	background := canvas.NewRectangle(theme.BackgroundColor())
	background.CornerRadius = 5
	background.StrokeColor = theme.InputBorderColor()
	background.StrokeWidth = theme.Padding()
	si.resultList = container.NewStack(background, container.NewPadded(si.searchList))
	si.resultList.Hide()

	search := func(query string) {
		si.results = ts.tags.KeysWithPrefix(query)
		si.searchList.Refresh()
		si.resultList.Show()
	}

	var txtSearch *queryEntry
	onKeyDown := func() {
		ts.window.Canvas().Focus(si.searchList)
		search(txtSearch.Text)
	}
	onEscape := func() {
		si.resultList.Hide()
		ts.window.Canvas().Focus(txtSearch)
	}
	txtSearch = newQueryEntry(onKeyDown, onEscape)

	clearing := false
	si.searchList.OnSelected = func(i int) {
		ts.AddSelected(NewTagItemData(si.results[i]))
		si.searchList.Unselect(i)
		si.resultList.Hide()
		clearing = true
		txtSearch.Clear()
		ts.Refresh()
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
		return fyne.NewSize(100, 30)
	} else {
		return fyne.NewSize(100, 200)
	}
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
	content           fyne.CanvasObject
	search            *SearchItem
	window            fyne.Window
}

type AutoExpandingList struct {
	widget.List
	minWidth float32
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
	return fyne.NewSize(a.minWidth, float32(a.Length())*35)
}

func NewTagSelection(window fyne.Window) *TagSelection {
	ts := &TagSelection{
		window: window,
		tags:   trie.NewTrie(),
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
		// l.RefreshLists()
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

	lblFavorites := widget.NewLabel("Favorites")
	lblFavorites.TextStyle.Bold = true
	ts.search = NewSeachItem(ts)
	ts.content = container.NewVBox(ts.selectedList, canvas.NewLine(theme.ForegroundColor()), lblFavorites, ts.favoriteList)

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
	return fyne.NewSize(200, 35+ts.content.MinSize().Height)
}

func (ts *TagSelection) Refresh() {
	ts.content.Refresh()
}

func (ts *TagSelection) AddTag(tag string) {
	ts.tags.Put(tag)
}

func (ts *TagSelection) ClearAllTags() {
	ts.tags = trie.NewTrie()
}

func (ts *TagSelection) AddFavorite(tag string) {
	ts.favorite = append(ts.favorite, NewTagItemData(tag))
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
	return fyne.NewSize(100, 100)
}

func (tsr *TagSelectionRenderer) Layout(s fyne.Size) {
	var searchHeight float32

	if tsr.ts.search.resultList.Hidden {
		searchHeight = float32(35)
	} else {
		searchHeight = float32(200)
	}
	contentPosY := float32(35) + theme.Padding()
	tsr.ts.search.Resize(fyne.NewSize(s.Width, searchHeight))
	tsr.ts.content.Move(fyne.NewPos(0, contentPosY))
	tsr.ts.content.Resize(fyne.NewSize(s.Width, s.Height-contentPosY))
}
