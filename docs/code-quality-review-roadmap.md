# imgview / tieview — Code-Quality Review & Roadmap

## Context

The project is two Fyne GUI binaries — `imgview` (local filesystem viewer) and
`tieview` (tie-network content client) — sharing a `gallery/` rendering
library, with a vendored Fyne fork. It works, but has grown organically: the
naming is inconsistent, responsibilities are tangled across large files, the
tagging subsystem carries duplicated state and swallowed errors, and there is
no stated product direction. This document is a **review + roadmap only** — no
code changes are made here. It records the problems, the agreed direction, and
a phased way forward so future sessions can execute with confidence.

### Agreed direction (from user)

1. **Product:** Two apps, one shared library. Keep both binaries; harden
   `gallery/` into a clean documented library; factor duplicated bootstrap into
   a shared helper. Each `cmd` stays a thin driver.
2. **Platform:** Keep a unified codebase but isolate the Android-vs-desktop
   branches behind a small platform seam instead of scattered `IsMobile()`
   checks.
3. **Naming:** Rename the gallery controller `Viewer` → `Gallery` (in
   `gallery.go`); keep the single-image widget `ImageView` (in `imageview.go`);
   move `ImageInfo` out of `tilelayout.go` into `imageinfo.go`.

---

## Part 1 — Critical review (findings)

### A. Naming (the headline issue)

- **File/type name collision.** `imageview.go` holds `ImageView` (one image);
  `imageviewer.go` holds `Viewer` (the whole gallery controller). The filenames
  differ by one letter but the types are unrelated, and neither file's primary
  type matches its filename. `Viewer` also owns `CurrentImageView *ImageView`,
  so "viewer" means both the gallery and the thing that holds the image viewer.
- **Two names for one container.** `Gallery.gallery` (`*fyne.Container`) and
  `TileLayout.grid` are the same object (`viewer.layout.grid = viewer.gallery`).
  Methods mix "Gallery" (`LoadGallery`, `ChangeGallery`, `ShowImageDir`) with
  "grid"/"Tiles" (`PlaceTiles`).
- **Model in the wrong file.** `ImageInfo`, the per-item data model, lives in
  `tilelayout.go`, not with the view code that consumes it.
- **`context` shadowing.** `GetThumbnail(context *ImageInfo)`,
  `videoThumbnail(context *ImageInfo)`, `NewImageTile(..., context *ImageInfo)`
  name an `*ImageInfo` parameter `context`, colliding with the `context`
  package used elsewhere. Rename to `info`.
- **Receiver inconsistency.** `ImageView` uses `iv`, but `RegisterKey` uses `e`,
  `CreateRenderer` uses `r`; the renderer mixes `r` and `ren`. `NewViewer` names
  its return `iv` (same token used for `ImageView`), so `iv` means two types.
- **Vocabulary drift.** "image"/"thumbnail"/"tile" are used interchangeably in
  the loading path (`imagesToLoad chan *ImageInfo` → `imageLoader` → produces
  tiles). `RunCmdA` vs config `CmdA`/`RunCmda` capitalization mismatch.

### B. Tagging subsystem (needs the most work)

- **One widget, two incompatible roles.** `TagSelection` serves both the
  sidebar filter (include/exclude checkbox meaningful) and the image tagger
  (checkbox meaningless — its toggle fires `OnSelectedChanged` but diffs to a
  no-op). The dead control is a design smell (`tagselection.go:328-343`,
  `imagetagger.go:69-79`).
- **Triplicated starred-tag state.** The "starred" set exists in three places:
  `imageTagger.favoriteTags map[string]bool`, `TagSelection.starredSet`, and
  the tie `("tags","favorite")` relation. Every toggle does map→slice→map
  round-trips (`imagetagger.go:99-116,159-168`, `tagselection.go:620-624`).
- **Redundant double refresh.** `SetFavorites` and `SetStarred` each call
  `favoriteList.Refresh()`; callers always call both back-to-back
  (`imagetagger.go:103-104,142-143,155-156`). Should be one call.
- **Non-deterministic ordering.** `starredList()` iterates a map, so the
  quick-pick list reorders randomly on each star toggle (`imagetagger.go:160`).
- **Swallowed errors + optimistic state divergence.** `syncTags` and `OnStar`
  only `fmt.Println` on failure and never reconcile local state with tie, so a
  failed write leaves the UI showing a tag that was never persisted
  (`imagetagger.go:105-116,275-292`).
