package main

import (
	"fmt"
	"slices"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tagselection"
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
	tc *client.TieClient
	// hash is the content hash of the currently VIEWED image ("" = none),
	// tracked via SetCurrentHash whether the panel is open or not.
	hash string
	// panelHash is the hash whose tags are currently LOADED in the panel.
	// It trails hash when the user navigates with the panel closed; opening
	// the panel then detects the mismatch and loads the new image's tags
	// (regression: a single hash field made ShowForImage's staleness check
	// always pass, so the panel kept showing the previous image's tags).
	panelHash   string
	appliedTags []string // snapshot of tags applied to panelHash in tie
	// appliedRating is the rating (0 = unrated, else 1..5) currently stored in
	// tie for panelHash, snapshotted at load so syncRating can diff against it.
	appliedRating int

	// allTags is the full known tag list (from tc.Get("tags")), kept here so
	// OnNewTag can append and refresh the available list without a round-trip.
	allTags []string

	ts     *tagselection.TagSelection
	rating *starRating
	Panel  fyne.CanvasObject // embed into viewer.Content as a stack overlay layer

	// OnHide, when non-nil, is called after the panel becomes hidden.
	// Use it to restore keyboard focus to the image view on desktop.
	OnHide func()
	// OnTagsAdded, when non-nil, is called on the UI goroutine with tags that
	// were successfully written to tie, so the sidebar can add genuinely new
	// tags to its search trie without a full reload.
	OnTagsAdded func(tags []string)
	// OnTagsChanged, when non-nil, is called on the UI goroutine with the
	// panel image's full tag list whenever the user edits it here, so other
	// views of the same image (the quick tag bar) stay in step.
	OnTagsChanged func(hash string, tags []string)
}

// newImageTagger creates an imageTagger. Call SetAllTags and SetFavoriteTags
// after the tag list has been fetched from tie to populate the panel.
func newImageTagger(window fyne.Window, tc *client.TieClient) *imageTagger {
	it := &imageTagger{
		tc: tc,
	}
	it.ts = tagselection.NewTagSelection(window)
	it.ts.ShowStars = true // must be set before the widget is first rendered
	// Keep the search entry focused after a selection or Escape so the user
	// can add several tags in a row without clicking back into the box.
	it.ts.KeepSearchFocus = true
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
				err = it.tc.RegisterFavorite(tag)
			} else {
				err = it.tc.UnregisterFavorite(tag)
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

	// The star rating widget writes the (hash,"rating","<1-5>") triple for the
	// image currently loaded in the panel. Clearing (rating 0) removes it.
	it.rating = newStarRating(24, func(rating int) {
		it.syncRating(rating)
	})

	closeBtn := widget.NewButton("✕", func() { it.HidePanel() })
	header := container.NewBorder(nil, nil,
		widget.NewLabel("Image tags"),
		closeBtn,
	)
	ratingRow := container.NewBorder(nil, nil, widget.NewLabel("Rating"), nil, container.NewHBox(it.rating))
	top := container.NewVBox(header, ratingRow)

	bg := canvas.NewRectangle(theme.BackgroundColor())
	inner := container.NewBorder(top, nil, nil, nil, it.ts)
	it.Panel = container.NewStack(bg, container.NewPadded(inner))
	it.Panel.Hide()

	return it
}

