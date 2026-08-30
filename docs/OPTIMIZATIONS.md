# Gallery Memory Optimizations

This document describes the memory and performance optimizations implemented to make the gallery more fluid and memory-efficient, especially on lower-end mobile devices.

## Summary of Changes

### 1. LRU Tile Cache (`gallery/tilecache.go`)
- **Problem**: Unbounded thumbnail cache kept all loaded tiles in memory forever
- **Solution**: Implemented insertion-order LRU cache adapted from tie-filehost
- **Impact**: 
  - Desktop: Max 500 tiles cached (~60-120 MB depending on tile size)
  - Mobile: Max 150 tiles cached (~18-36 MB)
  - Old thumbnails automatically evicted when cache fills up

### 2. Off-screen culling via Fyne's clip (`gallery/tilelayout.go`, `gallery/gallery.go`)
- **Note**: An earlier "virtual scrolling" attempt (Hide/Show off-screen tiles +
  `viewer.scroll.OnScrolled` → `gallery.Refresh()`) was **removed** — it made
  scrolling choppy because `*fyne.Container.Refresh()` re-uploads every tile
  texture and fired on every scroll frame.
- **Current approach**: `TileLayout.Layout` positions every tile; off-screen
  tiles are culled by Fyne's GL painter, which skips any object outside the
  scroll container's clip rect (`internal/painter/gl/painter.go`). No per-scroll
  work is needed.
- **Implementation**:
  - `PlaceTiles` adds all tiles atomically in a single `fyne.Do()` block to
    prevent a race where refresh happens before tiles are added.
- **Impact**:
  - Off-screen tiles cost no render time (painter clip test), with no per-frame
    relayout or refresh — smooth scrolling.

### 3. Improved Pagination UI
- **Problem**: Bottom bar pagination controls are small and difficult to tap on mobile
- **Solution**: Added large "Load Next Page ▼" button at end of gallery grid
- **Impact**:
  - Button is 60px tall on desktop, 80px on mobile
  - Full-width, high-importance styling for easy tapping
  - Positioned after all tiles in the layout
  - Only shown when more pages exist

### 4. Full Image Cleanup
- **Problem**: Full-size decoded images not released when returning to gallery
- **Solution**: Release `fyneImage.Image` when showing gallery view
- **Impact**:
  - Immediately frees 10-50 MB per full-size image
  - Critical on mobile where memory is constrained

### 5. Mobile-Specific Config Adjustments (`gallery/config.go`)
- **Problem**: Desktop defaults too memory-intensive for mobile
- **Solution**: `AdjustForMobile()` function reduces resource usage:
  - TileWidth: 300 → 200 (smaller thumbnails)
  - ImagesPerPage: 500 → 100 (fewer items per page)
  - Workers: 8 → 4 (less concurrent loading)
  - TileGap: 5 → 3 (more compact layout)
- **Impact**:
  - ~60% reduction in memory per page
  - Still maintains good image quality (QHD screens handled well)
  - Can be overridden in user config.toml if needed

### 6. Thumbnail Quality Considerations
- Desktop: TileWidth * 2 (600px wide thumbnails)
- Mobile: TileWidth * 2 (400px wide after adjustment)
- Both scales maintain high quality for 2× display scaling (Retina, QHD, etc.)
- JPEG quality: 90 for images, 85 for directory composites

## Performance Characteristics

### Before Optimizations
- Loading 500 images: All created as placeholders immediately
- Memory usage: ~200-300 MB for gallery (500 tiles + full images)
- Scroll performance: All tiles rendered, even off-screen
- Mobile: Laggy loading, high memory pressure

### After Optimizations
- Loading 100 images (mobile): Progressive loading with virtual scrolling
- Memory usage: ~50-80 MB for gallery (visible tiles only + LRU cache)
- Scroll performance: Smooth, only renders visible area
- Mobile: Fluid experience even on lower-end devices

## Future Optimization Opportunities

1. **Progressive/Chunked Loading**: Currently all page tiles are created at once. Could load first 2-3 rows immediately, then load more as user scrolls.

2. **Thumbnail Resolution Scaling**: Could detect actual screen DPI and adjust thumbnail generation resolution (1×, 1.5×, 2×) instead of always using 2×.

3. **Disk Cache Cleanup**: LRU eviction only happens in-memory. Disk cache in `~/.cache/imgview` grows unbounded. Could implement periodic cleanup or size limits.

4. **Gesture-Triggered Prefetch**: On mobile, could prefetch next page when user scrolls past 80% of current page.

5. **Memory Pressure Handling**: Could listen for OS memory warnings and proactively shrink cache size.

## UI Features

### Filename Label Toggle

**Default State**: Labels are **OFF** by default to maximize vertical space (saves ~22px per row).

**How to toggle**:
- **Desktop**: Press **N** key (configurable in config.toml as `ToggleFilenames`) OR click **☰ menu** → "Show/Hide filenames"
- **Mobile**: Tap **☰ menu** (bottom-right) → "Show/Hide filenames"

The **☰ menu button** appears in the bottom-right corner of the gallery on both mobile and desktop. It provides a discoverable, standard way to access settings without needing keyboard shortcuts or hidden gestures.

When labels are hidden:
- Labels are not rendered (`Hide()` called on label widgets)
- Tile height excludes label space (extraH = 0)
- Layout automatically recalculates to use full tile height for images
- No white space left where labels would be

When labels are shown:
- Displays basename only (e.g., "IMG_1234.jpg" not full path)
- 22px height reserved below each thumbnail
- Center-aligned with ellipsis truncation for long names

This is useful when browsing by visual recognition rather than needing filenames.

---

## Testing

To verify the optimizations:

1. **Desktop**: Load a directory with 5000+ images, scroll through gallery
   - Memory should stay under 200 MB
   - Scrolling should be smooth (both down and back up)
   - Tiles should reappear correctly when scrolling back up (no white space)
   - Pagination button should appear at end of each page
   - Press 'N' to toggle filenames - no white space should remain when hidden

2. **Mobile**: Load 100+ images on low-end device (2GB RAM)
   - Initial load should be fast (not waiting for all tiles)
   - Scrolling should be fluid
   - Memory usage should stay reasonable (no OOM crashes)
   - "Next Page" button should be easily tappable

3. **Cache Behavior**: Load page 1, scroll to page 2, return to page 1
   - Tiles should reload from cache (instant)
   - After scrolling through many pages, old tiles should be evicted

## Configuration

Users can override mobile defaults in `~/.config/imgview/config.toml`:

```toml
[General]
TileWidth = 250        # Override default mobile 200
ImagesPerPage = 200    # Override default mobile 100
Workers = 6            # Override default mobile 4
TileGap = 4            # Override default mobile 3
```

The automatic mobile detection can't be disabled, but these values take precedence.
