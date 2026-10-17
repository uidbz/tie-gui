// The chrome around the grid: Init, the sidebar and its drawer, the bottom bar
// and its pagination slot, the ☰ menu, and the label toggle.
package gallery

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Init completes Gallery initialization by wiring hotkeys and creating the
// layout. Must be called after NewGallery and after setting optional fields
// (Sidebar, Thumbnailer, callbacks). See NewGallery documentation for the
// two-step construction pattern rationale.
func (viewer *Gallery) Init() {
	viewer.layout = NewTileLayout(viewer.config, viewer.window, viewer.app, viewer, viewer.tileOnclick)
	empty := make([]fyne.CanvasObject, 0)
	viewer.gallery = container.New(viewer.layout, empty...)
	viewer.layout.grid = viewer.gallery
	viewer.layout.InitHotkeys()
	viewer.InitHotkeys()
	// The floating image-view menu button opens the same popup menu as the
	// bottom-bar button. It is stacked over the image by ChangeImage (the
	// bottom bar is not on screen then); a separate instance is required
	// because Fyne objects cannot be shared between two parents.
	viewer.imageMenuButton = widget.NewButton("☰", func() {
		viewer.showGalleryMenu()
	})
	viewer.imageMenuOverlay = container.NewBorder(nil,
		container.NewHBox(layout.NewSpacer(), viewer.imageMenuButton), nil, nil)
}

// ToggleSidebar hides the sidebar when it is visible, and restores it when
// it is hidden. In split mode it rebuilds the gallery layout immediately so
// the change takes effect without a restart; in drawer mode it just opens or
// closes the overlay, which leaves the grid (and its scroll position)
// untouched.
func (viewer *Gallery) ToggleSidebar() {
	if viewer.drawer != nil {
		if viewer.drawer.IsOpen() {
			viewer.CloseSidebar()
		} else {
			viewer.OpenSidebar()
		}
		return
	}
	if viewer.Sidebar != nil {
		viewer.sidebarStored = viewer.Sidebar
		viewer.Sidebar = nil
	} else {
		viewer.Sidebar = viewer.sidebarStored
	}
	viewer.CreateView()
	viewer.window.SetContent(viewer.Content)
}

// OpenSidebar shows the sidebar drawer. It is a no-op in split mode (where the
// sidebar is already on screen) and when there is no sidebar.
func (viewer *Gallery) OpenSidebar() {
	if viewer.drawer == nil {
		return
	}
	viewer.drawer.Open()
	viewer.updateSidebarToggle()
	viewer.notifySidebarToggled(true)
}

// CloseSidebar hides the sidebar drawer. Apps call it after the user makes a
// selection in the sidebar, so picking a tag returns them to the grid. It is
// also the drawer's own scrim-dismiss path.
func (viewer *Gallery) CloseSidebar() {
	if viewer.drawer == nil {
		return
	}
	viewer.drawer.Close()
	viewer.updateSidebarToggle()
	viewer.notifySidebarToggled(false)
}

// notifySidebarToggled reports a drawer visibility change to the app.
func (viewer *Gallery) notifySidebarToggled(open bool) {
	if viewer.OnSidebarToggled != nil {
		viewer.OnSidebarToggled(open)
	}
}

// SidebarOpen reports whether the sidebar is currently on screen: always true
// in split mode with a sidebar set, and the drawer's state in drawer mode.
func (viewer *Gallery) SidebarOpen() bool {
	if viewer.drawer != nil {
		return viewer.drawer.IsOpen()
	}
	return viewer.Sidebar != nil
}

// DrawerObject returns the sidebar drawer's overlay object in drawer mode, so
// an app-level view that replaces the gallery grid (e.g. tie-audio's album
// table view) can stack the same drawer over its own content — OpenSidebar /
// CloseSidebar / SidebarOpen keep working regardless of which view parents it.
// The object must only ever be on screen in one parent at a time (the gallery
// grid and the replacing view are swapped via window content, never shown
// together). Nil in split mode or when there is no sidebar.
func (viewer *Gallery) DrawerObject() fyne.CanvasObject {
	if viewer.drawer == nil {
		return nil
	}
	return viewer.drawer.object
}

// updateSidebarToggle re-labels the bottom-bar sidebar button for the current
// state: an arrow pointing the way the split pane will move, or a filter icon
// in drawer mode (where the panel slides over the grid rather than beside it).
func (viewer *Gallery) updateSidebarToggle() {
	if viewer.sidebarToggle == nil {
		return
	}
	if viewer.drawer != nil {
		viewer.sidebarToggle.SetIcon(theme.SearchIcon())
		viewer.sidebarToggle.SetText("")
		return
	}
	if viewer.Sidebar != nil {
		viewer.sidebarToggle.SetText("◀")
	} else {
		viewer.sidebarToggle.SetText("▶")
	}
}

