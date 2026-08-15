# imgview / tieview — LLM Reference

## Repository layout

| Path | What |
|------|------|
| `cmd/imgview/` | Local-filesystem image viewer entry point |
| `cmd/tieview/` | tie-network image viewer entry point |
| `gallery/` | Shared library: layout engine, tile widget, image view, config |
| `gallery/gallery.go` | Gallery controller (renamed from imageviewer.go in Phase 1) |
| `gallery/imageview.go` | Single-image display widget |
| `gallery/imageinfo.go` | Per-item data model (extracted from tilelayout.go in Phase 1) |
| `gallery/helper.go` | File-type detection utilities (extracted from imageview.go in Phase 1) |
| `gallery/apphelper.go` | Shared app bootstrap helpers (Phase 4) |
| `gallery/videohelper.go` | Video window creation helpers (Phase 4) |
| `gallery/platform.go` | Mobile vs desktop platform abstraction (Phase 5) |
| `gallery/extension.go` | Extension interface documentation (Phase 6) |
| `tagselection/` | Tag-picker widget used by tieview sidebar |
| `tagselection/trie/` | 256-ary prefix trie backing tag search |
| `mpvplayer/` | libmpv video player window |
| `third_party/fyne/` | Vendored fork of the Fyne GUI framework (replace directive in go.mod) |

The tie module lives at `../tie` (sibling directory) and is referenced via a
`replace` directive in `go.mod`. It is **not** a read-only dependency — both
repos are under active development together.

---

## Build

```sh
go build ./cmd/imgview   # local viewer
go build ./cmd/tieview   # tie-backed viewer
go test ./...
```

Both binaries require CGo (Fyne depends on OpenGL / system graphics). The
`migrated_fynedo` build tag is implicit in the vendored Fyne fork.

---

## Memory Optimization

The gallery implements several optimizations for fluid performance on mobile and
lower-end devices:

### LRU Tile Cache (`gallery/tilecache.go`)
In-memory thumbnail cache with size limits (500 desktop, 150 mobile). Uses
insertion-order LRU eviction. Cache operations are mutex-protected.

### Off-screen culling (Fyne clip)
`TileLayout.Layout` positions **every** tile on the page each pass; it does not
Hide/Show tiles or track a visible-index set. Off-screen tiles cost nothing to
render because Fyne's GL painter culls any object outside the scroll
container's clip rect (`Paint()` early-returns in
`third_party/fyne/internal/painter/gl/painter.go`; the scroll registers as a
clip via `IsClip` in `internal/driver/util.go`).

**Do not** add a `viewer.scroll.OnScrolled` → `gallery.Refresh()` handler: a
`*fyne.Container.Refresh()` refreshes every child (re-uploading every tile
texture) and, fired per scroll frame, made scrolling choppy. This was the
"virtual scrolling" regression removed after commit `6a83e2f`.

**Do not** call `grid.Refresh()` or `scroll.Refresh()` on hot paths either —
both recursively refresh children and force re-upload of every visible tile
texture. Use `layout.relayoutGrid()` (direct `Layout` + `canvas.Refresh(grid)`)
to reposition tiles, and `scroll.ScrollToOffset(...)` for programmatic
scrolling (fast path, also used by the J/K scroll hotkeys).

### Texture cache lifetime (fork patch)
Off-screen tile textures expire after a cache lifetime and are re-uploaded
when scrolled back into view. The vendored fork makes the default
platform-dependent (`third_party/fyne/internal/cache/lifetime_desktop.go` =
**10 min**, `lifetime_mobile.go` = 1 min for mobile/wasm); the `FYNE_CACHE`
env var still overrides. Trade-off: more VRAM on desktop (~500 MB worst case
for a fully scrolled 500-tile page) in exchange for hitch-free scroll-back.

### Pagination Button
Large "Load Next Page ▼" button appended to `grid.Objects` after all tiles
when `currentPage < maxPages - 1`. Handled specially in `Layout()`: given
full width and fixed height (60px desktop, 80px mobile).

