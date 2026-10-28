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

	// editorGen bumps whenever the editor's loaded selection is replaced
	// (selection or location change). In-flight load/write goroutines check
	// it before applying, so a late reply for a stale selection can never
	// write tags onto entries of a previous listing.
	editorGen int
	// writeGen bumps per editor write; only the newest write's outcome is
	// applied to the loaded state.
	writeGen int
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
	// MusicBrainz tag lookup lives here, next to the tags it writes: it adds
	// only genres/community tags (no metadata, MBIDs or cover art). The full
	// metadata match remains the row context menu's "MusicBrainz…" action.
	mbTags := widget.NewButtonWithIcon("Add MusicBrainz tags…", theme.SearchIcon(), tp.onMusicBrainzTags)
	mbTags.Alignment = widget.ButtonAlignLeading
	mbTags.Importance = widget.LowImportance

	tp.filterSection = container.NewVBox(
		widget.NewLabelWithStyle("Filter by tags", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		tp.filter,
	)
	tp.editorSection = container.NewVBox(editorHeader, mbTags, tp.editor)
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
	tp.editorGen++
	tp.loadedEntries = nil
	tp.loadedTags = map[string][]string{}
	tp.loadedCommon = nil
	tp.editor.ClearSelected()
}

// OnLocationChanged shows/hides the panel and reloads the tag universe.
func (tp *TagPanel) OnLocationChanged() {
	tp.editorGen++
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
// favorites into both quick-pick lists. The store round trips run off the UI
// goroutine; a stale reply (location changed meanwhile) is dropped.
func (tp *TagPanel) reloadTags() {
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	go func() {
		tags, err := ts.ListAllTags()
		var fav []string
		var favErr error
		if err == nil {
			fav, favErr = ts.ListFavoriteTags()
		}
		fyne.Do(func() {
			// Drop replies for a superseded store (config/collection switch
			// or navigation to another location): the tag universe of one tie
			// store must never bleed into another.
			if cur, ok := tp.tagStore(); !ok || cur != ts {
				return
			}
			if err != nil {
				return
			}
			tp.filter.ClearAllTags()
			tp.editor.ClearAllTags()
			for _, t := range tags {
				tp.filter.AddTag(t)
				tp.editor.AddTag(t)
			}
			if favErr == nil {
				tp.favorites = fav
			} else {
				tp.favorites = nil // don't carry another store's favorites over
			}
			tp.filter.SetFavoritesWithStars(tp.favorites)
			tp.editor.SetFavorites(tp.favorites)
		})
	}()
}

// onToggleFavorite pins/unpins a tag in the tie store (shared across clients).
// The write plus the favorites re-read run off the UI goroutine.
func (tp *TagPanel) onToggleFavorite(tag string, starred bool) {
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	go func() {
		var err error
		if starred {
			err = ts.AddFavoriteTag(tag)
		} else {
			err = ts.RemoveFavoriteTag(tag)
		}
		var fav []string
		var lerr error
		if err == nil {
			fav, lerr = ts.ListFavoriteTags()
		}
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(err, tp.fm.win)
				return
			}
			if cur, ok := tp.tagStore(); !ok || cur != ts {
				return // store changed while writing
			}
			if lerr == nil {
				tp.favorites = fav
			}
			tp.filter.SetFavoritesWithStars(tp.favorites)
			tp.editor.SetFavorites(tp.favorites)
		})
	}()
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
	go func() {
		related, err := ts.CoTags(include, exclude)
		fyne.Do(func() {
			if err != nil {
				return
			}
			if cur, ok := tp.tagStore(); !ok || cur != ts {
				return // query or location changed while fetching
			}
			tp.filter.SetListLabel("Related tags")
			tp.filter.SetFavorites(related)
		})
	}()
}

// OnSelectionChanged loads the selected file(s)' tags into the editor and swaps
// the panel to the editor section, or reverts to the filter when nothing is
// selected. The tag fetch runs off the UI goroutine (one bulk round trip when
// the backend supports it); a reply for a superseded selection is dropped.
func (tp *TagPanel) OnSelectionChanged() {
	tp.editorGen++
	if !tp.fm.isTie() || len(tp.fm.selectedEntries) == 0 {
		tp.clearEditorStateKeepGen()
		tp.showFilter()
		return
	}
	ts, ok := tp.tagStore()
	if !ok {
		return
	}
	var entries []fs.Entry
	for _, e := range tp.fm.selectedEntries {
		if e.Hash == "" {
			continue // directories and untracked rows can't be tagged
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		tp.clearEditorStateKeepGen()
		tp.showFilter()
		return
	}
	gen := tp.editorGen
	go func() {
		loaded := map[string][]string{}
		if bulk, ok := ts.(fs.BulkTagger); ok {
			keys := make([]string, 0, len(entries))
			for _, e := range entries {
				keys = append(keys, e.Hash)
			}
			tags, err := bulk.TagsFor(keys)
			if err != nil {
				return
			}
			for _, e := range entries {
				loaded[e.Hash] = tags[e.Hash]
			}
		} else {
			for _, e := range entries {
				tags, err := ts.GetTags(e)
				if err != nil {
					continue
				}
				loaded[e.Hash] = tags
			}
		}
		fyne.Do(func() {
			if gen != tp.editorGen {
				return // selection changed while loading
			}
			if len(loaded) == 0 {
				tp.clearEditorStateKeepGen()
				tp.showFilter()
				return
			}
			tp.loadedEntries = entries
			tp.loadedTags = loaded
			tp.loadedCommon = commonTags(entries, loaded)
			tp.editor.SetSelected(tp.loadedCommon) // does not fire OnSelectedChanged
			tp.showEditor(len(entries))
		})
	}()
}

// clearEditorStateKeepGen clears the editor without bumping the generation a
// concurrent OnSelectionChanged already advanced.
func (tp *TagPanel) clearEditorStateKeepGen() {
	tp.loadedEntries = nil
	tp.loadedTags = map[string][]string{}
	tp.loadedCommon = nil
	tp.editor.ClearSelected()
}

// onEditorChanged applies the editor's change as a delta (added/removed tags)
// to every selected file, preserving each file's non-common tags. The writes
// run off the UI goroutine; only the newest write's outcome updates the
// loaded state, so a superseded edit can't corrupt it.
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
	gen := tp.editorGen
	entries := append([]fs.Entry(nil), tp.loadedEntries...)
	before := tp.loadedTags
	write := tp.writeGen
	tp.writeGen++
	go func() {
		after := map[string][]string{}
		var failed error
		for _, e := range entries {
			tags := applyDelta(before[e.Hash], added, removed)
			if err := ts.SetTags(e, tags); err != nil {
				failed = err
				break
			}
			after[e.Hash] = tags
		}
		fyne.Do(func() {
			if gen != tp.editorGen || write != tp.writeGen-1 {
				return // selection or a newer write moved on
			}
			if failed != nil {
				dialog.ShowError(failed, tp.fm.win)
				return
			}
			tp.loadedTags = after
			for hash, tags := range after {
				tp.fm.updateEntryTags(hash, tags)
			}
			tp.loadedCommon = newCommon
		})
	}()
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
