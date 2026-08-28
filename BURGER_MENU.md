# Gallery Burger Menu Implementation

## Overview

Added a **☰ burger menu button** to the gallery bottom bar (right side) that provides access to gallery settings and options via a popup menu. This solves the discoverability problem on mobile while also being useful on desktop.

## UI Layout

```
┌────────────────────────────────────────────────────┐
│                                                    │
│              Gallery Grid (tiles)                  │
│                                                    │
└────────────────────────────────────────────────────┘
┌────────────────────────────────────────────────────┐
│ ◀  │ Prev  Next  1-100  101-200  ...         │ ☰  │
└────────────────────────────────────────────────────┘
    ↑                                              ↑
Sidebar toggle                              Menu button
(tieview only)                            (both apps)
```

## Implementation

### Gallery Struct Changes (gallery.go)

Added `menuButton` field:
```go
type Gallery struct {
    // ...
    menuButton    *widget.Button    // ☰ menu button in the bottom bar (right side)
    // ...
}
```

### CreateView() Updates

The bottom bar now uses a Border layout with three sections:
- **Left**: Sidebar toggle (◀/▶) - tieview only, nil in imgview
- **Center**: Pagination bar (Prev/Next/page links)
- **Right**: Menu button (☰) - both apps

```go
var left, right fyne.CanvasObject
if viewer.sidebarToggle != nil {
    left = viewer.sidebarToggle
}
right = viewer.menuButton
bottom = container.NewBorder(nil, nil, left, right, viewer.bottomBar)
```

### Menu Popup (showGalleryMenu)

Creates a popup menu with:
1. **Show/Hide filenames** - toggles `layout.showLabels`
   - Label updates dynamically based on current state
   - Calls `layout.ToggleLabels()` which refreshes the grid
2. (Future options go here)

Menu positioning:
- Appears **above** the ☰ button
- **Right-aligned** to the button
- Uses `AbsolutePositionForObject()` to calculate button position
- Positioned relative to canvas for proper mobile display

```go
buttonPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(viewer.menuButton)
buttonSize := viewer.menuButton.Size()
menuPos := fyne.NewPos(
    buttonPos.X+buttonSize.Width-popUpMenu.Size().Width,
    buttonPos.Y-popUpMenu.Size().Height,
)
popUpMenu.ShowAtPosition(menuPos)
```

## Menu Items

### Current

1. **Show filenames / Hide filenames**
   - Dynamic label based on `layout.showLabels` state
   - Toggles filename labels for entire gallery
   - Saves ~22px vertical space per row when hidden

### Future Possibilities

The menu architecture supports easy addition of more options:

- **Sort order**: Name, Date, Size, Type
- **Grid size**: Compact, Normal, Large (adjust TileWidth)
- **Refresh thumbnails**: Re-generate all thumbnails
- **Settings**: Open settings dialog
- **About**: App version and info
- **Select mode**: Enable multi-select for batch operations
- **Filter by type**: Images only, Videos only, All

Example:
```go
items = append(items, fyne.NewMenuItem("Sort by name", func() {
    viewer.sortGallery(SortByName)
}))

items = append(items, fyne.NewMenuItem("Refresh thumbnails", func() {
    viewer.layout.refreshThumbs = true
    viewer.ChangeGallery()
}))
```

## Benefits

1. **Discoverable**: Visible button vs hidden gesture (double-tap)
2. **Standard**: Familiar UI pattern (☰ = settings/options)
3. **Extensible**: Easy to add more menu items
4. **Consistent**: Same behavior on mobile and desktop
5. **Non-conflicting**: Doesn't interfere with tile tap/drag gestures
6. **Accessible**: Large tap target on mobile (button size)

## Testing

### Desktop
1. Open gallery with images
2. Click ☰ button (bottom-right)
3. Menu should appear above button, aligned right
4. Click "Show filenames"
5. Filenames should appear below thumbnails
6. Click ☰ again, click "Hide filenames"
7. Filenames should disappear
8. Alternatively, press 'N' key to toggle (still works)

### Mobile
1. Open DCIM/Camera folder
2. Tap ☰ button (bottom-right corner)
3. Menu should appear above button
4. Tap "Show filenames"
5. Filenames should appear (basenames only, not full paths)
6. Tap ☰ again, tap "Hide filenames"
7. Filenames should disappear, more images visible

### Tieview Specific
1. Open tieview with tag query
2. Verify ☰ button is on right side
3. Verify ◀ button is on left side (sidebar toggle)
4. Both buttons should work independently
5. Menu should still appear correctly positioned

## Code Location

- **Gallery struct**: `gallery/gallery.go` lines ~89
- **CreateView()**: `gallery/gallery.go` lines ~235-256
- **showGalleryMenu()**: `gallery/gallery.go` lines ~258-294

## Related Features

- **Filename labels**: `gallery/tilelayout.go` - ToggleLabels()
- **Sidebar toggle**: `gallery/gallery.go` - ToggleSidebar()
- **Keyboard shortcut**: `gallery/tilelayout.go` - 'N' key binding (desktop only)

## Notes

- The menu button is created once in `CreateView()` and reused
- Menu items are rebuilt on each open (allows dynamic labels)
- Popup dismisses automatically when user clicks outside
- No changes needed to cmd/imgview or cmd/tie-view mains
- Works with existing platform detection (mobile vs desktop)
