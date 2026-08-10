package main

import (
	"fmt"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"git.sr.ht/~uid/tie/client"

	"git.sr.ht/~uid/imgview/tagselection"
)

// imageTagger is a floating panel that overlays the single-image view and
// lets the user view, add, and remove tie tags for the displayed image.
//
// A single tap on the image opens or closes the panel. The panel contains
// the full TagSelection widget:
//   - The "selected" list shows tags currently applied to the image in tie.
//     Clicking a tag removes it.
//   - The search box and quick-pick list let the user add tags. Each tag has
//     a ☆/★ button: clicking it toggles the tag's status in the tie
//     ("tags","favorite") list, making it available for quick access.
//   - Typing a new tag name and pressing Enter creates the tag even if it
//     does not yet exist in tie.
//
// Tag writes are persisted immediately via tc.Add / tc.Delete in a goroutine.
type imageTagger struct {
	tc          *client.TieClient
	hash        string   // content hash of the currently viewed image ("" = none)
	appliedTags []string // snapshot of tags applied to the current image in tie

	// allTags is the full known tag list (from tc.Get("tags")), kept here so
	// OnNewTag can append and refresh the available list without a round-trip.
	allTags []string

	// favoriteTags tracks which tags are starred (in the tie "tags"/"favorite"
	// relation). Updated locally on every star toggle; persisted to tie.
	favoriteTags map[string]bool

	ts    *tagselection.TagSelection
	Panel fyne.CanvasObject // embed into viewer.Content as a stack overlay layer

	// OnHide, when non-nil, is called after the panel becomes hidden.
	// Use it to restore keyboard focus to the image view on desktop.
	OnHide func()
}

// newImageTagger creates an imageTagger. Call SetAllTags and SetFavoriteTags
// after the tag list has been fetched from tie to populate the panel.
func newImageTagger(window fyne.Window, tc *client.TieClient) *imageTagger {
	it := &imageTagger{
		tc:           tc,
		favoriteTags: make(map[string]bool),
	}
	it.ts = tagselection.NewTagSelection(window)
	it.ts.ShowStars = true // must be set before the widget is first rendered
	it.ts.SetListLabel("Favorites")
	// Cap the favorites list so the panel doesn't grow taller than the screen
	// when there are many tags. The search box reaches all tags regardless.
	it.ts.SetFavoriteMaxRows(8)
	// Cap the applied-tags list too.
	it.ts.SetSelectedMaxRows(4)

	// OnSelectedChanged fires when the selected list changes (add, remove, or
	// include/exclude checkbox toggle). We use the union of included and
	// excluded tags as the "applied" set: the checkbox has no meaningful
	// distinction in the tagging context.
	it.ts.OnSelectedChanged = func() {
		included, excluded := it.ts.SelectedTags()
		all := make([]string, 0, len(included)+len(excluded))
		all = append(all, included...)
		all = append(all, excluded...)
		it.syncTags(all)
	}

	// OnNewTag lets the user type a brand-new tag name that is not yet in the
	// trie and press Enter to apply it. The new tag is added to the trie so
	// it becomes immediately searchable (with ☆), then added to the selected
	// list, which triggers OnSelectedChanged → syncTags → tc.Add.
	it.ts.OnNewTag = func(tag string) {
		if !slices.Contains(it.allTags, tag) {
			it.allTags = append(it.allTags, tag)
			// Show the new tag in the list immediately (unstarred by default).
			it.ts.SetFavorites(it.allTags)
			it.ts.SetStarred(it.starredList())
		}
		it.ts.AddTag(tag)
		it.ts.AddSelected(tagselection.NewTagItemData(tag))
	}

	// OnStar fires when the user clicks the ☆/★ button on a tag in the
	// quick-pick list or search results. Toggle the tie ("tags","favorite")
	// relation and rebuild the favorites quick-pick list so only starred tags
	// appear there.
	it.ts.OnStar = func(tag string, isStarred bool) {
		it.favoriteTags[tag] = isStarred
		// Sync ☆/★ button state across the list and search dropdown.
		it.ts.SetStarred(it.starredList())
		go func() {
			if isStarred {
				if _, err := it.tc.Add("tags", "favorite", tag); err != nil {
					fmt.Println("imageTagger: error starring tag:", tag, err)
				}
			} else {
				if _, err := it.tc.Delete("tags", "favorite", tag); err != nil {
					fmt.Println("imageTagger: error unstarring tag:", tag, err)
				}
			}
		}()
	}

	closeBtn := widget.NewButton("✕", func() { it.HidePanel() })
	header := container.NewBorder(nil, nil,
		widget.NewLabel("Image tags"),
		closeBtn,
	)

	bg := canvas.NewRectangle(theme.BackgroundColor())
	inner := container.NewBorder(header, nil, nil, nil, it.ts)
	it.Panel = container.NewStack(bg, container.NewPadded(inner))
	it.Panel.Hide()

	return it
}

