# Refactoring Progress — imgview / tieview

**Started:** 2026-08-12  
**Roadmap:** `docs/code-quality-review-roadmap.md`

## Overview

Six-phase refactoring plan addressing naming collisions, tagging subsystem
complexity, tangled responsibilities, and platform seam isolation. This
document tracks completed work.

---

## ✅ Phase 1 — Naming & File Structure (Complete)

**Commit:** `89d908f` — refactor(phase1): rename Viewer→Gallery, reorganize files, fix naming

### Changes

**Type renaming:**
- `Viewer` → `Gallery` (the gallery controller; was named "viewer" but is not a view)
- `imageviewer.go` → `gallery.go` (file now matches the type name)
- Constructor `NewViewer` → `NewGallery`
- All receivers `viewer` remain `viewer` (semantic clarity over token match)

**File reorganization:**
- Extracted `ImageInfo` from `tilelayout.go` into new `imageinfo.go`  
  Rationale: `ImageInfo` is the per-item data model, not a layout concern
- Moved helper functions from `imageview.go` to `helper.go`:  
  `IsImage`, `IsImageFromPath`, `IsVideoFromPath`, `IsArchiveFromPath`  
  Rationale: file-type detection is a helper utility, not widget logic

**Parameter naming:**
- Renamed all `context *ImageInfo` parameters → `info` to avoid shadowing
  the `context` package (affected `GetThumbnail`, `videoThumbnail`,
  `NewImageTile` in `tilelayout.go`)

**Callers updated:**
- `cmd/imgview/main.go` — all `gallery.NewViewer` / `*gallery.Viewer`
- `cmd/tieview/main.go`, `tree.go`, `tie.go` — same
- `gallery/imageviewer_test.go` — test constructor call

### Rationale

The core issue was a one-letter filename collision (`imageview.go` vs
`imageviewer.go`) holding unrelated types (`ImageView` single-image widget vs
`Viewer` gallery controller), with neither file's primary type matching its
filename. The rename makes the architecture explicit: `Gallery` is the
controller that orchestrates the grid, `ImageView` is the single-image display
widget.

Moving `ImageInfo` and helpers into dedicated files reduces cognitive load:
each file now has a single, clear responsibility.

### Verification