// syncRating persists a user-chosen rating for the panel's image. It diffs
// against the appliedRating snapshot: a change to 1..5 replaces the stored
// value (delete old, add new), and clearing (0) just deletes it. Tags and
// rating share the same content-hash subject, so this rates the content
// everywhere it appears.
func (it *imageTagger) syncRating(rating int) {
	if it.panelHash == "" {
		return
	}
	hash := it.panelHash
	old := it.appliedRating
	if rating == old {
		return
	}
	it.appliedRating = rating
	go func() {
		if old != 0 {
			if _, err := it.tc.Delete(hash, "rating", strconv.Itoa(old)); err != nil {
				fmt.Printf("imageTagger: error clearing rating: %v\n", err)
			}
		}
		if rating != 0 {
			if _, err := it.tc.Add(hash, "rating", strconv.Itoa(rating)); err != nil {
				fmt.Printf("imageTagger: error setting rating %d: %v\n", rating, err)
			}
		}
	}()
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

// ShowForImage opens the panel for the image with hash. If the panel holds a
// different image's tags the selected list is reset first so the new image's
// tags are fetched and displayed.
func (it *imageTagger) ShowForImage(hash string) {
	if it.panelHash != hash {
		it.panelHash = hash
		it.appliedTags = nil
		it.ts.SetSelected(nil)
		it.appliedRating = 0
		it.rating.SetRating(0)
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
	if it.Panel.Visible() && it.panelHash == hash {
		it.HidePanel()
	} else {
		it.ShowForImage(hash)
	}
}

// SetCurrentHash records which image hash is currently displayed without
// opening the panel. This lets Toggle open for the correct image on the
// first tap even if the panel has never been shown before. When the panel
// IS open, it switches to the new image's tags immediately.
func (it *imageTagger) SetCurrentHash(hash string) {
	if it.hash == hash {
		return
	}
	it.hash = hash
	if it.Panel.Visible() && it.panelHash != hash {
		it.panelHash = hash
		it.appliedTags = nil
		it.ts.SetSelected(nil)
		it.appliedRating = 0
		it.rating.SetRating(0)
		it.loadCurrentTags()
	}
}

// loadCurrentTags fetches the tag triples for it.panelHash from tie in a
// goroutine and pre-populates the selected list so the user can see (and
// remove) them. SetSelected is used instead of AddSelected: the tags come
// FROM tie, so OnSelectedChanged must not fire (each partial AddSelected
// state would diff as spurious deletes/adds against appliedTags).
func (it *imageTagger) loadCurrentTags() {
	hash := it.panelHash
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
		rating, _ := strconv.Atoi(client.RowFirst(row, "rating"))
		fyne.Do(func() {
			if it.panelHash != hash {
				return // stale: user navigated to a different image
			}
			it.appliedTags = append([]string(nil), tags...)
			it.ts.SetSelected(tags)
			it.appliedRating = rating
			it.rating.SetRating(rating)
		})
	}()
}

// SetTags replaces the panel's applied-tag list for hash from an external
// source (the quick tag bar) without writing to tie. Ignored unless the panel
// currently holds that image. Uses SetSelected so OnSelectedChanged does not
// fire and diff the change back into tie.
func (it *imageTagger) SetTags(hash string, tags []string) {
	if hash == "" || it.panelHash != hash {
		return
	}
	it.appliedTags = append([]string(nil), tags...)
	it.ts.SetSelected(tags)
}

// syncTags is called by ts.OnSelectedChanged with the current union of
// included+excluded tags. It diffs newTags against the appliedTags snapshot
// and persists the additions/removals to tie.
func (it *imageTagger) syncTags(newTags []string) {
	if it.panelHash == "" {
		return
	}
	hash := it.panelHash

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
	if it.OnTagsChanged != nil {
		it.OnTagsChanged(hash, it.appliedTags)
	}

	go func() {
		var failed, addedOK []string
		for _, tag := range added {
			if _, err := it.tc.Add(hash, "tag", tag); err != nil {
				fmt.Printf("imageTagger: error adding tag %q: %v\n", tag, err)
				failed = append(failed, tag)
				continue
			}
			addedOK = append(addedOK, tag)
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

		// Let the sidebar add newly registered tags to its search trie
		// without a full reload (which would clear the search selection).
		if len(addedOK) > 0 && it.OnTagsAdded != nil {
			cb := it.OnTagsAdded
			fyne.Do(func() { cb(addedOK) })
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
				if it.panelHash != hash {
					return // stale: user navigated away
				}
				// Rebuild the selected list to match tie's ground truth.
				it.appliedTags = append([]string(nil), trueTags...)
				it.ts.SetSelected(trueTags)
				if it.OnTagsChanged != nil {
					it.OnTagsChanged(hash, it.appliedTags)
				}
				// TODO: Show error dialog to user (requires window reference)
				fmt.Printf("imageTagger: reconciled after %d failed tag operations\n", len(failed))
			})
		}
	}()
}
