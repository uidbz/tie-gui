# imgview / tie-view

**High-performance image viewers with justified gallery layout and extensible architecture**

[![Website](https://img.shields.io/badge/website-imgview.app-blue)](https://imgview.app)
[![License](https://img.shields.io/badge/license-see%20repository-lightgrey)](https://github.com/uidbz/tie-gui)

## Overview

This repository provides two complementary image viewer applications built on a shared, extensible gallery library:

- **imgview** — Local filesystem viewer with directory browsing, archive support (zip, rar, tar, cbr, etc.), and video playback
- **tie-view** — Network-based viewer for [tie](https://git.sr.ht/~uid/tie) content repositories with tag filtering, virtual filesystem navigation, and cached thumbnails

### Key Features

**Gallery Engine:**
- **Justified row layout** — Images grouped into rows with consistent height, no horizontal gaps, preserving aspect ratios
- **Fast pagination** — Efficient handling of large collections (500+ images per page)
- **Archive support** — Read images from zip, rar, tar, cbr, and other common formats
- **Collection previews** — Folder/archive tiles show a content thumbnail with a folder badge; swipe horizontally on a tile to cycle through its images; video tiles cycle through up to 10 extracted frames
- **Video integration** — libmpv player for video files with streaming support (tie-view)
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
- Go 1.25 or later
- C compiler (gcc or clang)
- Fyne dependencies — see [Fyne Getting Started](https://developer.fyne.io/started) (on Debian/Ubuntu: `gcc libgl1-mesa-dev xorg-dev`)
- **libmpv** (optional) — required for video playback and video thumbnails (on Debian/Ubuntu: `libmpv-dev`). Build with `-tags nompv` to disable video support entirely.

**Clone (note the submodule):**

imgview builds against a modified Fyne fork, vendored as a **git submodule** at `third_party/fyne` (the `imgview` branch of [github.com/uidbz/fyne](https://github.com/uidbz/fyne): GLVideo video embedding, Android system-bar control, longer desktop texture-cache lifetime). `go.mod` points at it via a `replace` directive, so the submodule must be checked out:

```sh
git clone --recurse-submodules https://github.com/uidbz/tie-gui
cd imgview

# …or, after a plain clone:
git submodule update --init
```

**Build:**
```sh
go build ./cmd/imgview   # local filesystem viewer
go build ./cmd/tie-view   # tie network viewer (tie is a pinned dependency, fetched automatically)

# Wayland build:
go build -tags wayland ./cmd/imgview

# Build without libmpv (no video playback/thumbnails):
go build -tags nompv ./cmd/imgview ./cmd/tie-view
```

> **Note:** `go install github.com/uidbz/tie-gui/cmd/imgview@latest` does **not** work — the `replace` directive points into the git submodule, which module-proxy downloads do not contain. Build from a clone as above.

**Install:**
```sh
go install ./cmd/imgview ./cmd/tie-view   # from within the clone; installs to $GOBIN
```

**Test:**
```sh
go test ./...
```

### Build without video playback

Video playback and video thumbnails require **libmpv** (`-lmpv`). To build
without it — no libmpv headers/libraries needed at all — add `-tags nompv`.
Video files then fall back to a placeholder thumbnail and cannot be played:

```sh
go build -tags nompv ./cmd/imgview ./cmd/tie-view   # desktop, no video
NOMPV=1 ./build-android.sh                          # Android, no video (see below)
```

### Build & install the Android APK

Both apps package as arm64-v8a APKs with in-app libmpv video playback
(hardware MediaCodec decode). See [docs/ANDROID.md](docs/ANDROID.md) for the
internals.

**Requirements:**
- `fyne` CLI — `go install fyne.io/fyne/v2/cmd/fyne@latest`
- Android SDK + NDK, with `ANDROID_HOME` and `ANDROID_NDK_HOME` set (the build
  scripts fall back to `~/android-sdk` and a bundled NDK path — adjust for your
  machine)
- The Fyne fork submodule checked out (`git submodule update --init`)
- For the libmpv build: cross-compiled native libs under
  `third_party/android-libs/`. These are **not committed** (large, gitignored);
  generate them once with `./vendor-android-libs.sh` after building libmpv+ffmpeg
  via the [mpv-android](https://github.com/mpv-android/mpv-android) buildscripts
  (see [docs/ANDROID.md](docs/ANDROID.md)). Skip this entirely with `NOMPV=1`.
- `adb` on a connected device to install

**Build the APK(s)** — output lands at `cmd/imgview/imgview.apk` /
`cmd/tie-view/tie-view.apk`:

```sh
git submodule update --init          # first time only: fetch the Fyne fork
./build-android.sh                   # both APKs, arm64-v8a, with libmpv video
./build-android.sh imgview           # just one app
NOMPV=1 ./build-android.sh           # libmpv-free build (no video, no native libs needed)
RELEASE=1 ./build-android.sh         # signed release build (set KEYSTORE/KEYSTORE_PASS/KEY_ALIAS)
```

**Install on a connected device** (uses `adb install -r`):

```sh
./install-android.sh                 # install both APKs
./install-android.sh imgview         # just one
DEVICE=<serial> ./install-android.sh # target a specific device (see: adb devices)
LAUNCH=1 ./install-android.sh        # also launch each app after install
```

**Build and install in one step:**

```sh
./build-install-android.sh           # build + install both
./build-install-android.sh tie-view   # just one
LAUNCH=1 ./build-install-android.sh  # build, install, launch
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

### tie-view — Network Content Viewer

```sh
# Query images by tag
tie-view -tag favorite
tie-view -tag vacation -tag 2024

# Use specific tie config
tie-view -config production.toml -tag work
tie-view -c prod -tag work  # short form

# Use specific filehost
tie-view -host fast -tag recent
```

**tie-view features:**
- Tag filtering with include/exclude semantics
- Co-tag refinement (faceted search)
- Virtual filesystem navigation
- Image tagging panel (tap image or swipe up on mobile)
- Profile switching (manage multiple tie instances)
- Cached thumbnail storage in tie metadata
- Server-cached archive cover thumbnails — the first image inside an archive is extracted once, then shared by every machine

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
| `Backspace` | Parent directory |
| `N` | Toggle filename labels |

Pages are switched via the page links in the bottom bar or the large **Load Next Page ▼** button at the end of the grid.

### Single Image View

| Key | Action |
|-----|--------|
| `Esc` | Return to gallery |
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
| Horizontal swipe on a folder/archive tile | Cycle through its images |
| Horizontal swipe on a video tile | Cycle through frame previews |

**Mobile-specific:**
- Android Back button returns to gallery
- Swipe up on image opens tag panel (tie-view)
- Soft keyboard suppression for optimal viewing

## Architecture

### Repository Structure

```
imgview/
├── cmd/
│   ├── imgview/        # Local filesystem viewer entry point
│   └── tie-view/        # Tie network viewer entry point
├── gallery/            # Shared library (rendering engine)
│   ├── gallery.go      # Gallery controller
│   ├── imageview.go    # Single-image display widget
│   ├── tilelayout.go   # Justified row layout engine
│   ├── imageinfo.go    # Per-item data model
│   ├── apphelper.go    # Shared app bootstrap
│   ├── platform.go     # Mobile vs desktop abstraction
│   ├── extension.go    # Extension API documentation
│   └── config.go       # Configuration management
├── tagselection/       # Tag picker widget (tie-view)
├── mpvplayer/          # libmpv video player integration
├── third_party/fyne/   # Vendored Fyne fork (git submodule, "imgview" branch)
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
go build ./cmd/imgview && go build ./cmd/tie-view  # Build verification
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

**tie-view connection issues:**
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
**Repository:** [github.com/uidbz/tie-gui](https://github.com/uidbz/tie-gui)