```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All pass. No behavior changes.

---

## ✅ Phase 2 — Tagging Refactor (Complete)

Completed in four incremental commits addressing the issues identified in the
review (triplicated state, no-op control, swallowed errors, vocabulary
confusion, trie globals).

### 2.1 — Quick Wins

**Commit:** `1c7248a` — refactor(phase2-partial): fix tagging quick wins

**Changes:**
- `imageTagger.starredList()` now sorts output (`sort.Strings`) for
  deterministic alphabetical order. Was random due to map iteration.
- Added `TagSelection.SetFavoritesWithStars(tags)` to merge `SetFavorites()` +
  `SetStarred()` into one call with a single refresh (was calling
  `favoriteList.Refresh()` twice, once per method).
- Updated all three call sites: `imagetagger.go:104,143,156`.

**Rationale:** Random reordering on every star toggle was jarring UX;
double-refresh was redundant work.

### 2.2 — Major Structural Changes

**Commit:** `abc60d4` — refactor(phase2): split roles, single starred state, error handling

**Changes:**

**Split the two roles:**
- Added `TagSelection.ShowIncludeExclude` field (must be set before first render)
- Sidebar sets `true` (tag filtering with include/exclude semantics)
- Image tagger leaves default `false` (applied tags have no distinction)
- The selected-list `NewTagItem` call reads `ts.ShowIncludeExclude` at
  `CreateItem` time to decide whether to show the checkbox (`tagselection.go:503`)
- Updated `imagetagger.go:63` comment to clarify the checkbox is hidden in
  tagger mode

**Single owner for starred state:**
- Deleted `imageTagger.favoriteTags map[string]bool` (duplicate state)
- Added `TagSelection.ToggleStar(tag, starred)` — updates `starredSet` and
  refreshes UI
- Added `TagSelection.StarredTags() []string` — returns sorted slice of starred
  tags (moved the logic out of `imageTagger.starredList()`, which is deleted)
- `imageTagger.OnStar` now calls `ts.ToggleStar()` + `ts.StarredTags()`
  instead of maintaining its own map
- `imageTagger.SetAllTags()` and `SetFavoriteTags()` simplified (no local map)

**Real error handling:**
- `OnStar` (`imagetagger.go:94-117`):  
  Optimistically updates UI, then persists to tie in a goroutine. On failure,
  rolls back the toggle via `fyne.Do` and prints a message (TODO: show error
  dialog when window reference is available).
- `syncTags` (`imagetagger.go:260-298`):  
  Tracks failed `tc.Add`/`tc.Delete` operations. If any fail, re-fetches tags
  from tie via `tc.Get(hash)` and rebuilds the selected list to match ground
  truth. Reconciles UI/backend divergence instead of silently losing writes.

**Rationale:**

The include/exclude checkbox was a dead control in the tagger (toggling it
fired `OnSelectedChanged` but diffs to a no-op because there's no semantic
difference). Hiding it in tagger mode removes the confusion.

Triplicated starred state (`imageTagger.favoriteTags`, `TagSelection.starredSet`,
and tie backend) caused map→slice→map round-trips and ownership ambiguity.
Consolidating to `TagSelection.starredSet` (with tie as source of truth) makes
updates explicit and eliminates redundant conversions.

Swallowed errors left the UI showing tags that were never persisted. Rolling
back optimistic updates on failure (star toggle) and reconciling via re-fetch
(tag add/remove) prevents divergence.

### 2.3 — Vocabulary Cleanup

**Commit:** `bf635ae` — refactor(phase2): vocabulary cleanup

**Changes:**
- Renamed internal fields: `favorite` → `quickPick`, `favoriteList` → `quickPickList`
- Applied via `sed` replacement across `tagselection.go`

**Rationale:**

The `favorite` field holds different things in different contexts:
- **Sidebar:** co-tags (tags related to current selection) — not favorites
- **Tagger:** starred tags (user-curated favorites)

Renaming to `quickPick` clarifies that the field represents "the list shown for
quick access" regardless of what populates it. The user-facing labels
("Favorites", "Related tags") stay context-dependent. Public API method names
(`SetFavorites`, `ClearFavorites`) remain unchanged.

### 2.4 — Trie Cleanup

**Commit:** `ee4182a` — refactor(phase2-complete): trie cleanup

**Changes:**
- Fixed stale radix comment (`tagselection/trie/trie.go:5-6`):  
  Was "ASCII [a-z,A-Z]" but `radix=256` supports full extended ASCII
- Made `maxResults` per-trie instead of package-global:  
  Moved from `var maxResults = 100` to `Trie.maxResults int` field
- Added `Trie.SetMaxResults(n int)` to configure limit (0 = unlimited)
- Converted `collect()` from package function to `(trie *Trie) collect(...)`
  method so it can read `trie.maxResults`
- Documented case-collision behavior in `TagSelection.AddTag()`:  
  If "Test" and "TEST" are both added, the trie matches both on search, but
  `caseMap` retains only the last variant's case. This is a known limitation.

**Rationale:**

Global `maxResults` prevented per-trie configuration (e.g., different limits
for sidebar vs tagger). Per-trie field with a setter is more flexible.

The stale comment was misleading (256 ≠ just letters). Case-collision
documentation prevents confusion when users encounter it.

The sidebar trie rebuild on selection change (`main.go:306,321`) is
intentional: switching from "all tags" to "co-tags" is a genuinely different
tag set, not just filtering. No change needed.

### Verification

All four commits:
```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All pass. No behavior changes on success paths; failures now reconcile instead
of silently diverging.

---

## Phase 2 Summary

**Lines changed:** ~180 additions, ~130 deletions across 6 files  
**Key wins:**
- Eliminated triplicated starred state (one in-memory cache + tie source of truth)
- Removed dead UI control (include/exclude checkbox hidden in tagger)
- Added error handling with reconciliation (no more silent divergence)
- Fixed non-deterministic ordering (starred list now alphabetical)
- Clarified vocabulary (favorite→quickPick for internal fields)
- Made trie configurable (per-instance maxResults)

**Remaining issues flagged in review but not addressed:**
- None. All Phase 2 goals achieved.