### Mobile Config Adjustments
`Config.AdjustForMobile()` reduces memory footprint on mobile platforms:
- TileWidth: 300 → 200
- ImagesPerPage: 500 → 100
- Workers: 8 → 4
- TileGap: 5 → 3

Applied automatically in both main programs after `LoadConfig` if
`platform.IsMobile()` is true.

### Image Cleanup
`showGallery()` releases full-size images (`fyneImage.Image = nil`) when
returning to the gallery view to free memory immediately.

---

## Gallery layout (`gallery/tilelayout.go`)

### Justified row layout

`TileLayout.Layout` implements a **justified row layout**: tiles are grouped
into rows and scaled so each row fills the full container width with no
horizontal gaps. All tiles in a row share the same height, determined by the
sum of their aspect ratios.

Algorithm (O(n)):

```
currentY = 0
for each row:
    accumulate tiles until (containerWidth - gaps) / sumAspects <= targetH
    rowH = (containerWidth - gaps) / sumAspects
    if last row: cap rowH at targetH
    for each tile in row:
        tileW = (tile.width / tile.height) * rowH
        place at (x, currentY), size (tileW, rowH + extraH)
        x += tileW + gap
    currentY += rowH + extraH + gap
```

Key config: `TileWidth` (default 300 px) is the **target row height**. Larger
values → fewer, taller rows. `TileGap` (default 5 px) is the inter-tile gap.
`extraH = labelHeight (22 px)` when filename labels are visible.

`tile.width` / `tile.height` are the thumbnail's pixel dimensions, which
preserve the original image's aspect ratio (thumbnails are width-scaled with
`imaging.Resize(w, 0, Lanczos)`). When `ImageInfo.Width / .Height` are
pre-populated (from tie metadata), the placeholder tile uses those instead,
giving the correct aspect ratio from the first layout pass.

### Placeholder tiles and lazy loading

`PlaceTiles` decodes `loading.png` **once** and shares it across placeholder
tiles for every slot on the current page, then sends each `ImageInfo` to the
`imagesToLoad` channel. `Workers` (default 8 desktop, 4 mobile) goroutines
drain the channel, call `GetThumbnail` → `NewImageTile`, and forward the real
tile to a single `tileUpdater` goroutine via the `results` channel. The
updater batches write-backs: one `fyne.Do` per flush (every ~120 ms trailing
or 32 tiles) swaps tiles into `layout.tiles`/`grid.Objects` and calls
`relayoutGrid()` — replacing the old every-20-images `grid.Refresh()` storm
that re-uploaded all textures. Stale cross-page results are dropped via an
`Info`-pointer guard. Decoded thumbnails are converted to `*image.RGBA` on
the worker so texture upload skips the painter's CPU pixel conversion.

When the current page is not the last page, `PlaceTiles` adds a large
"Load Next Page ▼" button as the final object in `grid.Objects`. This button
is 60px tall on desktop (80px on mobile) and styled with high importance for
easy tapping.

`tileCache *tileCache` holds an LRU in-memory tile cache keyed by path/hash.
Cache hits skip thumbnail decoding entirely. Cache size is limited to 500
tiles on desktop, 150 on mobile.

### Thumbnail pipeline

**Local (`imgview`):** hash file content (HighwayHash) → check
`~/.cache/imgview/<xx>/<yy>/<zz>/<hash>` → on miss: decode + EXIF-rotate
(JPEG only) → `ScaleImage(decoded, tileWidth*2)` (imaging **Linear** filter,
not Lanczos) → JPEG quality 90 → write cache.

**Remote (`tieview`):** `filehostThumbnailer.GetThumbnail` →
1. Check `tr.thumbHash` (pre-populated from query expand) → `GET filehost/<thumbHash>`
2. On miss: download full blob → decode → scale → encode → `PUT filehost/upload/<thumbHash>` → `Set(imageHash, "thumbnail", thumbHash)` → `Set(imageHash, "dimensions", "WxH")`

