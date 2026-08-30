package ui

import (
	"fmt"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/tagselection"
)

// TagPanel is the dedicated tie tag panel. It shows one of two mutually
// exclusive sections: a filter (include/exclude, drives the table's tag query)
// when nothing is selected, or an editor (view/apply tags of the selected
// file(s)) when one or more rows are selected. It is hidden unless browsing a
// tie location.
type TagPanel struct {
	fm     *FileManager
	filter *tagselection.TagSelection
	editor *tagselection.TagSelection

	filterSection *fyne.Container
	editorSection *fyne.Container
	editorLabel   *widget.Label

	Container *fyne.Container

	favorites []string // cached favorite-tag set (source of truth is the tie store)

	// State of the tags currently loaded into the editor, for delta-applying
	// edits across a multi-file selection.
	loadedEntries []fs.Entry
	loadedTags    map[string][]string // hash -> full tag list
	loadedCommon  []string            // tags common to all loadedEntries
}

func NewTagPanel(fm *FileManager) *TagPanel {
	tp := &TagPanel{fm: fm, loadedTags: map[string][]string{}}

	tp.filter = tagselection.NewTagSelection(fm.win)
	tp.filter.ShowIncludeExclude = true
	tp.filter.ShowStars = true
	tp.filter.SetListLabel("Favorite tags")
	tp.filter.SetFavoriteMaxRows(8)
	tp.filter.SetSelectedMaxRows(8)
	tp.filter.OnSelectedChanged = tp.onFilterChanged
	tp.filter.OnStar = tp.onToggleFavorite

	tp.editor = tagselection.NewTagSelection(fm.win)
	tp.editor.KeepSearchFocus = true
	tp.editor.SetListLabel("Favorites")
	tp.editor.SetFavoriteMaxRows(8)
	tp.editor.SetSelectedMaxRows(8)
	tp.editor.OnSelectedChanged = tp.onEditorChanged
	tp.editor.OnNewTag = func(tag string) { tp.editor.AddSelected(tagselection.NewTagItemData(tag)) }

	tp.editorLabel = widget.NewLabelWithStyle("Tags of selection", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	closeBtn := widget.NewButtonWithIcon("Close", theme.CancelIcon(), func() { tp.fm.clearSelection() })
	editorHeader := container.NewBorder(nil, nil, tp.editorLabel, closeBtn)

	tp.filterSection = container.NewVBox(
		widget.NewLabelWithStyle("Filter by tags", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		tp.filter,
	)
	tp.editorSection = container.NewVBox(editorHeader, tp.editor)
	tp.editorSection.Hide()

	tp.Container = container.NewVBox(tp.filterSection, tp.editorSection)
	tp.Container.Hide()
	return tp
}

func (tp *TagPanel) tagStore() (fs.TagStore, bool) {
	ts, ok := tp.fm.registry.For(tp.fm.pathValue()).(fs.TagStore)
	return ts, ok
}

func (tp *TagPanel) showFilter() {
	tp.editorSection.Hide()
	tp.filterSection.Show()
}

func (tp *TagPanel) showEditor(n int) {
	label := "Tags of selection"
	if n > 1 {
		label = fmt.Sprintf("Tags of selection (%d)", n)
	}
	tp.editorLabel.SetText(label)
	tp.filterSection.Hide()
	tp.editorSection.Show()
}

func (tp *TagPanel) clearEditorState() {
	tp.loadedEntries = nil
	tp.loadedTags = map[string][]string{}
	tp.loadedCommon = nil
	tp.editor.ClearSelected()
}

// OnLocationChanged shows/hides the panel and reloads the tag universe.
func (tp *TagPanel) OnLocationChanged() {
	if !tp.fm.isTie() {
		tp.Container.Hide()
		return
	}
	tp.Container.Show()
	tp.reloadTags()
	tp.clearEditorState()
	tp.showFilter()
}

// reloadTags feeds every tag into the search tries and pins the persisted
// favorites into both quick-pick lists.
func (tp *TagPanel) reloadTags() {
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	tags, err := ts.ListAllTags()
	if err != nil {
		return
	}
	tp.filter.ClearAllTags()
	tp.editor.ClearAllTags()
	for _, t := range tags {
		tp.filter.AddTag(t)
		tp.editor.AddTag(t)
	}
	if fav, err := ts.ListFavoriteTags(); err == nil {
		tp.favorites = fav
	}
	tp.filter.SetFavoritesWithStars(tp.favorites)
	tp.editor.SetFavorites(tp.favorites)
}

// onToggleFavorite pins/unpins a tag in the tie store (shared across clients).
func (tp *TagPanel) onToggleFavorite(tag string, starred bool) {
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	var err error
	if starred {
		err = ts.AddFavoriteTag(tag)
	} else {
		err = ts.RemoveFavoriteTag(tag)
	}
	if err != nil {
		dialog.ShowError(err, tp.fm.win)
		return
	}
	if fav, lerr := ts.ListFavoriteTags(); lerr == nil {
		tp.favorites = fav
	}
	tp.filter.SetFavoritesWithStars(tp.favorites)
	tp.editor.SetFavorites(tp.favorites)
}

func (tp *TagPanel) onFilterChanged() {
	include, exclude := tp.filter.SelectedTags()
	tp.fm.queryInclude = include
	tp.fm.queryExclude = exclude
	tp.fm.reload()

	// With no active query show the pinned favorites; once a tag is selected,
	// replace the quick-pick list with the co-occurring ("related") tags.
	if len(include) == 0 && len(exclude) == 0 {
		tp.filter.SetListLabel("Favorite tags")
		tp.filter.SetFavoritesWithStars(tp.favorites)
		return
	}
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	related, err := ts.CoTags(include, exclude)
	if err != nil {
		return
	}
	tp.filter.SetListLabel("Related tags")
	tp.filter.SetFavorites(related)
}

// OnSelectionChanged loads the selected file(s)' tags into the editor and swaps
// the panel to the editor section, or reverts to the filter when nothing is
// selected.
func (tp *TagPanel) OnSelectionChanged() {
	if !tp.fm.isTie() || len(tp.fm.selectedEntries) == 0 {
		tp.clearEditorState()
		tp.showFilter()
		return
	}
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	var entries []fs.Entry
	loaded := map[string][]string{}
	for _, e := range tp.fm.selectedEntries {
		if e.Hash == "" {
			continue // directories and untracked rows can't be tagged
		}
		tags, err := ts.GetTags(e)
		if err != nil {
			continue
		}
		entries = append(entries, e)
		loaded[e.Hash] = tags
	}
	if len(entries) == 0 {
		tp.clearEditorState()
		tp.showFilter()
		return
	}
	tp.loadedEntries = entries
	tp.loadedTags = loaded
	tp.loadedCommon = commonTags(entries, loaded)
	tp.editor.SetSelected(tp.loadedCommon) // does not fire OnSelectedChanged
	tp.showEditor(len(entries))
}

// onEditorChanged applies the editor's change as a delta (added/removed tags)
// to every selected file, preserving each file's non-common tags.
func (tp *TagPanel) onEditorChanged() {
	if !tp.fm.isTie() || len(tp.loadedEntries) == 0 {
		return
	}
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	newCommon, _ := tp.editor.SelectedTags()
	added := diff(newCommon, tp.loadedCommon)
	removed := diff(tp.loadedCommon, newCommon)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	for _, e := range tp.loadedEntries {
		tags := applyDelta(tp.loadedTags[e.Hash], added, removed)
		if err := ts.SetTags(e, tags); err != nil {
			dialog.ShowError(err, tp.fm.win)
			return
		}
		tp.loadedTags[e.Hash] = tags
		tp.fm.updateEntryTags(e.Hash, tags)
	}
	tp.loadedCommon = newCommon
}

// commonTags returns the tags present on every entry, sorted.
func commonTags(entries []fs.Entry, tagsByHash map[string][]string) []string {
	if len(entries) == 0 {
		return nil
	}
	counts := map[string]int{}
	for _, e := range entries {
		seen := map[string]bool{}
		for _, t := range tagsByHash[e.Hash] {
			if !seen[t] {
				seen[t] = true
				counts[t]++
			}
		}
	}
	var common []string
	for t, c := range counts {
		if c == len(entries) {
			common = append(common, t)
		}
	}
	sort.Strings(common)
	return common
}

// diff returns the elements of a that are not in b.
func diff(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, x := range b {
		set[x] = true
	}
	var out []string
	for _, x := range a {
		if !set[x] {
			out = append(out, x)
		}
	}
	return out
}

// applyDelta returns existing with removed tags dropped and added tags appended,
// de-duplicated and order-stable.
func applyDelta(existing, added, removed []string) []string {
	rm := make(map[string]bool, len(removed))
	for _, x := range removed {
		rm[x] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range existing {
		if rm[x] || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	for _, x := range added {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
