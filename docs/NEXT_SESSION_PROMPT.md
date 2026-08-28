# Project Status — imgview/tieview

**Status:** Refactoring complete, production-ready codebase  
**Last updated:** 2024-08-12

## Current State

The imgview/tieview project has completed a comprehensive six-phase refactoring. The codebase is now maintainable, well-documented, and ready for feature development.

### Completed Work

**Six-phase refactoring (27 commits):**
1. ✅ Naming & File Structure — Consistent naming, organized files
2. ✅ Tagging Refactor — Eliminated state duplication, added error handling
3. ✅ Decoupled Internals — Removed circular dependencies, fixed globals
4. ✅ De-duplicated Mains — Factored common bootstrap, distinct app identities
5. ✅ Platform Seam — Centralized mobile vs desktop behavior
6. ✅ API Hardening — Documented extension contract, organized public API

**Documentation:**
- Professional README with architecture overview
- Comprehensive ARCHITECTURE.md technical guide
- Quick start guide (QUICKSTART.md)
- Extension API documentation (gallery/extension.go)
- Complete refactoring change log
- Updated CLAUDE.md for LLM sessions

### Architecture Highlights

**Two Apps, One Library:**
- Gallery package: reusable rendering engine
- imgview: local filesystem viewer
- tieview: tie network viewer with tagging

**Extension Points:**
- CustomReader interface for content sources
- Thumbnailer for custom thumbnail generation
- Callback hooks for behavior customization
- Platform abstraction for mobile vs desktop

**Code Quality:**
- All tests pass
- Zero behavior regressions
- Clean separation of concerns
- Documented stable API

## Repository Structure

```
imgview/
├── cmd/
│   ├── imgview/        # Local filesystem viewer
│   └── tieview/        # Tie network viewer
├── gallery/            # Shared library
│   ├── gallery.go      # Gallery controller
│   ├── imageview.go    # Single-image widget
│   ├── tilelayout.go   # Layout engine
│   ├── platform.go     # Platform abstraction
│   ├── extension.go    # API documentation
│   └── ...
├── tagselection/       # Tag picker widget
├── mpvplayer/          # Video player
├── docs/               # Technical documentation
│   ├── README.md       # Documentation index
│   ├── QUICKSTART.md   # Getting started guide
│   ├── ARCHITECTURE.md # Technical overview
│   └── ...
└── README.md           # Main project README
```

## Quick Reference

**Build:**
```sh
go build ./cmd/imgview
go build ./cmd/tie-view
go test ./...
```

**Run:**
```sh
imgview /path/to/images
tieview -tag favorite
```

**Key Files for Context:**
- `CLAUDE.md` — Comprehensive codebase reference
- `gallery/extension.go` — Extension API
- `docs/ARCHITECTURE.md` — System design
- `docs/refactoring-progress.md` — Change log

## Future Development

### Ready for Implementation

The refactoring establishes a solid foundation. Future work can focus on:

**Feature Development:**
- Additional image formats
- Performance optimizations (incremental layout, virtual scrolling)
- UI enhancements (themes, customization)
- Mobile gesture refinements

**Extension Examples:**
- S3/cloud storage CustomReader implementation
- Custom thumbnail format (WebP)
- Plugin system for filters/effects
- Export/batch operations

**Deferred Architectural Work (optional):**
- ImageView I/O decoupling (loader interface) — medium complexity
- TileLayout split (layout math + thumbnail service) — large refactor

Both are working well in current form. Only pursue if specific benefits justify the cost.

## For New Sessions

### Understanding the Codebase

1. **Start with:** `README.md` for project overview
2. **Read:** `docs/ARCHITECTURE.md` for system design
3. **Reference:** `CLAUDE.md` for detailed codebase map
4. **Extension API:** `gallery/extension.go` for interfaces

### Common Tasks

**Adding new CustomReader:**
1. Implement `CustomReader` interface (GetReader, Path)
2. Optionally implement `Openable`, `VideoFile`, etc.
3. Wire into application main
4. Example: see `tieReader` in `cmd/tie-view/tie.go`

**Adding new platform behavior:**
1. Add method to `Platform` struct (`gallery/platform.go`)
2. Replace scattered checks with method call
3. Update documentation

**Modifying layout:**
1. Layout logic in `TileLayout.Layout` (`gallery/tilelayout.go`)
2. Algorithm documented in `docs/ARCHITECTURE.md`
3. Test with various image collections

### Testing Changes

**Verification checklist:**
```sh
go build ./cmd/imgview && go build ./cmd/tie-view && go test ./...
# Manual: launch both apps, test gallery + single-image view
```

**No regressions:** All changes since Phase 1 maintained test/build success.

## Communication Style

**Documentation standards:**
- Clear, concise technical language
- Example-driven (show, don't just tell)
- Layered depth (quick start → architecture → reference)
- Living documents (update alongside code)

**Commit messages:**
- Descriptive subject line (imperative mood)
- Body explains what and why, not how
- Reference phase/issue when applicable
- Co-authored attribution

## Project Context

**Development approach:**
- Incremental commits (verify after each change)
- Documentation alongside implementation
- Thread safety via `fyne.Do()` pattern
- Extension over modification

**Quality standards:**
- All builds must pass
- No behavior regressions (unless intentional fixes)
- Public API stability (gallery/extension.go)
- Comprehensive documentation

---

The project is in excellent shape. Well-organized, well-documented, and ready for future development or production use.