func (viewer *Gallery) CreateView() {
	var mainPage fyne.CanvasObject
	if viewer.scroll == nil {
		viewer.scroll = container.NewScroll(viewer.gallery)
	}
	// The size watcher sits below the scroll container (never intercepting
	// pointer events) and reports grid-width changes so the pagination links
	// can be rebuilt for the new window width.
	if viewer.sizeWatcher == nil {
		viewer.sizeWatcher = newSizeWatcher(func(width float32) {
			if viewer.galleryLoaded && viewer.bottomBar != nil {
				viewer.buildPagination()
			}
		})
	}
	// On mobile, overlay the grid with a horizontal-swipe catcher so the app can
	// page to an adjacent view; it forwards vertical drags back to the scroller.
	// The callbacks are read late (they may be wired after CreateView), so the
	// overlay is added whenever the platform uses drag gestures.
	grid := fyne.CanvasObject(container.NewStack(viewer.sizeWatcher, viewer.scroll))
	if viewer.platform != nil && viewer.platform.UsesMobileDragGestures() {
		grid = container.NewStack(viewer.sizeWatcher, viewer.scroll, newGridSwipeOverlay(viewer))
	}
	// The filter summary row sits above the grid (hidden until chips are set),
	// so a drawer layout still shows what is narrowing the view.
	if viewer.filterChipRow == nil {
		viewer.filterChipRow = container.NewStack()
		viewer.filterChipRow.Hide()
	}
	viewer.filterChipParent = container.NewBorder(viewer.filterChipRow, nil, nil, nil, grid)
	grid = viewer.filterChipParent

	sidebar := viewer.Sidebar
	switch {
	case sidebar != nil && viewer.SidebarDrawer:
		// Reuse an existing drawer: it already parents the sidebar object, and
		// re-wrapping the same object in a second drawer would give it two
		// parents.
		if viewer.drawer == nil {
			viewer.drawer = newSidebarDrawer(sidebar, viewer.CloseSidebar)
		}
		mainPage = container.NewStack(grid, viewer.drawer.object)
	case sidebar != nil:
		// Leaving drawer mode: drop the drawer so the sidebar has one parent.
		viewer.drawer = nil
		split := container.NewHSplit(sidebar, grid)
		split.SetOffset(0.2)
		mainPage = split
	default:
		viewer.drawer = nil
		mainPage = grid
	}
	viewer.rebuildFilterChips()

	// Lazily create the sidebar toggle button the first time a sidebar is
	// encountered. Update its label to reflect the current visibility state.
	hasSidebar := viewer.Sidebar != nil || viewer.sidebarStored != nil
	if hasSidebar && viewer.sidebarToggle == nil {
		viewer.sidebarToggle = widget.NewButton("◀", viewer.ToggleSidebar)
	}
	viewer.updateSidebarToggle()

	// Create menu button if it doesn't exist yet
	if viewer.menuButton == nil {
		viewer.menuButton = widget.NewButton("☰", func() {
			viewer.showGalleryMenu()
		})
	}

	// Compose the bottom bar: sidebar toggle on the left, menu on the right,
	// pagination in the center. The buttons are placed outside bottomBar.Objects
	// so the existing Objects[:2] (Prev/Next) trimming in LoadGallery is unaffected.
	//
	// bottom must stay a genuine nil interface when there is no bottom bar yet
	// (CreateView before LoadGallery): assigning the nil *fyne.Container to the
	// interface would hand Border a typed nil, which panics in its MinSize.
	var bottom fyne.CanvasObject
	viewer.bottomRow = nil
	if viewer.bottomBar != nil {
		var left, right fyne.CanvasObject
		if viewer.sidebarToggle != nil && !viewer.HideSidebarToggle {
			left = viewer.sidebarToggle
		}
		if !viewer.HideMenuButton {
			right = viewer.menuButton
		}
		viewer.bottomRow = container.NewBorder(nil, nil, left, right, viewer.bottomBar)
		bottom = viewer.bottomRow
	}

	viewer.mainBorder = container.NewBorder(nil, bottom, nil, nil, mainPage)
	viewer.Content.Objects = []fyne.CanvasObject{viewer.mainBorder}
	viewer.syncBottomRow()
}

