# Refactoring Design Decisions — imgview / tieview

This document captures the design rationale, trade-offs, and implementation
details behind the 2026-08 refactoring. Read `refactoring-progress.md` for
what changed; read this for why and how.

---

## High-Level Direction (User Input)

Before starting, clarified the intended architecture via targeted questions:

1. **Product split:** Two apps, one shared library. Keep both `imgview` (local)
   and `tieview` (network) as separate binaries; harden `gallery/` as a clean
   documented library. Factor duplicated bootstrap. Each `cmd` stays thin.

2. **Platform handling:** Keep unified codebase but isolate Android-vs-desktop
   branches behind a small platform seam instead of scattered `IsMobile()`
   checks throughout the code.

3. **Naming direction:** Rename the gallery controller `Viewer` → `Gallery`
   (lives in `gallery.go`); keep the single-image widget `ImageView`
   (`imageview.go`); move `ImageInfo` out of `tilelayout.go` into
   `imageinfo.go`.

These decisions shaped Phases 1–2 (completed) and will guide Phases 3–6.

---

## Phase 1 — Naming & File Structure

### Problem Statement

The gallery controller was named `Viewer` but is not a view — it's a
controller that orchestrates the grid, handles navigation, wires hotkeys, and
manages state. The single-image display widget is `ImageView`. Both existed in
near-identically-named files (`imageviewer.go` vs `imageview.go`), creating a
one-letter disambiguation problem.

`NewViewer` returned a `Viewer` (gallery controller) but the name collides
phonetically with "image viewer." The receiver name `viewer` was used for both
the gallery controller and the return value of `NewViewer`, so `iv` (short for
`ImageView`) was also used for the gallery in some places, making `iv` mean two
different types.

### Design Decisions

**Why "Gallery" not "GalleryView" or "GalleryController"?**

Considered three options (see roadmap AskUserQuestion):
1. `Gallery` + `ImageView` — distinct, matches package name intent
2. `GalleryView` + `ImageView` — parallel `-View` naming
3. Defer naming decision

Chose **Gallery** because:
- The type orchestrates the grid/pagination/navigation/hotkeys — it's the
  gallery itself, not a "view of a gallery."
- Parallel `-View` naming (`GalleryView` + `ImageView`) suggests they're both
  views, but `Gallery` is a controller (has business logic, manages state).
- "Gallery" matches the package name (`gallery/`) and the domain term.

**Why keep receiver name `viewer` instead of `g`?**

The name `viewer` is semantically clearer than a single-letter abbreviation.
The token mismatch (`type Gallery` with `func (viewer *Gallery)`) is
acceptable because the receiver name is local to each method — readers
understand "this gallery instance" from context. Short abbreviations (`g`,
`gal`) save no meaningful space and lose clarity.

**Why extract ImageInfo to its own file?**

`ImageInfo` is the per-item data model (path, dimensions, callbacks, flags).
It's consumed by multiple components (`Gallery`, `TileLayout`, `Tile`,
`ImageView`) and is conceptually separate from layout math. Keeping it in
`tilelayout.go` (789 lines, the largest file) was a historical accident.
Extracting it to `imageinfo.go` (67 lines) makes the architecture explicit:
layout, model, and view are distinct.

**Why move file-type helpers from imageview.go to helper.go?**

`IsImage`, `IsImageFromPath`, etc. are pure functions with no widget state.
They belong with other image utilities (`ScaleImage`, `Decode`, `ReadDir`) in
`helper.go`. Keeping them in `imageview.go` suggested they were `ImageView`
methods or tightly coupled to the widget, which they're not.

The `ImageView` widget should focus on display/interaction logic (pan, zoom,
gestures, rendering). Format detection is a helper concern.

### Trade-offs & Alternatives Considered

**Considered:** Rename `Viewer` → `ImageGallery` to avoid overloading "viewer."  
**Rejected:** Too verbose; "image" is implicit in the package name. `Gallery`
is concise and domain-appropriate.