// SetAllTags replaces the search trie and the quick-pick list with the full
// tag list. Star state (☆/★) is preserved from the current favoriteTags set.
// Safe to call from the UI goroutine at any time (e.g. after a profile switch).
func (it *imageTagger) SetAllTags(tags []string) {
	it.allTags = append([]string(nil), tags...)
	it.ts.ClearAllTags()
	for _, tag := range tags {
		it.ts.AddTag(tag)
	}
	// Show every tag so they are all browsable; ☆/★ indicates favorite status.
	it.ts.SetFavorites(tags)
	it.ts.SetStarred(it.starredList())
}

// SetFavoriteTags records which tags are currently starred in the tie
// ("tags","favorite") relation and refreshes the ☆/★ button state.
// Called by makeTagSidebar after every tc.Get("tags") fetch.
func (it *imageTagger) SetFavoriteTags(tags []string) {
	it.favoriteTags = make(map[string]bool, len(tags))
	for _, t := range tags {
		it.favoriteTags[t] = true
	}
	it.ts.SetStarred(tags)
}

// starredList returns the names of all currently starred tags.
func (it *imageTagger) starredList() []string {
	starred := make([]string, 0, len(it.favoriteTags))
	for t, v := range it.favoriteTags {
		if v {
			starred = append(starred, t)
		}
	}
	return starred
}

// ShowForImage opens the panel for the image with hash. If the panel was
// already open for a different image the selected list is reset first so
// the new image's tags are fetched and displayed.
func (it *imageTagger) ShowForImage(hash string) {
	if it.hash != hash {
		it.hash = hash
		it.appliedTags = nil
		it.ts.ClearSelected()
		it.loadCurrentTags()
	}
	it.Panel.Show()
	canvas.Refresh(it.Panel)
}

// HidePanel hides the tag panel without clearing state; reopening for the
// same image will not re-fetch tags from tie.
func (it *imageTagger) HidePanel() {
	it.Panel.Hide()
	canvas.Refresh(it.Panel)
	if it.OnHide != nil {
		it.OnHide()
	}
}

// Toggle opens the panel when hidden or closes it when visible.
// hash is the content hash of the image the user is currently viewing.
func (it *imageTagger) Toggle(hash string) {
	if it.Panel.Visible() && it.hash == hash {
		it.HidePanel()
	} else {
		it.ShowForImage(hash)
	}
}

// SetCurrentHash records which image hash is currently displayed without
// opening the panel. This lets Toggle open for the correct image on the
// first tap even if the panel has never been shown before.
func (it *imageTagger) SetCurrentHash(hash string) {
	if it.hash == hash {
		return
	}
	it.hash = hash
	// If the panel is open, switch it to the new image immediately.
	if it.Panel.Visible() {
		it.appliedTags = nil
		it.ts.ClearSelected()
		it.loadCurrentTags()
	}
}

// loadCurrentTags fetches the tag triples for it.hash from tie in a goroutine
// and pre-populates the selected list so the user can see (and remove) them.
func (it *imageTagger) loadCurrentTags() {
	hash := it.hash
	if hash == "" {
		return
	}
	go func() {
		row, err := it.tc.Get(hash)
		if err != nil {
			fmt.Println("imageTagger: error fetching tags:", err)
			return
		}
		tags := client.RowValues(row, "tag")
		fyne.Do(func() {
			if it.hash != hash {
				return // stale: user navigated to a different image
			}
			it.appliedTags = append([]string(nil), tags...)
			for _, tag := range tags {
				it.ts.AddSelected(tagselection.NewTagItemData(tag))
			}
		})
	}()
}

// syncTags is called by ts.OnSelectedChanged with the current union of
// included+excluded tags. It diffs newTags against the appliedTags snapshot
// and persists the additions/removals to tie.
func (it *imageTagger) syncTags(newTags []string) {
	if it.hash == "" {
		return
	}
	hash := it.hash

	var added, removed []string
	for _, t := range newTags {
		if !slices.Contains(it.appliedTags, t) {
			added = append(added, t)
		}
	}
	for _, t := range it.appliedTags {
		if !slices.Contains(newTags, t) {
			removed = append(removed, t)
		}
	}

	// Update the snapshot before launching the goroutine so rapid sequential
	// changes don't see stale state and duplicate operations.
	it.appliedTags = append([]string(nil), newTags...)

	if len(added) == 0 && len(removed) == 0 {
		return
	}

	go func() {
		for _, tag := range added {
			if _, err := it.tc.Add(hash, "tag", tag); err != nil {
				fmt.Println("imageTagger: error adding tag:", tag, err)
				continue
			}
			// Register in the global "tags"/"all" index so the tag shows up
			// in the sidebar and future image-tagger sessions.
			if _, err := it.tc.Add("tags", "all", tag); err != nil {
				fmt.Println("imageTagger: error registering tag:", tag, err)
			}
		}
		for _, tag := range removed {
			if _, err := it.tc.Delete(hash, "tag", tag); err != nil {
				fmt.Println("imageTagger: error removing tag:", tag, err)
			}
		}
	}()
}
