package gallery

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestCompactLayout(t *testing.T) {
	desktop := NewPlatformFor(false)
	mobile := NewPlatformFor(true)

	// A desktop window is never compact, however narrow: the split layout is
	// pointer-driven there and the user can widen the window.
	for _, w := range []float32{0, 320, 700, 2560} {
		if desktop.CompactLayout(w) {
			t.Errorf("desktop CompactLayout(%v) = true, want false", w)
		}
	}

	// A width of 0 means "not laid out yet", which on mobile is assumed
	// compact: guessing the split layout for a phone would show it for a frame.
	tests := []struct {
		width float32
		want  bool
	}{
		{0, true},
		{-1, true},
		{360, true},
		{CompactWidth - 1, true},
		{CompactWidth, false},
		{1024, false},
	}
	for _, tt := range tests {
		if got := mobile.CompactLayout(tt.width); got != tt.want {
			t.Errorf("mobile CompactLayout(%v) = %v, want %v", tt.width, got, tt.want)
		}
	}
}

// newDrawerTestGallery builds a minimal gallery with a sidebar, without the
// image-directory machinery the other tests need.
func newDrawerTestGallery(t *testing.T, drawer bool) *Gallery {
	t.Helper()
	test.NewApp()
	win := test.NewWindow(nil)

	config := Config{}
	config.General.TileWidth = 300
	config.General.ImagesPerPage = 10
	config.General.ThumbnailDir = t.TempDir()

	viewer := NewGallery(fyne.CurrentApp(), win, config, nil)
	viewer.Sidebar = widget.NewLabel("sidebar")
	viewer.SidebarDrawer = drawer
	viewer.Init()
	viewer.CreateView()
	return viewer
}

func TestSidebarDrawerOpensAndCloses(t *testing.T) {
	viewer := newDrawerTestGallery(t, true)

	if viewer.drawer == nil {
		t.Fatal("drawer mode did not build a drawer")
	}
	if viewer.SidebarOpen() {
		t.Error("drawer starts open, want closed so the grid is visible first")
	}

	viewer.OpenSidebar()
	if !viewer.SidebarOpen() {
		t.Error("OpenSidebar did not open the drawer")
	}
	// ToggleSidebar must flip the drawer rather than removing the sidebar (the
	// split-mode behavior), which would drop it from the layout entirely.
	viewer.ToggleSidebar()
	if viewer.SidebarOpen() {
		t.Error("ToggleSidebar did not close the open drawer")
	}
	if viewer.Sidebar == nil {
		t.Error("drawer-mode toggle cleared Sidebar; it must stay set")
	}
	viewer.ToggleSidebar()
	if !viewer.SidebarOpen() {
		t.Error("ToggleSidebar did not reopen the closed drawer")
	}
	viewer.CloseSidebar()
	if viewer.SidebarOpen() {
		t.Error("CloseSidebar did not close the drawer")
	}
}

// In split mode there is no drawer, and the open/close calls must be harmless
// no-ops so apps can call them unconditionally.
func TestSidebarSplitModeHasNoDrawer(t *testing.T) {
	viewer := newDrawerTestGallery(t, false)

	if viewer.drawer != nil {
		t.Fatal("split mode built a drawer")
	}
	if !viewer.SidebarOpen() {
		t.Error("SidebarOpen = false in split mode with a sidebar set")
	}
	viewer.OpenSidebar()
	viewer.CloseSidebar()
	if viewer.Sidebar == nil {
		t.Error("no-op drawer calls disturbed the split sidebar")
	}

	// The split-mode toggle still hides and restores the pane.
	viewer.ToggleSidebar()
	if viewer.Sidebar != nil {
		t.Error("split-mode ToggleSidebar did not hide the sidebar")
	}
	viewer.ToggleSidebar()
	if viewer.Sidebar == nil {
		t.Error("split-mode ToggleSidebar did not restore the sidebar")
	}
}

// Switching layout modes must hand the sidebar object from the split to the
// drawer and back: a Fyne object with two parents renders in neither.
func TestSidebarModeSwitchReparentsOnce(t *testing.T) {
	viewer := newDrawerTestGallery(t, false)
	sidebar := viewer.Sidebar

	viewer.SidebarDrawer = true
	viewer.CreateView()
	if viewer.drawer == nil {
		t.Fatal("switching to drawer mode built no drawer")
	}
	if viewer.drawer.sidebar != sidebar {
		t.Error("drawer does not hold the original sidebar object")
	}

	viewer.SidebarDrawer = false
	viewer.CreateView()
	if viewer.drawer != nil {
		t.Error("switching back to split mode left the drawer holding the sidebar")
	}

	// And back again: a second drawer must be built, not the stale one reused.
	viewer.SidebarDrawer = true
	viewer.CreateView()
	if viewer.drawer == nil || viewer.drawer.sidebar != sidebar {
		t.Error("re-entering drawer mode did not rewrap the sidebar")
	}
}

