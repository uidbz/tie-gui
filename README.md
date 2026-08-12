# imgview / tieview

**High-performance image viewers with justified gallery layout and extensible architecture**

[![Website](https://img.shields.io/badge/website-imgview.app-blue)](https://imgview.app)
[![License](https://img.shields.io/badge/license-see%20repository-lightgrey)](https://git.sr.ht/~uid/imgview)

## Overview

This repository provides two complementary image viewer applications built on a shared, extensible gallery library:

- **imgview** — Local filesystem viewer with directory browsing, archive support (zip, rar, tar, cbr, etc.), and video playback
- **tieview** — Network-based viewer for [tie](https://git.sr.ht/~uid/tie) content repositories with tag filtering, virtual filesystem navigation, and cached thumbnails

### Key Features

**Gallery Engine:**
- **Justified row layout** — Images grouped into rows with consistent height, no horizontal gaps, preserving aspect ratios
- **Fast pagination** — Efficient handling of large collections (500+ images per page)
- **Archive support** — Read images from zip, rar, tar, cbr, and other common formats
- **Video integration** — libmpv player for video files with streaming support (tieview)
- **Mobile support** — Touch gestures, pinch-zoom, platform-optimized rendering

**Extension Architecture:**
- Clean "two apps, one library" design with documented stable API
- CustomReader interface for pluggable content sources (filesystem, network, archives, Android URIs)
- Platform abstraction for mobile vs desktop behavior
- Configurable hotkeys and thumbnailing

## Installation

### Download Precompiled Binaries

**Latest build: 2022-07-09**
- [imgview for Linux X11](https://tfh1.com/38ca24392a20f04457572a7dbceb2380e6f824200329be4a58f5e302493baa1f/imgview)
- [imgview for Windows](https://tfh1.com/7074246ba84ae7ff1510968fe23183b4e46c101a9e1a87dcb56c62a3e160ed0f/imgview.exe) (untested)

*Note: No macOS binaries provided due to Xcode license terms. The project should build successfully if compiled locally.*

### Build from Source

**Requirements:**
- Go 1.18 or later
- C compiler (gcc or clang)
- Fyne dependencies — see [Fyne Getting Started](https://developer.fyne.io/started)

**Build:**
```sh
git clone https://git.sr.ht/~uid/imgview
cd imgview
go build ./cmd/imgview   # local filesystem viewer
go build ./cmd/tieview   # tie network viewer

# Wayland build:
go build -tags wayland ./cmd/imgview
```

**Install:**
```sh
go install git.sr.ht/~uid/imgview/cmd/imgview@latest
go install git.sr.ht/~uid/imgview/cmd/tieview@latest
```

**Test:**
```sh
go test ./...
```

## Usage

### imgview — Local Filesystem Viewer

```sh
# View a single image
imgview /path/to/image.jpg

# Browse a directory
imgview /path/to/directory

# Open an archive
imgview /path/to/archive.zip

# Use custom config
imgview -config ~/.config/imgview/my-config.toml /path/to/images
imgview -c my-config /path/to/images  # short form
```

### tieview — Network Content Viewer

```sh
# Query images by tag
tieview -tag favorite
tieview -tag vacation -tag 2024

# Use specific tie config
tieview -config production.toml -tag work
tieview -c prod -tag work  # short form

# Use specific filehost
tieview -host fast -tag recent
```

**tieview features:**
- Tag filtering with include/exclude semantics
- Co-tag refinement (faceted search)
- Virtual filesystem navigation
- Image tagging panel (tap image or swipe up on mobile)
- Profile switching (manage multiple tie instances)
- Cached thumbnail storage in tie metadata

## Configuration

### Config File Locations

- **Linux:** `~/.config/imgview/config.toml`
- **Windows:** `%AppData%\imgview\config.toml`

Default configuration: [gallery/config.toml](gallery/config.toml)

### Key Settings

```toml
[General]
TileWidth = 300       # Target row height in gallery (px)
TileGap = 5           # Gap between tiles (px)
Workers = 8           # Concurrent thumbnail loaders
ImagesPerPage = 500   # Pagination size
DefaultWidth = 1200   # Initial window width
DefaultHeight = 800   # Initial window height
ThumbnailDir = "~/.cache/imgview"  # Local thumbnail cache
```

## Keyboard Shortcuts

### Gallery View

| Key | Action |
|-----|--------|
| `Q`, `Esc` | Quit |
| `Down`, `J` | Scroll down |
| `Up`, `K` | Scroll up |
| `PageUp` | Previous page |
| `PageDown` | Next page |

### Single Image View

| Key | Action |
|-----|--------|
| `Esc`, `Backspace` | Return to gallery |
| `Right`, `J` | Next image |
| `Left`, `K` | Previous image |
| `Up`, `H` | Rotate left |
| `Down`, `L` | Rotate right |
| `X` | Fit to window |
| `S` | Zoom to original size |
| `F` | Toggle fullscreen |
| `B` | Switch filtering mode |

### Mouse/Touch Gestures

| Input | Action |
|-------|--------|
| Scroll down | Zoom in |
| Scroll up | Zoom out |
| Drag | Reposition image |
| Pinch (mobile) | Zoom |
| Swipe (mobile) | Navigate images |
| Double-click/tap | Toggle fullscreen |

**Mobile-specific:**
- Android Back button returns to gallery
- Swipe up on image opens tag panel (tieview)
- Soft keyboard suppression for optimal viewing

## Architecture

### Repository Structure

```
imgview/
├── cmd/
│   ├── imgview/        # Local filesystem viewer entry point
│   └── tieview/        # Tie network viewer entry point
├── gallery/            # Shared library (rendering engine)
│   ├── gallery.go      # Gallery controller
│   ├── imageview.go    # Single-image display widget
│   ├── tilelayout.go   # Justified row layout engine
│   ├── imageinfo.go    # Per-item data model
│   ├── apphelper.go    # Shared app bootstrap
│   ├── platform.go     # Mobile vs desktop abstraction
│   ├── extension.go    # Extension API documentation
│   └── config.go       # Configuration management
├── tagselection/       # Tag picker widget (tieview)
├── mpvplayer/          # libmpv video player integration
├── third_party/fyne/   # Vendored Fyne fork
└── docs/               # Technical documentation
```

### Design Principles

**Two Apps, One Library:**
- Gallery package provides the rendering engine
- Applications extend via well-defined interfaces (CustomReader, Thumbnailer, etc.)
- Clean separation enables code reuse and independent development

**Extension Points:**
- `CustomReader` — Supply content from any source (network, archives, URIs)
- `Thumbnailer` — Custom thumbnail generation and caching
- Callback hooks — Customize behavior (OnImageChange, OnTapped, etc.)
- Platform abstraction — Unified codebase with platform-specific optimizations

See [gallery/extension.go](gallery/extension.go) for comprehensive API documentation.

## Development

### Recent Improvements (2024 Refactoring)

The codebase underwent a comprehensive six-phase refactoring to improve maintainability and extensibility:

1. **Phase 1:** Naming consistency (Viewer→Gallery, file reorganization)
2. **Phase 2:** Tag subsystem cleanup (eliminated state duplication, added error handling)
3. **Phase 3:** Decoupled internals (removed circular dependencies, fixed globals)
4. **Phase 4:** De-duplicated mains (factored common bootstrap, distinct app identities)
5. **Phase 5:** Platform seam (centralized mobile vs desktop behavior)
6. **Phase 6:** API hardening (documented extension contract, organized public API)

**Results:**
- 27 commits, zero behavior regressions
- Stable extension API with comprehensive documentation
- 50+ lines reduced per main (now focused on unique logic)
- Clear separation between public API and internal implementation

See [docs/refactoring-progress.md](docs/refactoring-progress.md) for detailed change log.

### Code Quality

**Testing:**
```sh
go test ./...  # Unit tests
go build ./cmd/imgview && go build ./cmd/tieview  # Build verification
```

**Documentation:**
- [CLAUDE.md](CLAUDE.md) — Comprehensive LLM reference for codebase structure
- [gallery/extension.go](gallery/extension.go) — Extension API documentation
- [docs/](docs/) — Technical design documentation

**Threading Model:**
- All widget mutations via `fyne.Do()` on UI goroutine
- Thumbnail loading in worker pool (configurable, default 8)
- Network calls in background goroutines, never on UI thread

## Performance

**Optimizations:**
- Justified layout algorithm: O(n) single-pass
- Thumbnail caching (local disk or tie metadata)
- Lazy loading with worker pool
- Mobile GPU memory optimization (downscale high-res images)
- Session-scoped in-memory tile cache

**Known Limitations:**
- Zoom/reposition on very high resolution images (>12MP) can be slow on older hardware
- First-time thumbnail generation for large collections takes time (cached thereafter)

## Troubleshooting

**Build errors:**
- Ensure Fyne dependencies are installed (see [Fyne docs](https://developer.fyne.io/started))
- On Linux, install: `gcc`, `libgl1-mesa-dev`, `xorg-dev`
- On Windows, use MinGW-w64 or TDM-GCC

**Performance issues:**
- Reduce `TileWidth` in config for faster layout on low-end hardware
- Decrease `Workers` if experiencing memory pressure
- Enable GPU downscaling on mobile (automatic)

**tieview connection issues:**
- Verify tie daemon is running and accessible
- Check tie config file path and credentials
- Use `-c /path/to/config.toml` to specify config explicitly

## Contributing

**Bug Reports & Feature Requests:**
- [Issue Tracker](https://todo.sr.ht/~uid/imgview)

**Development:**
- Follow existing code style (gofmt, clear naming)
- Add tests for new functionality
- Update documentation for API changes
- See [docs/refactoring-design-decisions.md](docs/refactoring-design-decisions.md) for architectural context

## Related Projects

- [tie](https://git.sr.ht/~uid/tie) — Network content repository with metadata tagging
- [Fyne](https://fyne.io) — Cross-platform GUI framework

## License

See repository for license information.

---

**Website:** [imgview.app](https://imgview.app)  
**Repository:** [git.sr.ht/~uid/imgview](https://git.sr.ht/~uid/imgview)
