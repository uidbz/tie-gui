# imgview / tieview — LLM Reference

## Repository layout

| Path | What |
|------|------|
| `cmd/imgview/` | Local-filesystem image viewer entry point |
| `cmd/tieview/` | tie-network image viewer entry point |
| `gallery/` | Shared library: layout engine, tile widget, image view, config |
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

## Gallery layout (`gallery/tilelayout.go`)

### Justified row layout (current)

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

`PlaceTiles` creates placeholder tiles from `loading.png` for every slot on
the current page, then sends each `ImageInfo` to the `imagesToLoad` channel.
`Workers` (default 8) goroutines drain the channel, call `GetThumbnail` →
`NewImageTile`, and write the real tile back. The grid refreshes every 20
images or 500 ms after the last load.

`cachedTiles map[string]*Tile` holds in-memory tile cache keyed by path/hash.
Cache hits skip thumbnail decoding entirely.

### Thumbnail pipeline

**Local (`imgview`):** hash file content (HighwayHash) → check
`~/.cache/imgview/<xx>/<yy>/<zz>/<hash>` → on miss: decode + EXIF-rotate →
`ScaleImage(decoded, tileWidth*2)` → JPEG quality 90 → write cache.

**Remote (`tieview`):** `filehostThumbnailer.GetThumbnail` →
1. Check `tr.thumbHash` (pre-populated from query expand) → `GET filehost/<thumbHash>`
2. On miss: download full blob → decode → scale → encode → `PUT filehost/upload/<thumbHash>` → `Set(imageHash, "thumbnail", thumbHash)` → `Set(imageHash, "dimensions", "WxH")`

Thumbnail width is always `TileWidth * 2` (2× for HiDPI). Height is
aspect-preserved. Directory tiles produce a square 2×2 composite at
`TileWidth × TileWidth` px.

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
- `cachedTiles map[string]*Tile` — session-scoped in-memory thumbnail cache
- `imagesToLoad chan *ImageInfo` — work queue for loader goroutines

### `gallery.Tile` (`gallery/tilelayout.go`)
- `width, height float32` — thumbnail pixel dims (= original aspect ratio)
- `landscape bool` — `width > height` (kept for informational use; layout uses aspect ratio directly)
- `Content *canvas.Image` — the actual rendered image (`ScaleMode = Fastest`, `FillMode = Contain`)
- `Info *ImageInfo` — metadata and reader

### `gallery.ImageInfo` (`gallery/tilelayout.go`)
- `Width, Height int` — pre-stored original dimensions (0 = unknown, use thumbnail dims)
- `CustomReader CustomReader` — tie or archive reader
- `OnOpen func()` — if non-nil, replaces default image display (used for directories)
- `ThumbnailIsScaled bool` — set when thumbnail is already at tileWidth*2

### `gallery.Viewer` (`gallery/imageviewer.go`)
- `imageFiles []*ImageInfo` — full list for the current gallery source
- `currentPath string` — absolute path of the currently open directory
- `isFullscreen bool` — tracks fullscreen state; toggled by `ToggleFullscreen()`
- `Thumbnailer Thumbnailer` — if non-nil, used instead of local disk cache
- `OnImageChange func(*ImageInfo)` — called after `ChangeImage`

### Optional `CustomReader` interfaces (`gallery/imageviewer.go`)
| Interface | Method | Purpose |
|-----------|--------|---------|
| `Openable` | `Open()` | Directory entries: replaces tap with navigation |
| `VideoFile` | `IsVideo() bool` | Marks entry as video (shows placeholder, caller opens player) |
| `VideoStreamer` | `StreamURL() string` | Returns direct HTTP URL so libmpv streams without downloading |
| `DimensionProvider` | `Dimensions() (w, h int)` | Pre-known image dimensions for stable placeholder layout |

---

## Navigation and fullscreen

### Android back button
Handled in `Viewer.KeyPress` (window-level `SetOnTypedKey` handler). On mobile,
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

---

## tieview navigation model

Directory entries are `tieDirReader` instances implementing `Openable`. Tapping
one calls `browseDir(uid)` → `fsTree.showDirUID(uid, "")` — the virtual
filesystem sidebar tree navigates, not the gallery grid.

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