- **Vocabulary collision.** `favorite`, `star`, and `quick-pick` are used for
  overlapping-but-different concepts. In the sidebar the `favorite` field holds
  *co-tags / related tags*, not favorites; in the tagger it holds *starred*
  tags. One field name, two meanings.
- **Trie rough edges.** `maxResults` is a package global; the whole trie is
  rebuilt on every sidebar selection change; `caseMap` silently loses
  case-collisions; a stale `radix` comment.

### C. Tangled responsibilities

- **`ImageView` does I/O and format sniffing.** The display widget owns
  `GetReader` (opens files/archives/custom readers) and `LoadImage`, and hosts
  `IsImage`/`IsImageFromPath`/`IsVideoFromPath`/`IsArchiveFromPath`. These
  belong in a loader/helper, not the widget.
- **Layout triggers window-chrome side effects.** `ImageLayout.Layout` mutates
  `ImageView` state then calls `iv.changeFn()`, which sets the window title.
  Layout, view state, and window chrome are entangled.
- **`TileLayout` is a god-object.** The same type computes justified rows AND
  runs the thumbnail worker pool, reads/writes the disk cache, shells to libmpv
  for video frames, draws a play icon pixel-by-pixel, and hashes content
  (`tilelayout.go:337-645`). Layout math should not own I/O.
- **Bidirectional coupling.** `Gallery` holds `layout`; `layout` holds `viewer`
  and mutates its private fields (`viewer.scroll.Offset`, calls
  `viewer.ShowImageDir`). Break the back-reference with callbacks/interfaces.
- **Package-global drag state.** `region.go:166-167` declares package-level
  `startPos`/`dragStart` used by `Region.Dragged`/`DragEnd`, shadowing
  identically-named `ImageView` fields and making `Region` non-reentrant.
- **Debug leftovers.** `fmt.Println` used for user-facing errors and flow
  tracing; several large commented-out blocks remain.

### D. Duplication between the two mains

