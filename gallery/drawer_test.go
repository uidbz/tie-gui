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
