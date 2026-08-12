# Starting Prompt for Refactoring Continuation

Use this prompt to resume the imgview/tieview refactoring:

---

Continue the imgview/tieview refactoring project. Phases 1-3 are complete
(naming, tagging, decoupling). Phase 4 is next: de-duplicate the two mains.

**Context:**
- Working directory: `/home/johan/go/src/sourcehut/imgview`
- Project: Two Fyne GUI apps (imgview=local viewer, tieview=tie-network client)
  sharing a gallery/ library
- Git repo is clean, 18 refactoring commits on master
- All documentation in `docs/`:
  - `code-quality-review-roadmap.md` — 6-phase plan
  - `refactoring-progress.md` — what's done (Phases 1-3 complete)
  - `refactoring-design-decisions.md` — rationale, patterns, trade-offs

**Phase 4 goal (from roadmap):**
De-duplicate the two command entry points:
- Factor common app bootstrap (app ID, icon, window, config, viewer init, key
  wiring, resize, run)
- Give tieview its own distinct app ID and window title (currently reuses
  imgview's)
- Factor shared `-config`/`-c` `.toml` flag logic
- Factor duplicated mobile-focus-guard `OnImageChange` block
- Factor video-open window logic

**Key files:**
- `cmd/imgview/main.go` (263 lines)
- `cmd/tieview/main.go` (389 lines)
- `gallery/gallery.go` (post-Phase-1 rename from imageviewer.go)

**Patterns established:**
- Incremental commits with clear messages
- Verify after each change: `go build ./cmd/imgview && go build ./cmd/tieview && go test ./...`
- Document rationale in commit message and update `docs/refactoring-progress.md`
- Thread safety: all widget mutations via `fyne.Do()`
- No behavior changes unless fixing bugs

**Before starting:**
Read `docs/refactoring-progress.md` (Phases 1-3 summary) and
`docs/code-quality-review-roadmap.md` (Phase 4 details).

Start Phase 4.