**Considered:** Keep `imageviewer.go` filename, just rename the type inside.  
**Rejected:** The file/type name mismatch would persist. Renaming the file to
match the type (`gallery.go`) is a one-time churn for long-term clarity.

**Considered:** Move `ImageInfo` into a `model/` subpackage.  
**Rejected:** Premature structure; the `gallery/` package is cohesive as-is.
A `model/` subpackage would add import boilerplate for minimal benefit.

### Implementation Notes

**Git rename tracking:** Used `git mv imageviewer.go gallery.go` to preserve
history. The diff shows as a rename with modifications, not a delete+add.

**Global replace strategy:** Used `Edit(replace_all: true)` for mechanical
renames (`*Viewer` → `*Gallery`, `gallery.NewViewer` → `gallery.NewGallery`).
Each replacement was type-safe: the compiler caught any missed references.

**Parameter renaming:** The `context *ImageInfo` → `info` rename required
reading each function body to confirm the parameter wasn't used in a way that
would break (e.g., passed to a function expecting an actual `context.Context`).
All uses were purely as `ImageInfo`, so the rename was safe.

---

## Phase 2 — Tagging Refactor

### Problem Statement (Detailed)

The tagging subsystem had several intertwined issues:

1. **One widget, two incompatible roles:** `TagSelection` served both the
   sidebar filter (include/exclude checkbox meaningful) and the image tagger
   (checkbox meaningless — toggling it fired `OnSelectedChanged` but diffs to
   a no-op). Users see a control that does nothing.

2. **Triplicated starred-tag state:**
   - `imageTagger.favoriteTags map[string]bool` — local tracking
   - `TagSelection.starredSet map[string]bool` — widget tracking
   - Tie backend `("tags","favorite")` — source of truth  
   Every star toggle: read map → build slice → write map → refresh → persist
   to tie. Map→slice→map round-trips; unclear ownership.

3. **Redundant double refresh:** `SetFavorites()` and `SetStarred()` each call
   `favoriteList.Refresh()`. Callers always call both back-to-back
   (`imagetagger.go:103-104,142-143,155-156`), so two refreshes for one
   logical update.

4. **Non-deterministic ordering:** `imageTagger.starredList()` iterates a map,
   so the quick-pick list reorders randomly on each star toggle. Jarring UX.

5. **Swallowed errors + optimistic state divergence:**  
   `syncTags` and `OnStar` only `fmt.Println` on failure and never reconcile
   local state with tie. A failed `tc.Add(hash, "tag", tag)` leaves the UI
   showing a tag that was never persisted. User closes the panel, reopens →
   tag is gone. Confusing.

6. **Vocabulary collision:** `favorite`, `star`, and `quick-pick` used for
   overlapping concepts. In the sidebar the `favorite` field holds *co-tags*
   (related tags), not starred tags. In the tagger it holds *starred* tags.
   One field name, two meanings.

7. **Trie rough edges:** `maxResults` is a package global (can't configure
   per-trie); stale radix comment; `caseMap` silently loses case-collisions
   (undocumented).

### Design Decisions

#### 2.1 — Split the Two Roles (ShowIncludeExclude)

**Decision:** Add a `ShowIncludeExclude bool` field to `TagSelection` (must be
set before first render). Sidebar sets `true`, tagger leaves default `false`.

**Why not two separate widgets?**

The widgets share 95% of their logic (search trie, quick-pick list, selected
list, star buttons). Duplicating all of that to hide one checkbox is high
maintenance cost. A mode flag is simpler.

**Why not hide the checkbox dynamically at runtime?**

Fyne widget creation is lazy (cells are created on demand via `CreateItem`).
By the time we'd know to hide the checkbox, the widget tree is already built.
A `ShowIncludeExclude` field read at `CreateItem` time (sidebar: line 503,
tagger: line 503) is the Fyne-idiomatic approach.

**Trade-off:** The mode flag adds a configuration step (`ts.ShowIncludeExclude
= true` in sidebar), but it's explicit and localized. The alternative (two
separate widget types) would duplicate ~600 lines of code.