Thumbnail width is always `TileWidth * 2` (2× for HiDPI). Height is
aspect-preserved. Directory/archive tiles show the preview image at
`ImageInfo.previewIndex` with a semi-transparent folder icon overlaid
(`dirPreviewThumbnail`), disk-cached under `<hash>d` so the icon never leaks
into the same image's plain thumbnail.

---

## Image dimensions in tie metadata

When tieview generates a thumbnail it writes:
- `(imageHash, "thumbnail", thumbHash)` — content address of the thumbnail blob
- `(imageHash, "dimensions", "WxH")` — original image pixel dimensions, e.g. `"3840x2160"`

Both relations are fetched in the initial `Query(Expand: true)` call at zero
extra cost. `tieReader.dimensions` holds the raw string; `tieReader.Dimensions()`
parses it and implements `gallery.DimensionProvider`.

`gallery.ReadCustom` checks each `CustomReader` for `DimensionProvider` and
pre-populates `ImageInfo.Width` / `ImageInfo.Height`. `NewImageTile` uses
these when non-zero so placeholder tiles already have correct aspect ratios,
preventing layout reflow as thumbnails load.

---

## Key types

### `gallery.TileLayout` (`gallery/tilelayout.go`)
- Implements `fyne.Layout`
- `tiles []*Tile` — current page, indexed 0…pageSize-1
- `offset int` — index of first tile on this page within `viewer.imageFiles`
- `minHeight float32` — total pixel height of all rows; reported by `MinSize`
- `tileCache *tileCache` — session-scoped LRU in-memory thumbnail cache
- `imagesToLoad chan *ImageInfo` — work queue for loader goroutines

### `gallery.Tile` (`gallery/tilelayout.go`)
- `width, height float32` — thumbnail pixel dims (= original aspect ratio)
- `landscape bool` — `width > height` (kept for informational use; layout uses aspect ratio directly)
- `Content *canvas.Image` — the actual rendered image (`ScaleMode = Fastest`, `FillMode = Contain`)
- `Info *ImageInfo` — metadata and reader

### `gallery.ImageInfo` (`gallery/imageinfo.go`)
- `Width, Height int` — pre-stored original dimensions (0 = unknown, use thumbnail dims)
- `CustomReader CustomReader` — tie or archive reader
- `OnOpen func()` — if non-nil, replaces default image display (used for directories)
- `ThumbnailIsScaled bool` — set when thumbnail is already at tileWidth*2

### `gallery.Gallery` (`gallery/gallery.go`)
- Renamed from `Viewer` in Phase 1 refactoring
- `imageFiles []*ImageInfo` — full list for the current gallery source
- `currentPath string` — absolute path of the currently open directory
- `isFullscreen bool` — tracks fullscreen state; toggled by `ToggleFullscreen()`
- `Thumbnailer Thumbnailer` — if non-nil, used instead of local disk cache
- `OnImageChange func(*ImageInfo)` — called after `ChangeImage`
- `Platform() *Platform` — accessor for mobile vs desktop behavior (Phase 5)

### Optional `CustomReader` interfaces (`gallery/gallery.go`)
| Interface | Method | Purpose |
|-----------|--------|---------|
| `Openable` | `Open()` | Directory entries: replaces tap with navigation |
| `VideoFile` | `IsVideo() bool` | Marks entry as video (shows placeholder, caller opens player) |
| `VideoStreamer` | `StreamURL() string` | Returns direct HTTP URL so libmpv streams without downloading |
| `DimensionProvider` | `Dimensions() (w, h int)` | Pre-known image dimensions for stable placeholder layout |
| `DisplayNamer` | `DisplayName() string` | Human-readable name for thumbnail label (fallback: filepath.Base(Path())) |
| `PreviewProvider` | `Previews() ([]CustomReader, error)` | Browsable collection (tie dir/archive): supplies preview readers for the tile thumbnail + swipe cycling; called lazily on loader goroutines |
| `CoverProvider` | `CoverThumbnail()` + `StoreCoverThumbnail([]byte)` | Server-cached cover for a collection tile (tie archive): serves previewIndex 0 without enumerating the collection; the gallery stores a generated first preview as the cover on a miss |