// syncBottomRow hides the pagination links when there is only one page (they
// would read "Prev Next 1-37": nothing to navigate), and the whole bottom row
// when no side button remains beside them, handing the space back to the grid.
// Only a visibility change re-lays out the page: a *fyne.Container.Refresh
// would refresh (and re-upload) every tile.
func (viewer *Gallery) syncBottomRow() {
	if viewer.bottomBar == nil || viewer.bottomRow == nil {
		return
	}
	multi := viewer.maxPages > 1
	changed := viewer.bottomBar.Visible() != multi
	if multi {
		viewer.bottomBar.Show()
	} else {
		viewer.bottomBar.Hide()
	}
	// bottomRow is a Border(nil, nil, left, right, bottomBar): Objects[0] is
	// bottomBar, the rest are the side buttons.
	rowVisible := multi || len(viewer.bottomRow.Objects) > 1
	if viewer.bottomRow.Visible() != rowVisible {
		changed = true
		if rowVisible {
			viewer.bottomRow.Show()
		} else {
			viewer.bottomRow.Hide()
		}
	}
	if !changed {
		return
	}
	viewer.bottomRow.Layout.Layout(viewer.bottomRow.Objects, viewer.bottomRow.Size())
	if viewer.mainBorder != nil {
		viewer.mainBorder.Layout.Layout(viewer.mainBorder.Objects, viewer.mainBorder.Size())
		canvas.Refresh(viewer.mainBorder)
	}
}

// BottomBarVisible reports whether the gallery's own bottom bar (pagination
// and side buttons) is currently shown.
func (viewer *Gallery) BottomBarVisible() bool {
	return viewer.bottomRow != nil && viewer.bottomRow.Visible()
}

// ShowMenuAt opens the gallery's ☰ popup menu anchored above anchor's
// bottom-right corner — for apps that host the menu button themselves
// (HideMenuButton).
func (viewer *Gallery) ShowMenuAt(anchor fyne.CanvasObject) {
	viewer.showGalleryMenuAt(anchor)
}

// showGalleryMenu displays a popup menu with gallery options.
func (viewer *Gallery) showGalleryMenu() {
	// Position the menu near the button that opened it: the floating button
	// while the image view is on screen, otherwise the bottom-bar button.
	var anchor fyne.CanvasObject = viewer.menuButton
	if viewer.imageViewActive() && viewer.imageMenuButton != nil {
		anchor = viewer.imageMenuButton
	}
	viewer.showGalleryMenuAt(anchor)
}

func (viewer *Gallery) showGalleryMenuAt(anchor fyne.CanvasObject) {
	// Build menu items
	var items []*fyne.MenuItem

	// Toggle filenames option
	filenameLabel := "Show filenames"
	if viewer.layout != nil && viewer.layout.showLabels {
		filenameLabel = "Hide filenames"
	}
	items = append(items, fyne.NewMenuItem(filenameLabel, func() {
		if viewer.layout != nil {
			viewer.layout.ToggleLabels()
		}
	}))

	// Image info overlay: only meaningful while a single image is displayed.
	if viewer.imageViewActive() {
		infoLabel := "Image info"
		if viewer.infoOverlay != nil && viewer.infoOverlay.Visible() {
			infoLabel = "Hide image info"
		}
		items = append(items, fyne.NewMenuItem(infoLabel, func() {
			viewer.ToggleInfoOverlay()
		}))
	}

	// Future options can be added here:
	// - Sort order
	// - Grid size
	// - Refresh thumbnails
	// - Settings

	// App-specific options (e.g. tie-view's hidden-directory toggle).
	if viewer.MenuItems != nil {
		items = append(items, viewer.MenuItems()...)
	}

	// Create and show popup menu
	menu := fyne.NewMenu("", items...)
	popUpMenu := widget.NewPopUpMenu(menu, viewer.window.Canvas())

	// Position the menu at the bottom-right of the anchor.
	buttonPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	buttonSize := anchor.Size()

	// Position menu above the button, aligned to the right edge
	menuPos := fyne.NewPos(
		buttonPos.X+buttonSize.Width-popUpMenu.Size().Width,
		buttonPos.Y-popUpMenu.Size().Height,
	)
	popUpMenu.ShowAtPosition(menuPos)
}

// ToggleLabels shows or hides the name label under each tile, the programmatic
// equivalent of the bottom-bar menu's "Show filenames" item. Embedding apps
// can call it once after Init to default labels on (e.g. an album cover wall,
// where the title is essential). Safe to call before any tiles exist: the flag
// is flipped and subsequently created tiles honor it.
func (viewer *Gallery) ToggleLabels() {
	if viewer.layout != nil {
		viewer.layout.ToggleLabels()
	}
}
