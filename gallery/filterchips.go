package gallery

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// FilterChip is one entry in the gallery's filter summary row: a compact,
// tappable label describing something narrowing the current view (e.g. a
// selected tag). When OnRemove is set the chip carries a ✕ that drops just
// that filter.
type FilterChip struct {
	Label    string
	OnRemove func()
}

// SetFilterChips replaces the filter summary row shown above the grid, and the
// action run when the row itself is tapped (typically: open the sidebar drawer
// so the user can change the selection). Passing no chips hides the row.
//
// The row exists for the drawer layout: with the sidebar off-screen, the
// selected tags are invisible, so the user cannot see why the grid holds what
// it holds. It is harmless in split layouts, where callers simply don't set it.
func (viewer *Gallery) SetFilterChips(chips []FilterChip, onTap func()) {
	viewer.filterChips = chips
	viewer.filterChipsOnTap = onTap
	viewer.rebuildFilterChips()
}

// TagFilterChips builds a chip list from an include/exclude tag selection, the
// shape every tag sidebar in this repo produces. Excluded tags are prefixed
// with a minus so the two senses are distinguishable at a glance, and each
// chip's remove callback is handed the plain tag name.
func TagFilterChips(include, exclude []string, remove func(tag string)) []FilterChip {
	chips := make([]FilterChip, 0, len(include)+len(exclude))
	add := func(tags []string, prefix string) {
		for _, tag := range tags {
			tag := tag
			chip := FilterChip{Label: prefix + tag}
			if remove != nil {
				chip.OnRemove = func() { remove(tag) }
			}
			chips = append(chips, chip)
		}
	}
	add(include, "")
	add(exclude, "−")
	return chips
}

// FilterChipRow renders a chip list as a horizontally scrollable row — the
// same rendering SetFilterChips places above the grid — for app-level views
// that replace the gallery grid but keep the filter summary (e.g. tie-audio's
// album table view). onTap is the row's own action (typically: open the tag
// picker). Returns nil when there are no chips, so the caller can skip the row.
func FilterChipRow(chips []FilterChip, onTap func()) fyne.CanvasObject {
	if len(chips) == 0 {
		return nil
	}
	objects := make([]fyne.CanvasObject, 0, len(chips)+1)
	if onTap != nil {
		// A leading affordance that opens the picker, so the row is useful even
		// when every chip's own tap is taken by its remove button.
		edit := widget.NewButtonWithIcon("", theme.SearchIcon(), onTap)
		edit.Importance = widget.LowImportance
		objects = append(objects, edit)
	}
	for _, chip := range chips {
		objects = append(objects, newFilterChipButton(chip, onTap))
	}
	return container.NewHScroll(container.NewHBox(objects...))
}

// rebuildFilterChips repopulates the chip row from the stored chips. It is a
// no-op until CreateView has built the row's container.
func (viewer *Gallery) rebuildFilterChips() {
	row := viewer.filterChipRow
	if row == nil {
		return
	}
	content := FilterChipRow(viewer.filterChips, viewer.filterChipsOnTap)
	if content == nil {
		row.Hide()
		viewer.refreshFilterChipParent()
		return
	}

	row.Objects = []fyne.CanvasObject{content}
	row.Show()
	row.Refresh()
	viewer.refreshFilterChipParent()
}

// refreshFilterChipParent re-lays out the Border holding the chip row, so the
// row appearing or disappearing actually changes the space the grid gets (a
// container's own Refresh lays out its children, not its parent).
//
// A container that has not been laid out yet reports size zero, and
// Container.Refresh lays its children out at *that* size — which would collapse
// the grid and its scroller to nothing. So the refresh is skipped until the
// parent has a real size, which is exactly the case CreateView produces (it
// builds this Border fresh, and the canvas sizes it right after).
func (viewer *Gallery) refreshFilterChipParent() {
	if viewer.filterChipParent == nil {
		return
	}
	if viewer.filterChipParent.Size().IsZero() {
		return
	}
	viewer.filterChipParent.Refresh()
}

// newFilterChipButton renders one chip: a label button that runs onTap, plus a
// ✕ button when the chip is removable.
func newFilterChipButton(chip FilterChip, onTap func()) fyne.CanvasObject {
	label := widget.NewButton(chip.Label, func() {
		if onTap != nil {
			onTap()
		}
	})
	label.Importance = widget.LowImportance
	if chip.OnRemove == nil {
		return label
	}
	remove := widget.NewButtonWithIcon("", theme.CancelIcon(), chip.OnRemove)
	remove.Importance = widget.LowImportance
	return container.NewHBox(label, remove)
}