---

## Navigation and fullscreen

### Android back button
Handled in `Gallery.KeyPress` (window-level `SetOnTypedKey` handler). On mobile,
`Hotkey{"Back", showGallery}` is registered during `InitHotkeys`. `TypedKey`
on `ImageView` is intentionally not wired on mobile (suppresses soft keyboard).

### Fullscreen on mobile
`ChangeImage` automatically calls `window.SetFullScreen(true)` on mobile when
opening a single image. `showGallery` calls `window.SetFullScreen(false)` when
returning to the gallery grid. Both are guarded by `viewer.isFullscreen` to
avoid redundant calls.

### Directory navigation (imgview)
`ShowImageDir(path)` wipes `imageFiles`, resets pagination, calls `ReadImageDir`,
`LoadGallery`, then `window.SetContent`. **No history stack exists.** The only
back mechanism is `PathLevelUp` (hotkey), which calls
`ShowImageDir(filepath.Dir(currentPath))`. The Android back button returns to
the gallery view but does not pop a directory stack.

### Directory/archive/video tiles: preview swipe
Directory and archive entries carry `ImageInfo.PreviewPaths` (all images in
the folder / all image members in the archive); tie directories and archive
blobs instead resolve `ImageInfo.PreviewReaders` lazily via the
`PreviewProvider` interface (resolved inside `dirPreviewThumbnail` on a
loader goroutine — except that a `CoverProvider` entry's initial view
(previewIndex 0) prefers its server-cached cover, skipping enumeration
entirely).
Video tiles offer up to `videoPreviewFrames` (10) frame thumbnails spread
across the duration by seek percent (`mpvplayer.ExtractFramePercent`), cached
per frame index (`videoThumbnailCachePath` `-N` suffix; frame 0 keeps the
historic suffix-free name). The tile displays the preview at
`ImageInfo.previewIndex` (folder tiles get a folder-icon badge, video tiles
a play icon). A transparent `dirSwipeOverlay` widget covers only tiles whose
`ImageInfo.HasPreviews()` is true (regular image tiles have no overlay, so
the scroller keeps native drag/fling there): taps forward to the tile,
horizontal drags call `Tile.cyclePreview(±1)` (swipe left = next, right =
previous, wraps around), vertical drags forward to `scroll.ScrollToOffset` so
page scrolling still works when a gesture starts on an overlaid tile.
`cyclePreview` regenerates the thumbnail off-thread and swaps `Content.Image`
in place, invalidating only that tile's texture.

### Gallery hotkeys (default bindings)
- **N**: Toggle filename labels on/off (ToggleFilenames) — **desktop only**
- **J** / **Down**: Scroll down
- **K** / **Up**: Scroll up
- **Backspace**: Navigate to parent directory (PathLevelUp)
- **Q** / **Escape**: Quit (gallery view) or return to gallery (image view)

All hotkeys are configurable in `config.toml` under `[Gallery]` section.

**Routing:** gallery-grid hotkeys (scroll, PathLevelUp, ToggleFilenames) are
registered in `TileLayout.InitHotkeys` — `Gallery.KeyPress` (window-level
`SetOnTypedKey`) dispatches `layout.hotkeys` unconditionally, which is
required because no widget is focused in the gallery grid on desktop.
`viewer.hotkeys` (image-view actions) reach the desktop via the focused
`ImageView.TypedKey` and mobile via window-level dispatch
(`ShouldHandleHotkeysAtWindowLevel`). Registering grid hotkeys on
`viewer.hotkeys` leaves them dead on the desktop gallery view.

