# Architecture Overview

This document provides a technical overview of the tie-gui architecture — the shared gallery library behind imgview and tie-view, and the design patterns used across the clients (tie-fm, tie-audio-player).

## Table of Contents

- [System Architecture](#system-architecture)
- [Gallery Library](#gallery-library)
- [Extension Mechanism](#extension-mechanism)
- [Platform Abstraction](#platform-abstraction)
- [Layout Engine](#layout-engine)
- [Threading Model](#threading-model)
- [Caching Strategy](#caching-strategy)
- [Mobile Optimizations](#mobile-optimizations)

---

## System Architecture

### High-Level Design

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Application Layer                             │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐   │
│  │  cmd/imgview     │  │  cmd/tie-view    │  │  cmd/tie-fm      │   │
│  │ ┌──────────────┐ │  │ ┌──────────────┐ │  │ ┌──────────────┐ │   │
│  │ │ File system  │ │  │ │ Tie client   │ │  │ │ Twin panels  │ │   │
│  │ │ Archive      │ │  │ │ Tag sidebar  │ │  │ │ Tag panel    │ │   │
│  │ │ reader       │ │  │ │ Image tagger │ │  │ │ fs providers │ │   │
│  │ └──────────────┘ │  │ └──────────────┘ │  │ └──────────────┘ │   │
│  └──────────────────┘  └──────────────────┘  └──────────────────┘   │
│  ┌──────────────────┐                                               │
│  │ cmd/tie-audio-   │  (cmd/tie-fm and cmd/tie-audio-player do not  │
│  │    player        │   use the gallery library; tie-fm shares the  │
│  └──────────────────┘   tagselection widget with tie-view)          │
└──────────────┬───────────────────────────┬──────────────────────────┘
               │   Extension API           │
               │   (CustomReader,          │
               │    Thumbnailer, etc.)     │
               ▼                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    Gallery Library                           │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │   Gallery    │  │  TileLayout  │  │  ImageView   │      │
│  │ (controller) │  │   (layout)   │  │   (widget)   │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  Platform    │  │  Config      │  │  Helpers     │      │
│  │ (abstraction)│  │ (settings)   │  │ (bootstrap)  │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
                        ┌─────────────┐
                        │    Fyne     │
                        │  Framework  │
                        │ (vendored   │
                        │    fork)    │
                        └─────────────┘
```

### Design Philosophy

**Separation of Concerns:**
- Gallery library provides rendering engine (layout, display, interaction)
- Applications provide content sources (filesystem, network, archives)
- Clean interface boundary enables independent development

**Extension via Composition:**
- Applications implement simple interfaces (CustomReader, Thumbnailer)
- No inheritance hierarchies or complex abstractions
- Callbacks for behavior customization

**Platform Agnostic Core:**
- Single codebase for desktop and mobile
- Platform-specific behavior isolated in Platform abstraction
- Runtime detection, no build tags in application code

---

## Gallery Library

### Gallery Controller (`gallery/gallery.go`)

The `Gallery` struct is the main controller coordinating the gallery view and single-image view.

**Responsibilities:**
- Manage image collection (`imageFiles []*ImageInfo`)
- Coordinate layout and rendering
- Handle keyboard/gesture events via hotkey system
- Manage navigation and pagination
- Control fullscreen state
- Provide extension points via callbacks

**Lifecycle:**
```go
// Two-step initialization
viewer := gallery.NewGallery(app, window, config, tileOnclick)

// Customize between creation and initialization
viewer.Sidebar = makeSidebar(...)         // tie-view only
viewer.Thumbnailer = makeThumbnailer()    // tie-view only
viewer.OnImageChange = func(info) { ... } // both

// Complete initialization (wires hotkeys, creates layout)
viewer.Init()

// Load content
viewer.ReadImageDir("/path/to/images", nil)  // or ReadCustom, ReadImageArchive

// Display
viewer.LoadGallery()
viewer.CreateView()
window.SetContent(viewer.Content)
```

**Why Two-Step Initialization?**
- Applications need to customize Gallery (set Sidebar, Thumbnailer, callbacks) between creation and initialization
- `Init()` wires hotkeys and layout which may depend on configuration being complete
- Alternative (single constructor with all options) would require giant parameter list or builder pattern

### TileLayout (`gallery/tilelayout.go`)

Implements the justified row layout algorithm and thumbnail loading.

**Layout Algorithm (O(n)):**
```
currentY = 0
for each row:
    accumulate tiles until (containerWidth - gaps) / sumAspects <= targetH
    rowH = (containerWidth - gaps) / sumAspects
    if last row: cap rowH at targetH
    for each tile in row:
        tileW = (tile.width / tile.height) * rowH
        place at (x, currentY), size (tileW, rowH)
        x += tileW + gap
    currentY += rowH + gap
```

**Key insight:** Row height is determined by the sum of aspect ratios of tiles in that row. This ensures all tiles in a row have the same height while maintaining their aspect ratios.

**Thumbnail Loading:**
- Placeholder tiles created immediately (loading.png, decoded once per page)
- ImageInfo instances sent to `imagesToLoad` channel
- Worker pool (default 8 goroutines) drains channel
- Each worker: calls GetThumbnail → NewImageTile → forwards to `results` channel
- A single `tileUpdater` goroutine batches write-backs into one relayout per
  flush (~120 ms trailing or 32 tiles) — never `grid.Refresh()`, which would
  delete and re-upload every visible tile texture on the next frame

**Caching:**
- `tileCache` — session-scoped in-memory LRU (500 tiles desktop, 150 mobile; mutex-protected)
- Cache key is CustomReader.Path() (hash for tie, filepath for local)
- Cache hits skip thumbnail decoding entirely

### ImageView (`gallery/imageview.go`)

Single-image display widget with zoom, pan, and rotation.

**Features:**
- Zoom to fit, zoom to original size
- Pan via drag (desktop) or momentum scroll (mobile)
- Rotation (90° increments)
- EXIF orientation handling
- Pinch-to-zoom (mobile)
- Filtering mode toggle (fastest vs smooth scaling)

**Mobile Optimizations:**
- GPU memory optimization: downscale high-res images to 2× screen dimensions
- Touch gesture handling (pinch, swipe, momentum)
- Separate drag handlers for mobile vs desktop

### ImageInfo (`gallery/imageinfo.go`)

Per-item data model holding metadata and content access.

**Fields:**
- `Path string` — display identifier
- `Width, Height int` — pre-known dimensions (0 = unknown)
- `CustomReader CustomReader` — content source
- `InputIsVideo bool` — marks video entries
- `InputIsDir bool` — marks directory entries (browsable)
- `OnOpen func()` — custom open handler (used by directories)

---

## Extension Mechanism

See [gallery/extension.go](../gallery/extension.go) for comprehensive documentation.

### CustomReader Interface

Core extension point for content sources:

```go
type CustomReader interface {
    GetReader() (io.ReadSeeker, error)
    Path() string  // Used for caching and identification
}
```

**Implementations:**
- `ImageInfo` (gallery) — local files (default)
- `tieReader` (cmd/tie-view) — tie network blobs
- `uriReader` (cmd/imgview) — Android content:// URIs
- `archiveReader` (gallery) — archive members

### Optional Behaviors

**Openable** — Custom open action (directories):
```go
type Openable interface {
    Open()
}
```

**VideoFile** — Mark as video entry:
```go
type VideoFile interface {
    IsVideo() bool
}
```

**VideoStreamer** — Direct HTTP streaming:
```go
type VideoStreamer interface {
    StreamURL() string
}
```

**DimensionProvider** — Pre-known dimensions:
```go
type DimensionProvider interface {
    Dimensions() (width, height int)
}
```

**PreviewProvider** — Browsable collection (tie dir/archive): supplies preview
readers for the tile thumbnail and swipe cycling; called lazily on loader
goroutines:
```go
type PreviewProvider interface {
    Previews() ([]CustomReader, error)
}
```

**CoverProvider** — Ready-made cover thumbnail for a collection tile without
enumerating the collection (e.g. server-cached tie archive cover); the gallery
stores a generated first preview via StoreCoverThumbnail on a miss:
```go
type CoverProvider interface {
    CoverThumbnail() (io.ReadSeeker, error)
    StoreCoverThumbnail(jpegBytes []byte)
}
```

### Thumbnailer Interface

Custom thumbnail generation:

```go
type Thumbnailer interface {
    GetThumbnail(info *ImageInfo) (io.ReadSeeker, error)
}
```

**Default behavior:** Local disk cache in ThumbnailDir
**tie-view behavior:** Fetch from tie metadata or generate and persist

### Callback Extension Points

**Gallery struct exposes callbacks:**
- `OnImageChange func(*ImageInfo)` — after image display
- `OnTapped func()` — single-click/tap on image
- `OnDoubleTapped func()` — double-click/tap
- `OnSwipeUp func()` — upward swipe (mobile)
- `OnTileSecondaryTapped func(*Tile)` — right-click on tile

---

## Platform Abstraction

### Platform Struct (`gallery/platform.go`)

Centralizes mobile vs desktop behavior differences.

**Detection:**
```go
platform := NewPlatform()  // Detects via fyne.CurrentDevice().IsMobile()
```

**Semantic Methods:**
- `ShouldFocusImageView()` — focus for keyboard nav (desktop only)
- `ShouldHandleHotkeysAtWindowLevel()` — window-level key routing (mobile)
- `ShouldRegisterBackButton()` — Android/iOS back button (mobile)
- `ShouldAutoFullscreen()` — auto-fullscreen on open (mobile)
- `ShouldExitFullscreenOnGalleryView()` — exit on grid return (mobile)
- `ShouldDownscaleImages()` — GPU memory optimization (mobile)
- `UsesMobileDragGestures()` — pinch-zoom, momentum (mobile)
- `ShouldUseTapForAction()` — prefer tap vs swipe (desktop)

**Benefits:**
- Single detection point (Gallery creation)
- Semantic method names express intent
- Easy to test (mock Platform with fixed behavior)
- No scattered IsMobile() checks (~10 call sites eliminated)

---

## Layout Engine

### Justified Row Algorithm

**Goal:** Fill each row edge-to-edge with no horizontal gaps while maintaining aspect ratios.

**Constraints:**
- Target row height: `TileWidth` config (default 300px)
- Minimum gap between tiles: `TileGap` config (default 5px)
- All tiles in a row must have same height

**Algorithm:**
1. Accumulate tiles until row would exceed target height
2. Calculate actual row height: `(containerWidth - gaps) / sumAspects`
3. Scale each tile in row to calculated height
4. Place tiles left-to-right with gaps
5. Advance currentY by row height + gap

**Why it works:**
- Width of tile at height H is `(tile.width / tile.height) × H`
- Sum of all tile widths must equal `containerWidth - gaps`
- Solving for H gives `(containerWidth - gaps) / sumAspects`

### Pagination

**Default:** 500 images per page
- Configurable via `ImagesPerPage` setting
- Pagination controls in bottom bar
- Lazy loading: only current page thumbnails are loaded
- Cache persists across page changes (session-scoped)

---

## Threading Model

### UI Thread Invariant

**Rule:** All widget mutations must run on UI goroutine via `fyne.Do()`

**Automatically on UI thread:**
- `TileLayout.Layout()` (called by Fyne layout system)
- All `fyne.WidgetRenderer` methods
- Event handlers (KeyPress, Tapped, etc.)

**Must use `fyne.Do()`:**
- Thumbnail workers updating tiles/grid
- Network callback handlers
- Timer/goroutine callbacks that mutate widgets

### Worker Pool

**Thumbnail loading:**
- Configurable worker count (default 8)
- Workers drain `imagesToLoad` channel
- Each worker: GetThumbnail → decode → create tile → send to `results` channel
- `tileUpdater` goroutine batches tile swaps into one `fyne.Do` + relayout
  per flush (~120 ms trailing or 32 tiles)

**Thread safety:**
- `imagesToLoad` channel coordinates workers
- `tileCache`: mutex-protected (accessed from loader workers)
- `layout.tiles` / `grid.Objects`: written only on the UI thread (inside `fyne.Do`)
- Network clients: must be safe for concurrent reads

### Network Calls

**Rule:** Never block UI thread with network I/O

**Pattern:**
```go
go func() {
    result := networkCall()
    fyne.Do(func() {
        updateUI(result)
    })
}()
```

---

## Caching Strategy

### Three-Level Cache

**1. Session in-memory cache** (`TileLayout.tileCache`)
- Holds decoded thumbnail tiles for the current session
- LRU eviction: 500 tiles desktop, 150 mobile
- Key: CustomReader.Path()
- Benefit: instant cache hits for navigation

**2. Local disk cache** (imgview)
- Location: ThumbnailDir config (default `~/.cache/imgview`)
- Structure: `<xx>/<yy>/<zz>/<64-char-hash>` (3-level hex prefix)
- Format: JPEG quality 90, 2× TileWidth
- Variants: folder-badged collection previews use a `<hash>d` suffix; video
  frames use a `v<sha256(path)>` key with `-N` per frame index
- Invalidated: manual cleanup only
- Benefit: fast startup on revisited directories

**3. Tie metadata cache** (tie-view)
- Storage: tie `(imageHash, "thumbnail", thumbHash)` relation
- Generated on first view, fetched on subsequent views
- Dimensions stored: `(imageHash, "dimensions", "WxH")`
- Archive covers use the same relation on the archive hash (CoverProvider):
  the tile's initial view fetches the small cover instead of downloading the
  whole archive blob
- Shared across all tie-view clients
- Benefit: network-cached thumbnails, no local storage

### Content Addressing

**Both local and tie use same hash:**
- Algorithm: HighwayHash with shared key
- File content → 64 hex char hash
- Portable across machines (same file = same hash)
- Enables tie thumbnail deduplication

---

## Mobile Optimizations

### GPU Memory Management

**Problem:** High-res phone photos (12MP+) are ~48MB RGBA. Re-uploading full texture on every pinch-zoom frame causes lag.

**Solution:** Downscale to 2× screen dimensions
- Longest edge capped at `2 × max(screenWidth, screenHeight)`
- Example: 1080p screen → 4320px max edge → ~8MP texture
- Leaves headroom for zoom while keeping texture manageable
- Desktop: no downscaling (GPUs can handle full resolution)

### Gesture Handling

**Pinch-to-zoom:**
- Track two simultaneous touch points
- Calculate distance change → zoom factor
- Apply incrementally during gesture

**Momentum scrolling:**
- Track touch velocity on drag end
- Decelerate over time with physics simulation
- Cancel on new touch

**Swipe navigation:**
- Accumulate horizontal drag distance
- Trigger next/prev when exceeding threshold
- Separate from pan (only at image edges)

### Focus Management

**Desktop:** Focus ImageView for keyboard navigation
**Mobile:** Skip focus (would summon soft keyboard)

**Consequence:** Mobile uses window-level KeyPress handler
- All hotkeys routed through Gallery.KeyPress
- ImageView.TypedKey never called on mobile
- Back button mapped to show-gallery action

### Fullscreen Behavior

**Mobile:** Auto-fullscreen on image open, exit on gallery return
**Desktop:** Manual fullscreen toggle (F key), persistent across images

---

## Performance Characteristics

### Complexity Analysis

**Layout:** O(n) single-pass
- Each tile examined once
- Row accumulation amortized constant
- No backtracking or recursion

**Thumbnail loading:** O(n) with parallelism
- Worker pool provides parallelism factor
- Wall-clock time: ~O(n/workers)
- Bottleneck: I/O (disk or network) not CPU

**Navigation:** O(1)
- Next/prev: array index increment
- Tile display: cache lookup
- No collection scanning

### Memory Usage

**Thumbnail cache:** ~2MB per 1000 tiles (600px thumbnails)
- Formula: `tileCount × tileWidth × tileHeight × 4 bytes/pixel × compression`
- Compression: ~0.1 (JPEG) → ~240KB per 100 tiles

**ImageView:** ~50MB for 12MP image (RGBA)
- Formula: `width × height × 4 bytes/pixel`
- Mobile downscaling reduces by ~4× (2× per dimension)

### Optimization Opportunities

**Future work:**
- Incremental layout (only re-layout visible rows)
- Virtual scrolling (only render viewport tiles)
- Progressive thumbnail loading (blur-up)
- Thumbnail pre-generation worker
- WebP thumbnail format (better compression)

---

## Error Handling

### Philosophy

**Gallery library:** Degrade gracefully
- Failed thumbnail → show placeholder (loading.png)
- Failed image decode → show placeholder
- Failed layout → use minimum size
- Never crash on bad content

**Applications:** Surface errors to user
- Network failures → error dialog (tie-view)
- File not found → error dialog (imgview)
- Tag sync failures → rollback UI + reconcile (tie-view)

### Reconciliation Pattern (tie-view)

**Optimistic updates with reconciliation:**
```go
// 1. Optimistically update UI
updateLocalState(newValue)

// 2. Persist to network in background
go func() {
    if err := network.Write(newValue); err != nil {
        // 3. On failure, fetch ground truth and reconcile
        groundTruth := network.Read()
        fyne.Do(func() {
            updateLocalState(groundTruth)
            showError(err)
        })
    }
}()
```

Used for: star toggles, tag add/remove

---

## Testing Strategy

**Unit tests:**
- Layout algorithm correctness
- CustomReader interface compliance
- Platform abstraction behavior

**Integration tests:**
- Two-step initialization pattern
- Thumbnail cache hit/miss
- Pagination boundaries

**Manual testing:**
- Visual layout verification (justified rows)
- Gesture handling (mobile emulator or device)
- Platform-specific behavior (desktop vs mobile)
- Large collection performance (1000+ images)

---

## Further Reading

- [Extension API Documentation](../gallery/extension.go)
- [Memory & Performance Optimizations](OPTIMIZATIONS.md)
- [CLAUDE.md](../CLAUDE.md) — Comprehensive codebase reference