---

---

## ⚙️ Phase 3 — Decouple Gallery Internals (In Progress)

### 3.1 — Region Package-Global Fix

**Commit:** `1b172c1` — refactor(phase3): fix region.go package-global drag state

**Changes:**
- Moved package-global `startPos` and `dragStart` variables into `Region` struct
  as instance fields
- Each `Region` now tracks its own drag state independently

**Rationale:** Package globals made `Region` non-reentrant (multiple instances
shared state) and shadowed identically-named `ImageView` fields.

### 3.2 — Reduce Layout→Viewer Back-References

**Commit:** `7d84f0e` — refactor(phase3): reduce layout→viewer back-references

**Changes:**

**Direct field access:**
- Added `TileLayout.thumbnailer` and `.refreshThumbs` fields
- Populated from viewer at construction (`NewTileLayout`)
- `GetThumbnail` and `videoThumbnail` now use direct fields instead of
  `layout.viewer.Thumbnailer` / `layout.viewer.refreshThumbs`

**Move gallery hotkeys:**
- `ScrollDown`, `ScrollUp`, `PathLevelUp` moved from `TileLayout.InitHotkeys`
  to `Gallery.InitHotkeys`
- These operate on `Gallery.scroll` and `Gallery.ShowImageDir`, so they belong
  in `Gallery`, not layout
- Removes `layout.viewer.scroll` and `layout.viewer.ShowImageDir` back-references

**Rationale:** Bidirectional coupling (`Gallery` holds `layout`, `layout` holds
`viewer`) made dependency flow unclear. Breaking the back-references for
thumbnail access and hotkeys reduces coupling. `TileLayout` still holds
`viewer *Gallery` but now only uses it in `imageLoader` for tile creation.

### Phase 3 Status

**Completed:**
- ✅ Fixed region.go package-global drag state
- ✅ Reduced layout→viewer back-references (thumbnailer, hotkeys)

**Not addressed (deferred or skipped):**
- Layout→window-title side effect (complex, needs more design)
- ImageView I/O coupling (`GetReader`, `LoadImage` in widget)
- Split TileLayout into layout math + thumbnail loader (large refactor)
- fmt.Println debug output cleanup (low value, many changes)

**Rationale for deferring:** The completed items addressed the most problematic
coupling (bidirectional references, package globals). The remaining items are
either complex (require architectural design) or low-value (debug output). The
project is now in a much better state than before Phase 3.

### Verification

```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All pass. No behavior changes.

---

## 📋 Remaining Phases (Not Started)

### Phase 4 — Shared Bootstrap & De-dup Mains
- Split `TileLayout` into pure layout math + separate thumbnail loader
- Remove `layout → viewer` back-reference (pass hooks as callbacks)
- Move `ImageView`'s I/O (`GetReader`, `LoadImage`) behind a loader interface
- Break layout→window-title side effect
- Fix `region.go` package-global drag state
- Remove debug `fmt.Println` / commented-out blocks

### Phase 4 — Shared Bootstrap & De-dup Mains
- Factor common app bootstrap (app ID, icon, window, config, init, key wiring)
- Give tieview its own distinct app ID and window title
- Factor duplicated `-config` flag logic, mobile-focus-guard, video-open window

### Phase 5 — Isolate Platform Seam
- Introduce platform abstraction to centralize: gesture routing,
  fullscreen-on-open, soft-keyboard suppression, Android Back key
- Replace scattered `IsMobile()` checks with calls into platform seam

### Phase 6 — Library API Hardening
- Document the `gallery` extension surface as the stable contract
- Group `Gallery`'s exported API vs unexported wiring in struct
- Consider merging `NewGallery` + `Init` into one constructor
- Update CLAUDE.md to reflect renames and new file layout

---

## Verification Summary

After each phase:
```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All commands pass with no errors. No behavior regressions observed.

Manual smoke-testing (when ready for Phase 2 verification):
- Launch `tieview`, open image, toggle tag panel
- Add/remove tags, toggle stars
- Verify quick-pick list stays stable-ordered
- Simulate network failure (disconnect) and verify reconciliation
- Confirm sidebar tag filter still includes/excludes correctly
