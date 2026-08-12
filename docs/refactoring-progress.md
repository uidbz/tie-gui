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

### 3.3 — Debug Output Cleanup

**Commit:** `17a15ad` — refactor(phase3): remove debug fmt.Println statements

**Changes:**
- Removed 17 `fmt.Println` statements across gallery package (helper.go,
  imageview.go, tilelayout.go, gallery.go)
- All error cases were already handled (early return, fallback, skip)
- Removed unused `fmt` imports from all four files

**Rationale:** The println statements were debug noise that didn't add user
value. Error cases were already handled via return values or fallbacks.

### 3.4 — Simplified Title Update Callback

**Commit:** `d92feed` — refactor(phase3): simplify title update callback

**Changes:**
- Removed unnecessary `go func() { fyne.Do(...) }` wrapper from `changeFn`
- Now directly calls `viewer.window.SetTitle(...)` since already on UI thread

**Rationale:** `changeFn` is called from `ImageLayout.Layout` and `ImageView`
zoom methods, which Fyne guarantees run on the UI thread. Spawning a goroutine
that immediately marshals back to UI thread via `fyne.Do` is pointless overhead.

### Phase 3 Status

**Completed (4 of 6 items):**
- ✅ Fixed region.go package-global drag state
- ✅ Reduced layout→viewer back-references (thumbnailer, hotkeys)
- ✅ Debug output cleanup (17 println statements removed)
- ✅ Simplified title callback (removed unnecessary goroutine)

**Not addressed (deferred):**
- ImageView I/O coupling (`GetReader`, `LoadImage` in widget) — Medium
  complexity; would require loader interface + refactor of file opening logic.
  Deferred as ImageView currently works well and the coupling is not causing
  active problems.
- Split TileLayout into layout math + thumbnail loader — Large architectural
  refactor; would extract thumbnail generation/caching into separate service.
  High value but substantial work (20-30k tokens). Deferred to future phase
  or separate project.

**Rationale:** Phase 3 addressed the most problematic coupling issues:
bidirectional references, package globals, debug clutter, unnecessary
goroutines. The remaining items are architectural improvements that would
benefit from dedicated design work. The project is in significantly better
shape than before Phase 3 started.

### Verification

```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All pass. No behavior changes.

---

## ✅ Phase 4 — Shared Bootstrap & De-dup Mains (Complete)

Completed in five incremental commits addressing the duplication flagged in the
roadmap: both mains had copy-pasted app bootstrap, config flag logic, mobile
focus guards, and video window creation.

### 4.1 — Give tieview distinct identity

**Commit:** `0beab8b` — refactor(phase4): give tieview distinct app ID and window title

**Changes:**
- Changed tieview's app ID from `"sr.ht.uid.imgview"` to `"sr.ht.uid.tieview"`
- Changed window title from `"imgview"` to `"tieview"`

**Rationale:** Both apps shared the same ID and title, making them
indistinguishable in the system (taskbar, window manager, preferences). This
was the simplest isolated fix to make tieview identifiable.

### 4.2 — Factor app bootstrap

**Commit:** `814b5ac` — refactor(phase4): factor common app bootstrap into gallery.NewApp

**Changes:**
- Created `gallery/apphelper.go` with `NewApp(appID, windowTitle, iconData)`
- Both mains now call `myApp, myWindow := gallery.NewApp(...)` instead of the
  three-line bootstrap (app.NewWithID, SetIcon, NewWindow)
- Removed redundant `fyne.io/fyne/v2/app` import from both mains

**Rationale:** The bootstrap pattern was identical in both mains; only the ID
and title strings differed. Factoring it eliminates 4 lines of duplication.

### 4.3 — Factor config flag logic

**Commit:** `4ccc21b` — refactor(phase4): factor .toml config flag logic

**Changes:**
- Added `gallery.ConfigFlag(helpText)` — defines -config/-c flags, returns pointer
- Added `gallery.NormalizeConfigPath(path)` — appends .toml suffix if needed
- Both mains call `ConfigFlag` to define flags, then `flag.Parse()`, then pass
  `NormalizeConfigPath(*configPath)` to their config loaders
- Removed duplicated flag definition (3 lines) and suffix logic (5 lines)

**Rationale:** The -config/-c flag definition and .toml suffix appending were
identical in both mains (including the comment). ConfigFlag lets the caller
control when `flag.Parse()` is called (tieview needs to define other flags
first). NormalizeConfigPath centralizes the suffix logic.

### 4.4 — Factor mobile focus guard

**Commit:** `22937b9` — refactor(phase4): factor mobile focus guard into FocusImageViewOnDesktop

**Changes:**
- Added `gallery.FocusImageViewOnDesktop(window, viewer)` helper
- imgview's `OnImageChange` now calls the helper directly
- tieview's `OnImageChange` calls the helper first, then runs its tagger
  overlay logic

**Rationale:** The mobile focus guard (IsMobile check, focus CurrentImageView
on desktop, skip on mobile to avoid soft keyboard) was duplicated verbatim with
identical comment in both mains. The helper encapsulates the platform check and
focus call. Eliminates 8 lines of duplication.

### 4.5 — Factor video window creation

**Commit:** `6b97de0` — refactor(phase4): factor video window creation into OpenVideoWindow

**Changes:**
- Created `gallery/videohelper.go` with
  `OpenVideoWindow(app, player, displayPath, onClose)`
- Helper creates window with "Video: basename" title, wires close intercept
  (close video → close window → optional cleanup), resizes 800x520, shows
- imgview passes `nil` for onClose; tieview uses it to remove temp files
- Removed 20 lines of duplication (8 from imgview, 12 from tieview)

**Rationale:** Both mains had overlapping video window setup logic. imgview
used a direct path; tieview added streaming/temp-file logic. The window
creation part (title format, close intercept pattern, 800x520 size) was
duplicated. Helper factors the common pattern and lets tieview inject its
cleanup logic via the onClose callback.

### Phase 4 Summary

**Lines changed:** ~90 additions (3 new files), ~65 deletions across both mains  
**Key wins:**
- tieview now has distinct identity (app ID, window title)
- Eliminated 4 copy-paste bootstrap patterns
- Centralized magic constants (800x520 video window size)
- Both mains are now 50+ lines shorter and focus on their unique logic

**Verification:**
```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All pass. No behavior changes.