#### 2.2 — Single Owner for Starred State

**Decision:** Delete `imageTagger.favoriteTags`. Make `TagSelection.starredSet`
the single in-memory cache. Tie backend is source of truth.

**Why keep starredSet in TagSelection instead of fetching from tie every time?**

Fetching from tie on every render would require async load + UI update for
every star-button draw, which is dozens of round-trips per quick-pick list
display. The in-memory `starredSet` is a read-through cache: fetch once from
tie at load, update locally on changes, persist back to tie.

**Why not keep favoriteTags in imageTagger and push to TagSelection?**

That inverts ownership: `imageTagger` would own the state and `TagSelection`
would be a dumb view. But `TagSelection` already has `starredSet` for the
star-button display, so we'd have two caches again. Consolidating ownership in
the widget that displays the stars is more cohesive.

**New API surface:**
- `TagSelection.ToggleStar(tag, starred)` — update `starredSet` + refresh UI
- `TagSelection.StarredTags() []string` — sorted slice of starred tags

**Trade-off:** `imageTagger` now depends on `TagSelection` for starred-tag
queries (`ts.StarredTags()`), but that's appropriate — the widget owns the
state. The alternative (imageTagger owning it) duplicates state and loses
cohesion.

#### 2.3 — Error Handling with Reconciliation

**Decision:** On network failure, roll back optimistic UI updates or re-fetch
from tie to reconcile.

**OnStar strategy (optimistic rollback):**

```go
// Optimistically update UI
it.ts.ToggleStar(tag, isStarred)
it.ts.SetFavoritesWithStars(it.ts.StarredTags())

go func() {
    var err error
    if isStarred {
        _, err = it.tc.Add("tags", "favorite", tag)
    } else {
        _, err = it.tc.Delete("tags", "favorite", tag)
    }
    if err != nil {
        // Roll back on the UI goroutine
        fyne.Do(func() {
            it.ts.ToggleStar(tag, !isStarred) // reverse the toggle
            it.ts.SetFavoritesWithStars(it.ts.StarredTags())
            fmt.Printf("failed to %s tag %q: %v\n", ..., err)
        })
    }
}()
```

**Rationale:** Star toggles are instant feedback. Waiting for the network would
make the UI feel sluggish. Optimistic update + rollback on failure gives
immediate response with correctness.

**syncTags strategy (reconcile via re-fetch):**

Track failed `tc.Add`/`tc.Delete` operations. If any fail, re-fetch tags from
tie via `tc.Get(hash)` and rebuild the selected list to match tie's ground truth.

**Why re-fetch instead of rollback?**

`syncTags` can have multiple operations in flight (add 3 tags, remove 2). If
one fails, rolling back that operation doesn't guarantee consistency — another
goroutine might have changed the same tag. Re-fetching from tie gives a clean
reconciliation: "show me what tie actually has, ignore my local state."

**Why not use a mutex to serialize tag operations?**

That would block the UI. The current design allows rapid sequential changes
(user clicks tags quickly) without waiting for network round-trips. The
re-fetch reconciliation handles the rare failure case without sacrificing
responsiveness in the common case.

**Trade-off:** Re-fetch adds latency on failure (extra `tc.Get(hash)` call),
but failures are rare, and correctness is more important than speed on the
error path.