### Gallery UI controls
- **☰ Menu button** (bottom-right): Opens popup menu with options
  - Show/Hide filenames
  - (Future: more options)
- **◀/▶ Toggle** (bottom-left, tieview only): Show/hide tag sidebar
- **Pagination** (bottom-center): Page navigation links and "Load Next Page" button at end of gallery

**Default**: Filename labels are OFF by default (saves ~22px vertical space per row). Use ☰ menu → "Show filenames" to enable.

---

## tieview navigation model

Directory entries are `tieDirReader` instances implementing `Openable`. Tapping
one calls `browseDir(uid)` → `fsTree.showDirUID(uid, "")` — the virtual
filesystem sidebar tree navigates, not the gallery grid. Both `tieDirReader`
and `tieArchiveReader` also implement `PreviewProvider`, so their tiles show
a content thumbnail (first image inside, folder-badged) and support swipe
cycling; the static `folderIcon` in `cmd/tieview/folder.go` remains only as
the fallback for empty or unreadable collections.

`tieArchiveReader` additionally implements `CoverProvider`: the tile's
initial view uses the server-cached cover (tie relation `(archiveHash,
"thumbnail", thumbHash)`, riding along with the query's expanded attributes
at zero extra cost) instead of downloading the whole archive blob. On a cover
miss the first member is extracted once (one full download) and uploaded as
the cover via `StoreCoverThumbnail`, so the slow path runs once ever per
archive and is shared by every machine. Full-archive downloads (cover misses
and swipe cycling) are capped at 2 concurrent via `archiveFetchSem` in
`cmd/tieview/tie.go` — each blob is held in memory for swipe cycling.

Tag-based navigation uses `readFromTie(viewer, tc, include, exclude, "tag", browseDir)`.
The query uses `Expand: true` so thumbnail hashes and image dimensions arrive
inline with no extra round-trips.

---

## Tag sidebar (`cmd/tieview/main.go` — `makeTagSidebar`)

**Initial load:** `tc.Get("tags")` returns two relations:
- `"all"` — every tag ever applied in the collection
- `"favorite"` — curated quick-pick tags (falls back to `"all"` if empty)

**On selection change:** `CoTagsForQueryExcludingInput(include, exclude, "")` is
called in a goroutine. The result replaces both the search trie and the
quick-pick list. Clearing the selection restores the original full lists.
`SetFavorites([]string)` is used (not `ClearFavorites` + `AddFavorite` loop)
to avoid a double-refresh bug: `ClearFavorites` triggers `Refresh` with 0 items,
then adding items without another `Refresh` leaves the list visually blank.

**Profile switch:** `reloadTags()` (returned from `makeTagSidebar`) clears
selected tags, clears the trie, clears favorites, then re-runs the
`tc.Get("tags")` fetch. Called as the `onApply` callback from `makeSettingsTab`.

`makeTagSidebar` also receives a `*imageTagger`. Whenever the tag list is
(re-)fetched, `tagger.SetAllTags(allTags)` is called so the image tagger's
search trie stays in sync with the sidebar's list without a second network request.

---

## Settings profiles (`cmd/tieview/settings.go`)

Profiles are stored as a JSON array under the `"tie.profiles"` Preferences key.
Active profile name under `"tie.activeProfile"`. Legacy flat keys
(`"tie.webservice"`, etc.) are still read at startup when no profiles exist yet.

```go
type profile struct {
    Name             string `json:"name"`
    Webservice       string `json:"webservice"`
    Namespace        string `json:"namespace"`
    Collection       string `json:"collection"`
    FilehostName     string `json:"filehostName"`
    FilehostURL      string `json:"filehostURL"`
    FilehostInsecure bool   `json:"filehostInsecure"`
}
```

**Apply** writes the profile, calls `applyProfileToConfig(&tc.Config)`, then
`*tc = *client.NewTieClient(tc.Config)`. The struct-overwrite pattern
(`*tc = *newTc`) propagates the rebuilt `ws.Client` (which has private fields
baked at construction) to all existing `*TieClient` pointers without any tie
module changes. `onApply()` is then called (→ `reloadTags`).