---

## ✅ Phase 5 — Isolate Platform Seam (Complete)

Completed in three incremental commits addressing the scattered IsMobile()
checks flagged in the roadmap. Platform-specific behavior (mobile vs desktop)
is now centralized in a single Platform abstraction instead of ~10 inline
device checks.

### 5.1 — Create Platform abstraction

**Commit:** `79c6699` — refactor(phase5): create Platform abstraction for mobile vs desktop

**Changes:**
- Created `gallery/platform.go` with Platform struct and semantic methods:
  - `ShouldFocusImageView()` — focus suppression (soft keyboard avoidance)
  - `ShouldHandleHotkeysAtWindowLevel()` — keyboard routing
  - `ShouldRegisterBackButton()` — Android/iOS back button
  - `ShouldAutoFullscreen()`, `ShouldExitFullscreenOnGalleryView()` — fullscreen management
  - `ShouldDownscaleImages()` — GPU memory optimization
  - `UsesMobileDragGestures()` — gesture routing
  - `ShouldUseTapForAction()` — tap vs swipe preference
- Added `platform *Platform` field to Gallery and ImageView structs
- Wired through NewGallery and NewImageView constructors
- NewPlatform() gracefully handles test environments (defaults to desktop)

**Rationale:** This is a runtime seam (per roadmap: unified codebase), not
compile-time. Platform detection happens once at Gallery creation and is
propagated to ImageView instances. Each method name expresses intent instead
of requiring readers to reverse-engineer the meaning of `!IsMobile()` in
context.

### 5.2 — Replace checks in gallery package

**Commit:** `af1f63b` — refactor(phase5): replace IsMobile checks in gallery package

**Changes:**
- **gallery/apphelper.go:** FocusImageViewOnDesktop uses `ShouldFocusImageView()`
- **gallery/imageview.go:** downscaleForMobile uses `ShouldDownscaleImages()`,
  Dragged/DragEnd use `UsesMobileDragGestures()`
- **gallery/gallery.go:** KeyPress uses `ShouldHandleHotkeysAtWindowLevel()`,
  InitHotkeys uses `ShouldRegisterBackButton()`, showGallery uses
  `ShouldExitFullscreenOnGalleryView()`, ChangeImage uses `ShouldAutoFullscreen()`

**Rationale:** Replaced 8 scattered `fyne.CurrentDevice().IsMobile()` checks
with semantic method calls. Each call site now reads as intent (e.g.
"should register back button?") instead of a device check plus reader-inferred
logic.

### 5.3 — Replace checks in cmd mains

**Commit:** `93dad6e` — refactor(phase5): replace IsMobile checks in cmd mains

**Changes:**
- Added `Platform()` accessor to Gallery for external access
- **cmd/tieview/main.go:** Tagger tap/swipe choice uses
  `ShouldUseTapForAction()`, OnHide focus uses `ShouldFocusImageView()`
- **cmd/imgview/main.go:** Folder picker check uses `IsMobile()`

**Rationale:** The last 3 IsMobile() checks in project code (excluding
third_party) now route through the Platform abstraction. The Platform()
accessor lets mains query platform behavior without exposing the internal
field.

### Phase 5 Summary

**Lines changed:** ~120 additions (1 new file), ~10 net line change (method calls replace checks)  
**Key wins:**
- Centralized ~10 scattered IsMobile() checks into 1 platform abstraction
- Semantic method names express intent (ShouldAutoFullscreen vs bare IsMobile)
- Platform detection happens once, not per check
- Runtime seam maintains unified codebase (no build tags, no file splits)

**Verification:**
```sh
go build ./cmd/imgview
go build ./cmd/tieview
go test ./...
```

All pass. No behavior changes.

---

## 📋 Remaining Phases (Not Started)

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