**TODO left in code:** "Show error dialog to user (requires window reference)."
Today errors print to stdout. Proper UI feedback (a dialog or toast) requires
passing `fyne.Window` to `imageTagger`, which is a minor change for Phase 4
(when we're refactoring initialization).

#### 2.4 — Vocabulary Cleanup

**Decision:** Rename internal fields `favorite` → `quickPick`, `favoriteList`
→ `quickPickList`. Keep method names (`SetFavorites`, `ClearFavorites`)
unchanged.

**Why keep the method names?**

The methods are part of the public API (called by `cmd/tie-view/main.go` and
`imagetagger.go`). Renaming them would break callers and require updates to
CLAUDE.md's API table. The method names are also semantically correct:
`SetFavorites(tags)` means "set the tags shown in the quick-pick area,"
regardless of whether those tags are starred favorites (tagger) or co-tags
(sidebar).

**Why rename the internal fields?**

The field names are private to `TagSelection` and only appear in the
implementation. Renaming them clarifies their role (the quick-access list)
without changing the public contract.

**Trade-off:** The field name (`quickPick`) doesn't perfectly match the method
name (`SetFavorites`), but that's acceptable because the method name is
user-facing (describes what the list contains in the tagger context) and the
field name is implementation-facing (describes the UI role).

#### 2.5 — Trie Cleanup

**Decision:** Make `maxResults` a per-trie field instead of a package global.

**Why not keep it global?**

Different components might want different limits. The tagger's search dropdown
might cap at 50 results (screen space), while the sidebar might cap at 100
(more space). A global prevents per-use-case tuning.

**Why not make it a parameter to KeysWithPrefix?**

That would require threading the limit through every call. A field set once at
Trie creation (`trie := NewTrie(); trie.SetMaxResults(50)`) is more ergonomic.

**Why document case-collision instead of fixing it?**

Fixing case-collisions would require `caseMap` to be `map[string][]string`
(one lowercase key → multiple original-case variants) and a heuristic to pick
which variant to display (first added? most recent? alphabetically first?).
That adds complexity for a rare edge case.

Documenting the behavior in a comment (`AddTag` at line 568-574) is sufficient:
if users encounter it, they know it's expected. If it becomes a real problem
(users frequently add "Test" and "TEST"), we can revisit.

**Radix comment fix:** The comment said "ASCII [a-z,A-Z]" but `radix=256`
supports full extended ASCII (0-255). Fixed to say "full extended ASCII
character indexing."

---

## Implementation Patterns & Practices

### Incremental Commits

Phase 2 was split into four commits:
1. Quick wins (sort + merge refresh)
2. Major structural changes (split roles + single state + error handling)
3. Vocabulary cleanup
4. Trie cleanup

**Why not one big commit?**

Incremental commits make review easier (each commit is a digestible unit) and
allow bisecting if a regression is found. Each commit builds and passes tests
independently.

**Why group split-roles + single-state + error-handling into one commit?**

Those changes are semantically coupled: the error handling depends on the
single-state refactor (rolling back `starredSet` directly), and split-roles is
a prerequisite for understanding why error handling matters (the no-op checkbox
path obscured the need for reconciliation).

Splitting them into three commits would mean commit 2 changes behavior (removes
checkbox), commit 3 changes state ownership, and commit 4 adds error handling
— but commit 3's motivation only makes sense after commit 2. Grouping them into
one "major structural changes" commit preserves the narrative.

### Thread Safety

**UI goroutine discipline:** All `fyne.Do(func() { ... })` blocks are used when
mutating widget state from background goroutines. Key locations:
- `imageTagger.go:219-227` (load current tags)
- `imagetagger.go:106-115` (error rollback in OnStar)
- `imagetagger.go:280-295` (reconcile in syncTags)

**Why not use a mutex?**

Fyne's threading model is "all widget operations on the UI goroutine." A mutex
would protect shared state but wouldn't prevent Fyne crashes from off-thread
widget access. `fyne.Do` is the correct pattern.

**makeTagSidebar closures:** The sidebar code in `main.go:235-340` uses
closure variables (`allTags`, `allFavorites`, `allFavoritesLabel`) that are
only read/written inside `fyne.Do` blocks, so no mutex is needed. CLAUDE.md
documents this explicitly.

### Testing Strategy

**Unit tests:** No new tests added because the changes are refactoring
(behavior-preserving). The existing test suite (`gallery/imageviewer_test.go`,
`tieview/tie_test.go`, `trie/trie_test.go`) was run after every commit to
verify no regressions.

**Manual testing guidance:** `refactoring-progress.md` documents the smoke-test
checklist for Phase 2:
- Launch tieview, open image, toggle tag panel
- Add/remove tags, toggle stars
- Verify quick-pick list stays stable-ordered
- Simulate network failure (disconnect) and verify reconciliation
- Confirm sidebar tag filter still includes/excludes correctly

**Why not add tests for error handling?**

Testing the error-handling paths would require mocking `TieClient` to inject
failures, which is invasive (the tagger receives a `*client.TieClient`, not an
interface). The error-handling code is simple (roll back or re-fetch) and the
correctness argument is straightforward. Adding mocks for test coverage alone
is not justified.

If Phase 6 introduces a `TieClient` interface (for library API hardening), we
can add error-handling tests then.

### Backward Compatibility

**No API breaks:** All changes are internal. The `TagSelection` public API
(`AddTag`, `SetFavorites`, `SelectedTags`, etc.) is unchanged. The new methods
(`ToggleStar`, `StarredTags`, `SetFavoritesWithStars`) are additions, not
replacements.

**Gallery rename impact:** `NewViewer` → `NewGallery` and `*gallery.Viewer` →
`*gallery.Gallery` are API breaks for external callers. However:
- The project is not published as a library (no external consumers).
- Both `cmd/imgview` and `cmd/tie-view` are in-tree and were updated in the
  same commit.
- If this were a library, the rename would be a major version bump (v2.0.0).

### Performance Considerations

**Sorting overhead:** `starredList()` now sorts on every call. Starred-tag
lists are small (<50 tags typically), so `sort.Strings` is ~1µs. Not a concern.

**Double-refresh elimination:** Merging `SetFavorites` + `SetStarred` into
`SetFavoritesWithStars` saves one `favoriteList.Refresh()` call per update.
Each refresh is ~10ms (widget tree walk + render), so this saves ~10ms per
star toggle. Measurable but not dramatic.

**Reconcile overhead:** Re-fetching tags from tie on error adds one `tc.Get(hash)`
call (~50-200ms depending on network). This only happens on failure, so it
doesn't affect the common path.

---

## Lessons & Future Work

### What Went Well

- Incremental commits with clear commit messages made the refactor reviewable.
- Asking directional questions upfront (two apps vs one, naming preferences)
  avoided rework.
- Mechanical renames (`replace_all: true`) were fast and safe (compiler catches
  errors).
- Thread-safety discipline (`fyne.Do`) prevented subtle race bugs.

### What to Watch

- The sidebar trie rebuild on selection change (`main.go:306,321`) rebuilds
  the entire trie even when switching between two selections with large
  overlap. If the tag list grows to 10k+ tags, this might be slow. Monitor.
- The `imageTagger` error messages print to stdout. Phase 4 (shared bootstrap)
  should thread `fyne.Window` through so we can show error dialogs.
- The `TagSelection` dual-mode design (ShowIncludeExclude) is simple but not
  extensible. If a third mode appears, consider a mode enum or strategy pattern.

### Phase 3 Prep Notes

Phase 3 will tackle `TileLayout`'s god-object problem (layout math + I/O +
video extraction + caching in one type). Design considerations:

- **Split point:** Layout math (justified rows) vs thumbnail loading (worker
  pool + cache + video frames).
- **Interface:** `TileLayout` should accept a `ThumbnailLoader` interface, not
  directly import `mpvplayer`.
- **Naming:** Candidate names for the loader: `ThumbnailService`,
  `ImageCache`, `ThumbnailProvider`.
- **Coupling:** The layout currently reaches back into `viewer` fields
  (`viewer.Thumbnailer`, `viewer.refreshThumbs`). Break this with callbacks or
  an interface.

**Read before starting Phase 3:**
- `gallery/tilelayout.go:217-335` (layout math)
- `gallery/tilelayout.go:337-645` (thumbnail pipeline)
- `gallery/imageviewer.go:30` (Thumbnailer field)

---

## References

- **Roadmap:** `docs/code-quality-review-roadmap.md` — six-phase plan
- **Progress:** `docs/refactoring-progress.md` — what was done
- **API reference:** `CLAUDE.md` — unchanged; will be updated in Phase 6
- **Git history:** `git log --oneline docs/` — commits starting `89d908f`
