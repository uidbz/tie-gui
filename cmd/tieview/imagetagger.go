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
		tc: tc,
	}
	it.ts = tagselection.NewTagSelection(window)
	it.ts.ShowStars = true // must be set before the widget is first rendered
	// ShowIncludeExclude defaults to false — applied tags have no include/exclude
	// distinction, so the checkbox is hidden in the tagger context.
	it.ts.SetListLabel("Favorites")
	// Cap the favorites list so the panel doesn't grow taller than the screen
	// when there are many tags. The search box reaches all tags regardless.
	it.ts.SetFavoriteMaxRows(8)
	// Cap the applied-tags list too.
	it.ts.SetSelectedMaxRows(4)

	// OnSelectedChanged fires when the selected list changes (add or remove).
	it.ts.OnSelectedChanged = func() {
		included, excluded := it.ts.SelectedTags()
		// In tagger mode there's no include/exclude checkbox (ShowIncludeExclude=false),
		// but SelectedTags() still returns the split for API consistency. Union them.
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
		}
		// Add to trie so the tag is reachable from search going forward.
		it.ts.AddTag(tag)
		// New tags are unstarred by default; quick-pick list is unchanged.
		it.ts.AddSelected(tagselection.NewTagItemData(tag))
	}

	// OnStar fires when the user clicks the ☆/★ button on a tag in the
	// quick-pick list or search results. Update the starred set, persist to
	// tie, and rebuild the quick-pick list so only starred tags appear there.
	// On network failure, rolls back the optimistic UI update.
	it.ts.OnStar = func(tag string, isStarred bool) {
		// Optimistically update UI
		it.ts.ToggleStar(tag, isStarred)
		it.ts.SetFavoritesWithStars(it.ts.StarredTags())

		go func() {
			var err error
			if isStarred {
				_, err = it.tc.Add("tags", "favorite", tag)
			} else {
				_, err = it.tc.Delete("tags", "favorite", tag)
			}
			if err != nil {
				// Roll back the optimistic update on the UI goroutine
				fyne.Do(func() {
					it.ts.ToggleStar(tag, !isStarred) // reverse the toggle
					it.ts.SetFavoritesWithStars(it.ts.StarredTags())
					// TODO: Show error dialog to user (requires window reference)
					fmt.Printf("imageTagger: failed to %s tag %q: %v\n",
						map[bool]string{true: "star", false: "unstar"}[isStarred], tag, err)
				})
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

// SetAllTags replaces the search trie with the full tag list and refreshes
// the quick-pick list (which shows only starred tags).
// Safe to call from the UI goroutine at any time (e.g. after a profile switch).
func (it *imageTagger) SetAllTags(tags []string) {
	it.allTags = append([]string(nil), tags...)
	it.ts.ClearAllTags()
	for _, tag := range tags {
		it.ts.AddTag(tag)
	}
	// Quick-pick shows only starred tags; search reaches everything else.
	it.ts.SetFavoritesWithStars(it.ts.StarredTags())
}

// SetFavoriteTags records which tags are currently starred in the tie
// ("tags","favorite") relation, rebuilds the quick-pick list, and refreshes
// the ☆/★ button state in the search dropdown.
// Called by makeTagSidebar after every tc.Get("tags") fetch.
func (it *imageTagger) SetFavoriteTags(tags []string) {
	it.ts.SetFavoritesWithStars(tags)
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
		var failed []string
		for _, tag := range added {
			if _, err := it.tc.Add(hash, "tag", tag); err != nil {
				fmt.Printf("imageTagger: error adding tag %q: %v\n", tag, err)
				failed = append(failed, tag)
				continue
			}
			// Register in the global "tags"/"all" index so the tag shows up
			// in the sidebar and future image-tagger sessions.
			if _, err := it.tc.Add("tags", "all", tag); err != nil {
				fmt.Printf("imageTagger: error registering tag %q: %v\n", tag, err)
			}
		}
		for _, tag := range removed {
			if _, err := it.tc.Delete(hash, "tag", tag); err != nil {
				fmt.Printf("imageTagger: error removing tag %q: %v\n", tag, err)
				failed = append(failed, tag)
			}
		}

		// If any operations failed, re-fetch tags from tie to reconcile the UI.
		if len(failed) > 0 {
			row, err := it.tc.Get(hash)
			if err != nil {
				fmt.Printf("imageTagger: failed to reconcile after errors: %v\n", err)
				return
			}
			trueTags := client.RowValues(row, "tag")
			fyne.Do(func() {
				if it.hash != hash {
					return // stale: user navigated away
				}
				// Rebuild the selected list to match tie's ground truth.
				it.ts.ClearSelected()
				it.appliedTags = append([]string(nil), trueTags...)
				for _, tag := range trueTags {
					it.ts.AddSelected(tagselection.NewTagItemData(tag))
				}
				// TODO: Show error dialog to user (requires window reference)
				fmt.Printf("imageTagger: reconciled after %d failed tag operations\n", len(failed))
			})
		}
	}()
}
