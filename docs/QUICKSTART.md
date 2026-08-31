# Quick Start Guide

Get imgview/tie-view running in 5 minutes.

## Prerequisites

- **Go 1.25+** — Check with `go version`
- **C compiler** — gcc (Linux), MinGW (Windows), or Xcode (macOS)
- **Fyne dependencies** — See platform-specific requirements below
- **libmpv** *(optional)* — for video playback and video thumbnails; without it, build with `-tags nompv`

### Linux (Debian/Ubuntu)

```sh
sudo apt-get install gcc libgl1-mesa-dev xorg-dev libmpv-dev
```

### Linux (Fedora/RHEL)

```sh
sudo dnf install gcc mesa-libGL-devel libXcursor-devel libXrandr-devel \
                 libXinerama-devel libXi-devel libXxf86vm-devel mpv-devel
```

### macOS

```sh
xcode-select --install
brew install mpv   # optional, for video support
```

### Windows

Install MinGW-w64 from [mingw-w64.org](https://mingw-w64.org) or TDM-GCC. (Video support needs a libmpv DLL; most Windows users should build with `-tags nompv`.)

---

## Installation

### Build from Source

imgview builds against a modified Fyne fork, vendored as a **git submodule** at `third_party/fyne` — `go install ...@latest` from the module proxy does not work, so build from a clone:

```sh
# Clone repository including the fyne fork submodule (required)
git clone --recurse-submodules https://github.com/uidbz/tie-gui
cd tie-gui

# …or fetch the submodule after a plain clone:
git submodule update --init

# Build the applications
go build ./cmd/imgview
go build ./cmd/tie-view
go build ./cmd/tie-fm
go build ./cmd/tie-audio

# Without libmpv (no video support in imgview/tie-view):
go build -tags nompv ./cmd/imgview ./cmd/tie-view

# Run tests
go test ./...

# Optional: Install to $GOPATH/bin
go install ./cmd/...
```

### Verify Installation

```sh
imgview --help   # Should show usage
tie-view --help   # Should show usage
```

---

## First Run

### imgview — Browse Local Images

```sh
# View current directory
imgview .

# View specific directory
imgview ~/Pictures

# View single image
imgview ~/Pictures/photo.jpg

# Open archive
imgview ~/Downloads/comics.cbr
```

**Expected:** Gallery window opens showing thumbnails in justified rows.

**Navigation:**
- Click image to view fullscreen
- `Esc` to return to gallery
- `Left/Right` arrows to navigate images
- `Q` or `Esc` in gallery to quit

### tie-view — Network Content (Requires tie)

**Note:** tie-view requires a [tie](https://github.com/uidbz/tie) triplestore. Skip this section if you don't have tie set up.

```sh
# Query by tag
tie-view -tag favorite

# Multiple tags (AND)
tie-view -tag vacation -tag 2024

# Custom config
tie-view -config production.toml -tag work
```

**Expected:** Gallery window opens showing images matching tag query.

**Features:**
- Left sidebar: tag filtering with co-tag refinement
- Click image to view fullscreen
- Tap image (or swipe up on mobile) to open tag panel
- Add/remove tags, toggle favorites (★/☆)

### tie-fm — File Manager

```sh
tie-fm
```

**Expected:** Twin-panel window. Browse local paths on one side and `tie:`
locations on the other.

**Features:**
- Tag panel filters listings by include/exclude tags; switches to related
  (co-occurring) tags once a filter is chosen
- Select rows to view/edit their tags (directories are taggable too)
- ☆/★ favorite tags, stored in the tie collection and shared with tie-view
- Copy/move/delete with progress view; USB drives and MTP phones under
  removable devices (Linux)

**Removable-device dependencies (Linux, optional):**
`libmtp-dev pkg-config` at build time; `udisks2` and
`gvfs-backends gvfs-fuse` at runtime. Without them tie-fm still builds and
runs — MTP devices simply report as unsupported.

### tie-audio — Music in tie

```sh
tie-audio
```

**Expected:** Player window; browse the collection's audio by tag and play.

---

## Configuration

### Create Config File

```sh
# Linux/macOS
mkdir -p ~/.config/imgview
cp gallery/config.toml ~/.config/imgview/config.toml

# Windows
mkdir %AppData%\imgview
copy gallery\config.toml %AppData%\imgview\config.toml
```

### Customize Settings

Edit the config file to change:

```toml
[General]
TileWidth = 300        # Increase for larger thumbnails
TileGap = 5            # Space between tiles
Workers = 8            # Parallel thumbnail loaders
ImagesPerPage = 500    # Images per page
DefaultWidth = 1200    # Initial window size
DefaultHeight = 800

[Image]
# Customize keyboard shortcuts
NextImage = ["Right", "J"]
PreviousImage = ["Left", "K"]
ShowGallery = ["Escape"]
FullScreen = ["F"]
```

See [gallery/config.toml](../gallery/config.toml) for all options.

---

## Common Use Cases

### Browse Photos by Directory

```sh
imgview ~/Pictures/Vacation2024
```

**Tips:**
- Folder/archive tiles show a content thumbnail with a folder badge — click to open
- **Swipe horizontally** on a folder/archive tile to preview its other images; on a video tile to cycle frame captures
- Use the bottom-bar page links for large collections
- `S` to zoom to original size
- `X` to fit to window

### View Comic Archives

```sh
imgview ~/Comics/manga.cbz
```

**Supported formats:** zip, rar, tar, 7z, cbr, cbz

### Tag-Based Image Organization (tie-view)

```sh
# Find untagged images
tie-view -tag untagged

# Exclude tag (images NOT tagged 'archived')
tie-view -tag !archived

# Complex query
tie-view -tag work -tag 2024 -tag !draft
```

**Workflow:**
1. Query images by tag
2. Click image to view
3. Swipe up (mobile) or tap (desktop) to open tag panel
4. Add/remove tags
5. Navigate to next image (tags auto-sync)

### Video Files

```sh
imgview ~/Videos  # Shows video thumbnails with play icon
```

**Note:** Requires libmpv installed. Click thumbnail to play.

---

## Keyboard Reference

### Gallery View

| Key | Action |
|-----|--------|
| `J`, `Down` | Scroll down |
| `K`, `Up` | Scroll up |
| `Backspace` | Parent directory |
| `N` | Toggle filename labels |
| `Q`, `Esc` | Quit |

(Pages are switched via the bottom-bar page links or the **Load Next Page ▼** button.)

### Image View

| Key | Action |
|-----|--------|
| `J`, `Right` | Next image |
| `K`, `Left` | Previous image |
| `H`, `Up` | Rotate left |
| `L`, `Down` | Rotate right |
| `S` | Original size |
| `X` | Fit to window |
| `F` | Fullscreen |
| `B` | Toggle filtering |
| `Esc` | Gallery |

### Mouse/Touch

| Input | Action |
|-------|--------|
| Scroll wheel | Zoom |
| Drag | Pan |
| Double-click | Fullscreen |
| Right-click tile (tie-view) | De-import |

---

## Troubleshooting

### Build Errors

**Error:** `replacement directory ./third_party/fyne does not exist`
- **Fix:** The Fyne fork is a git submodule: `git submodule update --init`

**Error:** `gcc: command not found`
- **Fix:** Install C compiler (see Prerequisites)

**Error:** `Package 'gl' not found`
- **Fix:** Install OpenGL development headers (see Prerequisites)

**Error:** `cannot find -lmpv` / `Package 'mpv' not found`
- **Fix:** Install libmpv development files (see Prerequisites), or build without video support: `go build -tags nompv ./cmd/imgview`

**Error:** `undefined reference to XCreateWindow`
- **Fix:** Install X11 development headers (see Prerequisites)

### Runtime Issues

**Performance slow on large images**
- **Fix:** Images >12MP may be slow to zoom. Downscale source images or wait for caching.

**Thumbnails not caching**
- **Fix:** Check `ThumbnailDir` path in config. Ensure write permissions.

**tie-view: connection refused**
- **Fix:** Ensure tie triplestore is running. Check config file path with `-config`.

**Window too small on HiDPI**
- **Fix:** Fyne auto-detects scaling. Override with `FYNE_SCALE=1.5` environment variable.

### Platform-Specific

**macOS: "imgview cannot be opened because the developer cannot be verified"**
- **Fix:** Right-click → Open, or `xattr -d com.apple.quarantine imgview`

**Windows: Missing DLL errors**
- **Fix:** Ensure MinGW bin directory is in PATH

**Linux Wayland: Fullscreen doesn't work**
- **Known issue:** Fyne fullscreen has limitations on Wayland. Use X11 or rebuild with `-tags wayland`.

---

## Next Steps

### For Users

- Customize keyboard shortcuts in config file
- Set up tie-view with your tie instance
- Enable thumbnail caching for faster browsing

### For Developers

- Read [ARCHITECTURE.md](ARCHITECTURE.md) for system design
- See [gallery/extension.go](../gallery/extension.go) for extension API
- Check [CLAUDE.md](../CLAUDE.md) for comprehensive codebase reference

### Getting Help

- **Bug reports:** [GitHub Issues](https://github.com/uidbz/tie-gui/issues)
- **Questions:** Check existing issues or create new one

---

## Quick Reference Card

```
┌─────────────────────────────────────────────────────────┐
│ imgview Quick Reference                                  │
├─────────────────────────────────────────────────────────┤
│ GALLERY VIEW              │ IMAGE VIEW                  │
│   J/Down    Scroll down   │   J/Right   Next image      │
│   K/Up      Scroll up     │   K/Left    Previous image  │
│   Backspace Parent dir    │   H/Up      Rotate left     │
│   N         Filenames     │   L/Down    Rotate right    │
│   Q/Esc     Quit          │   S         Original size   │
│                           │   X         Fit window      │
│  Swipe left/right on a    │   F         Fullscreen      │
│  folder/archive/video     │   Esc       Back to gallery │
│  tile: cycle preview      │                             │
├─────────────────────────────────────────────────────────┤
│ COMMAND LINE                                            │
│   imgview [path]          View directory or image       │
│   imgview -c cfg.toml     Use custom config             │
│   tie-view -tag name       Query by tag                  │
├─────────────────────────────────────────────────────────┤
│ CONFIG: ~/.config/imgview/config.toml                   │
│ CACHE:  ~/.cache/imgview/                               │
└─────────────────────────────────────────────────────────┘
```

Print this card for quick reference while learning the application.