---

## `tagselection.TagSelection` API (`tagselection/tagselection.go`)

| Method | What |
|--------|------|
| `AddTag(tag)` | Insert into search trie (lowercased; original case in `caseMap`) |
| `ClearAllTags()` | Reset trie and caseMap |
| `AddFavorite(tag)` | Append to quick-pick list (no Refresh — use `SetFavorites` for updates) |
| `SetFavorites([]string)` | Replace quick-pick list and call `Refresh` once |
| `ClearFavorites()` | Empty quick-pick list and Refresh |
| `ClearSelected()` | Empty selected-tag list and Refresh |
| `AddSelected(*TagItemData)` | Move tag to selected set; fires `OnSelectedChanged` |
| `SelectedTags() ([]string, []string)` | Returns (included, excluded) tag slices |
| `SetListLabel(string)` | Change the bold label above the quick-pick list |
| `SetFavoriteMaxRows(n)` | Cap visible rows in the quick-pick list (0 = uncapped) |
| `SetSelectedMaxRows(n)` | Cap visible rows in the selected-tag list (0 = uncapped) |
| `SetStarred([]string)` | Replace the starred-tag set and refresh the quick-pick list |
| `OnSelectedChanged func()` | Callback fired on any selection change |
| `OnNewTag func(tag string)` | Called when user presses Enter with typed text but no row highlighted; nil in sidebar, set by image tagger |
| `OnStar func(tag string, starred bool)` | Called when user clicks ☆/★ on a quick-pick item; nil in sidebar, set by image tagger |
| `ShowStars bool` | When true, quick-pick items show a ☆/★ toggle button; must be set before first render |

**Critical:** `ClearFavorites()` calls `Refresh`. If you then call `AddFavorite`
in a loop without a final `Refresh`, the list stays visually blank. Always use
`SetFavorites` when replacing the list contents.

---

## Image tagger (`cmd/tieview/imagetagger.go`)

`imageTagger` is a floating panel that overlays the single-image view and
lets the user add and remove tags for the displayed image.

**Interaction:** A single tap on the image calls `viewer.OnTapped`, which
calls `tagger.Toggle(hash)`. The panel slides in from the bottom; a second
tap closes it. Navigating to a different image while the panel is open
resets it to the new image's tags automatically.

**Panel contents:** a standard `TagSelection` widget reused with different
semantics from the sidebar:
- The **selected** list = tags currently applied to the image in tie.
  Clicking a tag removes it.
- The **favorites quick-pick list** = all known tags, each with a ☆/★
  toggle button. Starred tags (those in the tie `("tags","favorite")`
  relation) show ★; clicking toggles the star and persists the change to
  tie. Clicking the tag itself (not the star) adds it to the image.
- The **search box + dropdown** = full-text search across all known tags.
  Search-result items also show ☆/★ buttons. Clicking a result (not the
  star) adds it to the image. Typing a name that does not exist and
  pressing Enter creates the tag via `OnNewTag` (see below).
- The include/exclude checkbox has no meaning in this context (it is
  present because the widget is shared); toggling it fires
  `OnSelectedChanged` but the diff against `appliedTags` will be empty,
  so no tie write occurs.

**Persistence:** `syncTags(newTags)` diffs `newTags` against the
`appliedTags` snapshot and calls `tc.Add` / `tc.Delete` in a goroutine.
Newly added tags are also registered via `tc.Add("tags","all",tag)` so
they appear in the sidebar and future tagger sessions.

**Layout wiring:** `viewer.OnImageChange` appends a persistent
`taggerOverlay = container.NewBorder(nil, tagger.Panel, nil, nil)` to
`viewer.Content.Objects` each time a single image is displayed.
`tagger.Panel` starts hidden; showing/hiding it via `Show()`/`Hide()`
controls visibility without rebuilding the content stack.