func TestSidebarDrawerSwipeBackToGrid(t *testing.T) {
	viewer := newDrawerTestGallery(t, true)

	viewer.OpenSidebar()
	if !viewer.SidebarOpen() {
		t.Fatal("OpenSidebar did not open the drawer")
	}
	// A swipe that starts on the panel (not the scrim) dismisses it: the
	// panel's swipe scroller reports the leftward travel.
	viewer.drawer.swipe.scrolled(fyne.NewPos(drawerCloseDrag-1, 0))
	if !viewer.SidebarOpen() {
		t.Error("a sub-threshold swipe closed the drawer")
	}
	viewer.drawer.swipe.scrolled(fyne.NewPos(drawerCloseDrag, 0))
	if viewer.SidebarOpen() {
		t.Error("a leftward swipe across the panel did not close the drawer")
	}

	// Reopening starts from a fresh offset, so an earlier swipe does not carry
	// over and dismiss it again.
	viewer.OpenSidebar()
	if !viewer.SidebarOpen() {
		t.Fatal("OpenSidebar did not reopen the drawer")
	}
	if off := viewer.drawer.swipe.scroll.Offset.X; off != 0 {
		t.Errorf("reopened drawer swipe offset = %v, want 0", off)
	}
	viewer.drawer.swipe.scrolled(fyne.NewPos(drawerCloseDrag, 0))
	if viewer.SidebarOpen() {
		t.Error("after reopening, a swipe no longer closes the drawer")
	}
}

// The mobile driver delivers drag travel only in DragEvent.Dragged and leaves
// AbsolutePosition at zero, so the scrim must not depend on that position.
func TestDrawerScrimSwipeUsesDragDelta(t *testing.T) {
	dismissed := 0
	scrim := newDrawerScrim(func() { dismissed++ })

	scrim.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-20, 0)})
	if dismissed != 0 {
		t.Errorf("dismissed after 20 px, want the %d px threshold", drawerCloseDrag)
	}
	scrim.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-25, 1)})
	if dismissed != 1 {
		t.Fatalf("leftward drag of %d px fired %d dismisses, want 1", drawerCloseDrag, dismissed)
	}
	// Once fired, the rest of the same gesture must not fire again.
	scrim.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-200, 0)})
	if dismissed != 1 {
		t.Errorf("one gesture fired %d dismisses, want 1", dismissed)
	}
	scrim.DragEnd()

	// A vertical-dominant drag is not a dismiss gesture.
	scrim.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-10, 80)})
	if dismissed != 1 {
		t.Errorf("vertical drag dismissed the drawer")
	}
	scrim.DragEnd()

	// Nor is a rightward one.
	scrim.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(120, 0)})
	if dismissed != 1 {
		t.Errorf("rightward drag dismissed the drawer")
	}
}

func TestFilterChipsRowVisibility(t *testing.T) {
	viewer := newDrawerTestGallery(t, true)

	if viewer.filterChipRow == nil {
		t.Fatal("CreateView built no filter chip row")
	}
	if viewer.filterChipRow.Visible() {
		t.Error("chip row starts visible, want hidden until chips are set")
	}

	viewer.SetFilterChips(TagFilterChips([]string{"beach"}, []string{"blurry"}, nil), func() {})
	if !viewer.filterChipRow.Visible() {
		t.Error("chip row stayed hidden after chips were set")
	}

	viewer.SetFilterChips(nil, nil)
	if viewer.filterChipRow.Visible() {
		t.Error("chip row stayed visible with no chips")
	}
}

func TestTagFilterChips(t *testing.T) {
	var removed []string
	chips := TagFilterChips([]string{"a", "b"}, []string{"c"}, func(tag string) {
		removed = append(removed, tag)
	})

	if len(chips) != 3 {
		t.Fatalf("len(chips) = %d, want 3", len(chips))
	}
	// Excluded tags are visibly distinct, but the callback gets the bare tag.
	if chips[2].Label != "−c" {
		t.Errorf("exclude chip label = %q, want %q", chips[2].Label, "−c")
	}
	for _, chip := range chips {
		chip.OnRemove()
	}
	want := []string{"a", "b", "c"}
	for i, tag := range want {
		if removed[i] != tag {
			t.Fatalf("removed = %v, want %v", removed, want)
		}
	}

	// Without a remove callback the chips are display-only (no ✕ button).
	for _, chip := range TagFilterChips([]string{"a"}, nil, nil) {
		if chip.OnRemove != nil {
			t.Error("chip built with a nil remover carries an OnRemove")
		}
	}
}

// A single-page gallery hides its pagination links; the bottom row itself
// stays while a side button (sidebar toggle, ☰) remains, and disappears when
// an embedding app hosts both elsewhere.
func TestBottomRowSinglePage(t *testing.T) {
	viewer := newDrawerTestGallery(t, true)
	viewer.LoadGallery()
	viewer.CreateView()
	if viewer.bottomBar.Visible() {
		t.Error("pagination links visible on a single-page gallery")
	}
	if !viewer.BottomBarVisible() {
		t.Error("bottom row hidden although the toggle and ☰ are on it")
	}

	viewer.HideSidebarToggle = true
	viewer.HideMenuButton = true
	viewer.CreateView()
	if viewer.BottomBarVisible() {
		t.Error("bottom row visible with no pages and no side buttons")
	}

	// More than one page brings the links (and the row) back.
	viewer.maxPages = 3
	viewer.syncBottomRow()
	if !viewer.bottomBar.Visible() || !viewer.BottomBarVisible() {
		t.Error("multi-page gallery hid its pagination links")
	}
}
