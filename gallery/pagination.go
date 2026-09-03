package gallery

import (
	"image/color"
	"sort"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Pagination links: the bottom bar shows "Prev", a window of 2-6 page links
// (elided in the middle with "…" when there are more pages than slots), and
// "Next". The number of slots depends on the available grid width, so the
// links are rebuilt when the window is resized.

const (
	// minPageSlots / maxPageSlots bound how many numbered page links fit
	// between Prev and Next.
	minPageSlots = 2
	maxPageSlots = 6
	// pageSlotWidth is the approximate width budget per numbered page link
	// ("500-1000", padding, and separators), used to derive the slot count
	// from the grid width.
	pageSlotWidth = float32(130)
	// paginationReservedWidth is the width budget for the Prev/Next links and
	// the side buttons (sidebar toggle, ☰ menu) around the pagination box.
	paginationReservedWidth = float32(260)
)

// paginationSlotCount maps a grid width to the number of numbered page links
// shown in the bottom bar (minPageSlots..maxPageSlots).
func paginationSlotCount(width float32) int {
	n := int((width - paginationReservedWidth) / pageSlotWidth)
	if n < minPageSlots {
		return minPageSlots
	}
	if n > maxPageSlots {
		return maxPageSlots
	}
	return n
}

// pageSlots computes which page numbers appear as links. When all pages fit
// in slots, every page is shown. Otherwise the first and last pages are
// always shown and the remaining budget is spent expanding around the current
// page, so the hidden ranges collapse to one "…" gap in the middle (two when
// the current page sits deep in the interior).
func pageSlots(current, maxPages, slots int) []int {
	if maxPages <= slots {
		pages := make([]int, maxPages)
		for i := range pages {
			pages[i] = i
		}
		return pages
	}
	if slots < minPageSlots {
		slots = minPageSlots
	}
	set := map[int]bool{0: true, maxPages - 1: true}
	if slots > 2 && current > 0 && current < maxPages-1 {
		set[current] = true
	}
	for d := 1; len(set) < slots && d < maxPages; d++ {
		for _, p := range []int{current - d, current + d} {
			if p >= 0 && p < maxPages && len(set) < slots {
				set[p] = true
			}
		}
	}
	pages := make([]int, 0, len(set))
	for p := range set {
		pages = append(pages, p)
	}
	sort.Ints(pages)
	return pages
}

// buildPagination rebuilds the bottom-bar page links for the current page
// count. Links after the first two (Prev/Next) are replaced; when the pages
// do not all fit, one elided "…" label stands for the hidden middle range.
func (viewer *Gallery) buildPagination() {
	if viewer.bottomBar == nil {
		prevPage := widget.NewHyperlink("Prev", nil)
		prevPage.OnTapped = func() { viewer.ChangePage(viewer.currentPage - 1) }
		nextPage := widget.NewHyperlink("Next", nil)
		nextPage.OnTapped = func() { viewer.ChangePage(viewer.currentPage + 1) }
		viewer.bottomBar = container.NewHBox(prevPage, nextPage)
	}
	viewer.bottomBar.Objects = viewer.bottomBar.Objects[:2]

	imagesPerPage := viewer.config.General.ImagesPerPage
	if imagesPerPage <= 0 {
		imagesPerPage = 1
	}
	viewer.maxPages = len(viewer.imageFiles) / imagesPerPage
	if len(viewer.imageFiles)%imagesPerPage != 0 {
		viewer.maxPages++
	}
	if viewer.maxPages < 1 {
		viewer.maxPages = 1
	}

	slots := maxPageSlots
	if viewer.layout != nil && viewer.layout.grid != nil {
		slots = paginationSlotCount(viewer.layout.grid.Size().Width)
	}
	pages := pageSlots(viewer.currentPage, viewer.maxPages, slots)
	prevPageIdx := -1
	for _, i := range pages {
		// One "…" label between non-consecutive pages (the elided middle).
		if prevPageIdx >= 0 && i > prevPageIdx+1 {
			viewer.bottomBar.Add(widget.NewLabel("…"))
		}
		prevPageIdx = i
		i := i
		start := i*imagesPerPage + 1
		end := start + imagesPerPage - 1
		if i == viewer.maxPages-1 {
			end = len(viewer.imageFiles)
		}
		page := widget.NewHyperlink(strconv.Itoa(start)+"-"+strconv.Itoa(end), nil)
		page.OnTapped = func() {
			viewer.ChangePage(i)
		}
		if i == viewer.currentPage {
			page.TextStyle.Bold = true
		}
		viewer.bottomBar.Add(page)
	}
	viewer.bottomBar.Refresh()
}

// sizeWatcher is a transparent widget stacked behind the gallery grid whose
// sole job is to report grid resizes: Fyne offers no window-resize callback,
// but the grid tracks the window size, and the pagination link count depends
// on that width. Stacked below the scroll container it never intercepts
// pointer events.
type sizeWatcher struct {
	widget.BaseWidget
	bg       *canvas.Rectangle
	onResize func(width float32)
	width    float32
}

func newSizeWatcher(onResize func(width float32)) *sizeWatcher {
	w := &sizeWatcher{
		bg:       canvas.NewRectangle(color.Transparent),
		onResize: onResize,
	}
	w.ExtendBaseWidget(w)
	return w
}

func (w *sizeWatcher) Resize(size fyne.Size) {
	w.BaseWidget.Resize(size)
	if size.Width != w.width {
		w.width = size.Width
		w.onResize(size.Width)
	}
}

func (w *sizeWatcher) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.bg)
}