```
viewer.Content (Stack)
  ├── viewer.CurrentImage      ← ImageLayout container with ImageView
  └── taggerOverlay            ← Border container (transparent when panel hidden)
        └── tagger.Panel       ← Stack(bg rect, padded VBox(header, TagSelection))
```

**Tag list sync:** `makeTagSidebar` calls `tagger.SetAllTags(allTags)` after
every `tc.Get("tags")` fetch (startup and profile switch), keeping the
tagger's search trie up to date without a separate network request.

**Key methods:**

| Method | What |
|--------|------|
| `newImageTagger(window, tc)` | Create; Panel is hidden; call `SetAllTags` to populate trie |
| `SetAllTags([]string)` | Replace search trie + favorites list |
| `SetFavoriteTags([]string)` | Replace the starred-tag set and refresh the ☆/★ buttons |
| `Toggle(hash)` | Open panel for hash, or close if already open for that hash |
| `ShowForImage(hash)` | Open panel; fetches current tags from tie if hash changed |
| `HidePanel()` | Hide panel without clearing state; fires `OnHide` |
| `SetCurrentHash(hash)` | Track current image hash; if panel is open, switches it |
| `OnHide func()` | Called after the panel hides; used to restore keyboard focus on desktop |

---

## `tieReader` (`cmd/tieview/tie.go`)

```go
type tieReader struct {
    seeker     io.ReadSeeker
    data       []byte
    host       client.FileHost
    client     *http.Client
    hash       string       // content address (HighwayHash, 64 hex chars)
    thumbHash  string       // content address of cached thumbnail
    dimensions string       // "WxH" from tie metadata, e.g. "3840x2160"
    isVideo    bool
}
```

Implements: `CustomReader`, `VideoFile`, `VideoStreamer`, `DimensionProvider`.

`Dimensions()` parses `dimensions` via `parseDimensions(s)` → `strings.SplitN(s, "x", 2)`.

---

## Content addressing

Both local thumbnails and tie blobs use the same HighwayHash key
(`galleryKey` in `gallery/hash.go` = `tieKey` in `tie/client/tie.go`). This
means a file's content address is identical whether computed locally or by the
tie daemon, and thumbnail caches are portable across machines.

Local thumbnail cache path: `ThumbnailDir/<xx>/<yy>/<zz>/<64-char-hash>`
(3 levels × 2 hex chars, then full hash as filename).

---

## Config (`gallery/config.go`, `gallery/config.toml`)

| Key | Default | Effect |
|-----|---------|--------|
| `TileWidth` | 300 | Target row height in the justified layout; thumbnail width = `TileWidth * 2` |
| `TileGap` | 5 | Pixel gap between tiles |
| `Workers` | 8 | Concurrent thumbnail loader goroutines |
| `ImagesPerPage` | 500 | Pagination page size |
| `ThumbnailDir` | `~/.cache/imgview` | Local thumbnail disk cache root |

Config file locations: `~/.config/imgview/config.toml` (Linux),
`%AppData%\imgview\config.toml` (Windows).

---

## Threading model

- All widget mutations must run on the UI goroutine via `fyne.Do(func(){...})`.
- `TileLayout.Layout` and all `fyne.WidgetRenderer` methods are called on the
  UI goroutine automatically.
- `imageLoader` goroutines write to `layout.tiles[idx]` and `layout.grid.Objects[idx]`
  via `fyne.Do`; the grid `Refresh` is also dispatched via `fyne.Do`.
- `makeTagSidebar` closure variables (`allTags`, `allFavorites`, `allFavoritesLabel`)
  are only read/written inside `fyne.Do` blocks, so no mutex is needed.
- Network calls (`tc.Get`, `tc.Query`, `CoTagsForQueryExcludingInput`,
  `getlib.ReadFile`) must run in goroutines, never on the UI goroutine.

---

## Shared app helpers (`gallery/apphelper.go`, Phase 4)