- Copy-pasted app bootstrap (both hardcode app ID `"sr.ht.uid.imgview"` and
  window title `"imgview"` — tieview isn't even distinctly identified).
- Duplicated `-config`/`-c` `.toml`-suffix flag logic.
- The mobile-focus-guard `OnImageChange` block (and its comment) is duplicated
  verbatim.
- Video-open window logic (create window, intercept close, resize 800x520)
  exists in both mains with overlapping structure.

### E. Platform split

- All platform branching is runtime `fyne.CurrentDevice().IsMobile()` (~10
  sites across both mains and `gallery/`), plus compile-time build tags only in
  `mpvplayer/`. No `runtime.GOOS`, no `_android.go`/`_desktop.go` in project
  code. The mix is a clarity/maintenance smell, not fragmentation — but the
  single-image widget knowing phone gesture semantics and GPU texture sizing is
  the sharpest example of the tangle.

---

## Part 2 — Roadmap (phased, ordered by leverage)

Each phase is independently shippable and leaves the tree green
(`go build ./cmd/imgview && go build ./cmd/tieview && go test ./...`).

### Phase 1 — Naming & file structure (mechanical, high clarity)

- Rename type `Viewer` → `Gallery`; rename `imageviewer.go` → `gallery.go`.
  Update receiver `viewer` → `g` (or keep `viewer`; pick one, apply everywhere).
- Keep `ImageView` and `imageview.go` as-is.
- Move `ImageInfo` (+ `NewImageInfo`, `NewImageInfoCustomReader`) out of
  `tilelayout.go` into a new `imageinfo.go`.
- Move the free functions `IsImage`/`IsImageFromPath`/`IsVideoFromPath`/
  `IsArchiveFromPath` out of `imageview.go` into `helper.go`.
- Rename `context *ImageInfo` params → `info` throughout `tilelayout.go`.
- Normalize receiver names and fix `RunCmdA`/`CmdA`/`RunCmda` capitalization.
- Collapse the `gallery`/`grid` dual naming to one term.
- Critical files: `gallery/imageviewer.go`, `gallery/tilelayout.go`,
  `gallery/imageview.go`, `gallery/helper.go`. Callers in both `cmd/` mains
  reference `gallery.NewViewer` / the `Viewer` type — update those too.

### Phase 2 — Tagging refactor (the flagged priority)

- **Single owner for starred state.** Make tie the source of truth; keep one
  in-memory cache (`TagSelection.starredSet`) and delete
  `imageTagger.favoriteTags`. Expose add/remove that update the set and refresh
  once.
- **Split the two roles.** Give `TagSelection` an explicit mode (filter vs
  tagger) or two thin wrappers, so the include/exclude checkbox only exists in
  filter mode. Removes the dead control and the no-op `OnSelectedChanged` path.
- **One refresh call.** Merge `SetFavorites`+`SetStarred` into a single method.
- **Deterministic order.** Sort `starredList()` output.
- **Real error handling.** On `tc.Add`/`tc.Delete` failure in `syncTags`/
  `OnStar`, surface to the user and roll back the optimistic local state.
- **Vocabulary.** Pick one term (recommend: "starred" for the user-curated set,
  "related" for the sidebar's co-tag list) and rename the misleading `favorite`
  field accordingly. Update CLAUDE.md's TagSelection API table to match.
- **Trie cleanup.** Make `maxResults` non-global; stop rebuilding the whole
  trie on every selection change; handle `caseMap` collisions; fix stale
  comments.
- Critical files: `tagselection/tagselection.go`, `tagselection/trie/trie.go`,
  `cmd/tieview/imagetagger.go`, `cmd/tieview/main.go` (`makeTagSidebar`).

### Phase 3 — Decouple gallery internals

- Split `TileLayout` into: (a) pure layout math (justified rows), and (b) a
  `thumbnailLoader` owning the worker pool + disk cache + video-frame
  extraction + play-icon drawing.
- Remove the `layout → viewer` back-reference: pass the few needed hooks
  (`onNavigate`, scroll access) as callbacks/an interface.
- Move `ImageView`'s I/O (`GetReader`, `LoadImage`) behind a loader so the
  widget only displays.
- Break the layout→window-title side effect: have `ChangeImage` (not the
  layout) own title updates.
- Fix `region.go` package-global drag state → instance fields.
- Remove `fmt.Println` debug/error output; use a logger or surface errors.
- Critical files: `gallery/tilelayout.go`, `gallery/imageview.go`,
  `gallery/gallery.go` (post-rename), `gallery/region.go`.

### Phase 4 — Shared bootstrap & de-dup the mains

- Add a `gallery` (or new `app/`) helper for the common bootstrap: app ID,
  icon, window, config load, `NewGallery`, `Init`, key wiring, resize, run.
- Give tieview its own app ID and window title (stop reusing imgview's).
- Factor the shared `-config`/`-c` `.toml` flag logic.
- Factor the duplicated mobile-focus-guard `OnImageChange` block.
- Factor the video-open window logic into one helper both mains call.
- Critical files: `cmd/imgview/main.go`, `cmd/tieview/main.go`, new shared
  helper in `gallery/`.

### Phase 5 — Isolate the platform seam

- Introduce a small platform abstraction (a `platform` interface or
  `_desktop.go`/`_mobile.go` files behind Fyne's device check) that centralizes:
  gesture routing, fullscreen-on-open, soft-keyboard focus suppression, and the
  Android Back key. Replace the scattered `IsMobile()` checks with calls into
  this seam.
- Keep it a runtime seam (per agreed direction: unified codebase), just
  consolidated in one place instead of ~10 sites.
- Critical files: `gallery/imageview.go`, `gallery/gallery.go`,
  `cmd/imgview/main.go`, `cmd/tieview/main.go`.

### Phase 6 — Library API hardening (finish "two apps, one library")

- Document the `gallery` extension surface (`CustomReader`, `Openable`,
  `VideoFile`, `VideoStreamer`, `DimensionProvider`, `Thumbnailer`, `Sidebar`,
  the `On*` callbacks) as the stable contract both apps depend on.
- Group `Gallery`'s exported callback API vs unexported wiring in the struct
  definition for readability.
- Consider merging `NewViewer` + `Init` into one constructor (the two-step init
  is non-obvious).
- Update CLAUDE.md to reflect all renames and the new file layout.

---

## Verification (per phase)

Run after each phase; all must pass with no behavior change unless the phase is
explicitly a behavior fix (Phase 2 error handling):

```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

Because both binaries need CGo/OpenGL and the tagging/gesture work is
UI-driven, also manually smoke-test the affected surface:

- **Phase 2:** launch `tieview`, open an image, open the tag panel, add/remove
  tags, toggle stars, switch images, switch profiles — confirm the quick-pick
  list stays stable-ordered and a simulated network failure surfaces (don't
  silently diverge). Confirm the sidebar tag filter still includes/excludes.
- **Phases 3 & 5:** launch both binaries, verify gallery paging, single-image
  pan/zoom/rotate on desktop, and (if a device/emulator is available) mobile
  gestures, fullscreen-on-open, and the Android Back button.

State explicitly in each phase's report whether UI was actually exercised or
only compiled — type/test passing verifies correctness, not feature behavior.