Phase 4 refactoring factored common bootstrap code from both mains into shared
helpers:

- **`NewApp(appID, windowTitle, iconData)`** — creates Fyne app with ID, icon,
  and window. Eliminates copy-pasted 3-line bootstrap.
- **`ConfigFlag(helpText)`** — defines `-config`/`-c` flags and returns pointer.
  Caller controls when `flag.Parse()` is called.
- **`NormalizeConfigPath(path)`** — appends `.toml` suffix if needed.
- **`FocusImageViewOnDesktop(window, viewer)`** — focuses image view on desktop,
  skips on mobile (soft keyboard avoidance). Used in `OnImageChange` handlers.
- **`OpenVideoWindow(app, player, displayPath, onClose)`** — creates video
  player window with standard layout (800x520, close intercept, optional
  cleanup). Used by both mains for consistent video window behavior.

## Platform abstraction (`gallery/platform.go`, Phase 5)

Phase 5 refactoring centralized ~10 scattered `IsMobile()` checks into a single
platform abstraction. The `Platform` struct wraps device detection and provides
semantic methods for platform-specific behavior:

**Focus & keyboard routing:**
- `ShouldFocusImageView()` — focus image view for keyboard nav (desktop only)
- `ShouldHandleHotkeysAtWindowLevel()` — route keys to window handler (mobile)
- `ShouldRegisterBackButton()` — register Android/iOS Back key (mobile)

**Fullscreen management:**
- `ShouldAutoFullscreen()` — auto-fullscreen on image open (mobile)
- `ShouldExitFullscreenOnGalleryView()` — exit fullscreen on gallery view (mobile)

**GPU & gesture optimization:**
- `ShouldDownscaleImages()` — downscale for GPU memory (mobile)
- `UsesMobileDragGestures()` — pinch-zoom, momentum scroll (mobile)
- `ShouldUseTapForAction()` — prefer tap over swipe (desktop)

Platform detection happens once at `Gallery` creation via `NewPlatform()`. All
platform-specific logic routes through `viewer.Platform()` accessor. This is a
**runtime seam** (unified codebase, no build tags), not compile-time.

## Extension API (`gallery/extension.go`, Phase 6)

Phase 6 added comprehensive documentation of the stable extension contract
between the gallery library and applications. See `gallery/extension.go` for
full documentation of:

- Core interface: `CustomReader`
- Optional behaviors: `Openable`, `VideoFile`, `VideoStreamer`, `DimensionProvider`
- Thumbnailing: `Thumbnailer`
- Callbacks: `OnImageChange`, `OnTapped`, `OnSwipeUp`, etc.

The Gallery struct is now organized with extension points clearly separated from
internal wiring. See struct definition in `gallery/gallery.go` for the three
sections: Extension API (Callbacks), Extension API (Public Fields), and
Internal Wiring.

---

## Common patterns

**Replacing a tie client's connection without changing the pointer:**
```go
// ws.Client bakes URL and TLS at construction; mutating tc.Config.Webservice
// alone has no effect. Overwrite the struct in place instead:
*tc = *client.NewTieClient(tc.Config)
// All existing *TieClient pointers now see the new ws.Client.
```

**Pre-populating ImageInfo dimensions from a CustomReader:**
```go
// In ReadCustom — already wired:
if dp, ok := r.(DimensionProvider); ok {
    info.Width, info.Height = dp.Dimensions()
}
// In NewImageTile — already wired:
if context.Width > 0 && context.Height > 0 {
    t.width, t.height = float32(context.Width), float32(context.Height)
}
```

**Updating a fyne List atomically (single Refresh):**
```go
// Wrong — list stays blank after ClearFavorites triggers Refresh:
ts.ClearFavorites()
for _, tag := range tags { ts.AddFavorite(tag) }   // no Refresh here!

// Correct — SetFavorites does one Refresh at the end:
ts.SetFavorites(tags)
```
