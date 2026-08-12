# Quick Start Guide

Get imgview/tieview running in 5 minutes.

## Prerequisites

- **Go 1.18+** — Check with `go version`
- **C compiler** — gcc (Linux), MinGW (Windows), or Xcode (macOS)
- **Fyne dependencies** — See platform-specific requirements below

### Linux (Debian/Ubuntu)

```sh
sudo apt-get install gcc libgl1-mesa-dev xorg-dev
```

### Linux (Fedora/RHEL)

```sh
sudo dnf install gcc mesa-libGL-devel libXcursor-devel libXrandr-devel \
                 libXinerama-devel libXi-devel libXxf86vm-devel
```

### macOS

```sh
xcode-select --install
```

### Windows

Install MinGW-w64 from [mingw-w64.org](https://mingw-w64.org) or TDM-GCC.

---

## Installation

### Option 1: Install Latest Release

```sh
go install git.sr.ht/~uid/imgview/cmd/imgview@latest
go install git.sr.ht/~uid/imgview/cmd/tieview@latest
```

### Option 2: Build from Source

```sh
# Clone repository
git clone https://git.sr.ht/~uid/imgview
cd imgview

# Build both applications
go build ./cmd/imgview
go build ./cmd/tieview

# Run tests
go test ./...

# Optional: Install to $GOPATH/bin
go install ./cmd/imgview
go install ./cmd/tieview
```

### Verify Installation

```sh
imgview --help   # Should show usage
tieview --help   # Should show usage
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

### tieview — Network Content (Requires tie)

**Note:** tieview requires a [tie](https://git.sr.ht/~uid/tie) daemon. Skip this section if you don't have tie set up.

```sh
# Query by tag
tieview -tag favorite

# Multiple tags (AND)
tieview -tag vacation -tag 2024

# Custom config
tieview -config production.toml -tag work
```

**Expected:** Gallery window opens showing images matching tag query.

**Features:**
- Left sidebar: tag filtering with co-tag refinement
- Click image to view fullscreen
- Tap image (or swipe up on mobile) to open tag panel
- Add/remove tags, toggle favorites (★/☆)

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
ShowGallery = ["Escape", "Backspace"]
ToggleFullScreen = ["F"]
```

See [gallery/config.toml](../gallery/config.toml) for all options.

---

## Common Use Cases

### Browse Photos by Directory

```sh
imgview ~/Pictures/Vacation2024
```

**Tips:**
- Navigate subdirectories by clicking folder tiles
- `PageUp/PageDown` for large collections
- `S` to zoom to original size
- `X` to fit to window

### View Comic Archives

```sh
imgview ~/Comics/manga.cbz
```

**Supported formats:** zip, rar, tar, 7z, cbr, cbz

### Tag-Based Image Organization (tieview)

```sh
# Find untagged images
tieview -tag untagged

# Exclude tag (images NOT tagged 'archived')
tieview -tag !archived

# Complex query
tieview -tag work -tag 2024 -tag !draft
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
| `PageDown` | Next page |
| `PageUp` | Previous page |
| `Q`, `Esc` | Quit |

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
| `Esc`, `Backspace` | Gallery |

### Mouse/Touch

| Input | Action |
|-------|--------|
| Scroll wheel | Zoom |
| Drag | Pan |
| Double-click | Fullscreen |
| Right-click tile (tieview) | De-import |

---

## Troubleshooting

### Build Errors

**Error:** `gcc: command not found`
- **Fix:** Install C compiler (see Prerequisites)

**Error:** `Package 'gl' not found`
- **Fix:** Install OpenGL development headers (see Prerequisites)

**Error:** `undefined reference to XCreateWindow`
- **Fix:** Install X11 development headers (see Prerequisites)

### Runtime Issues

**Performance slow on large images**
- **Fix:** Images >12MP may be slow to zoom. Downscale source images or wait for caching.

**Thumbnails not caching**
- **Fix:** Check `ThumbnailDir` path in config. Ensure write permissions.

**tieview: connection refused**
- **Fix:** Ensure tie daemon is running. Check config file path with `-config`.

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
- Set up tieview with your tie instance
- Enable thumbnail caching for faster browsing

### For Developers

- Read [ARCHITECTURE.md](ARCHITECTURE.md) for system design
- See [gallery/extension.go](../gallery/extension.go) for extension API
- Review [refactoring-progress.md](refactoring-progress.md) for recent improvements
- Check [CLAUDE.md](../CLAUDE.md) for comprehensive codebase reference

### Getting Help

- **Bug reports:** [Issue Tracker](https://todo.sr.ht/~uid/imgview)
- **Questions:** Check existing issues or create new one
- **Contributing:** See README.md development section

---

## Quick Reference Card

```
┌─────────────────────────────────────────────────────────┐
│ imgview Quick Reference                                  │
├─────────────────────────────────────────────────────────┤
│ GALLERY VIEW              │ IMAGE VIEW                  │
│   J/Down    Scroll down   │   J/Right   Next image      │
│   K/Up      Scroll up     │   K/Left    Previous image  │
│   PageDown  Next page     │   H/Up      Rotate left     │
│   PageUp    Prev page     │   L/Down    Rotate right    │
│   Q/Esc     Quit          │   S         Original size   │
│                           │   X         Fit window      │
│                           │   F         Fullscreen      │
│                           │   Esc       Back to gallery │
├─────────────────────────────────────────────────────────┤
│ COMMAND LINE                                            │
│   imgview [path]          View directory or image       │
│   imgview -c cfg.toml     Use custom config             │
│   tieview -tag name       Query by tag                  │
├─────────────────────────────────────────────────────────┤
│ CONFIG: ~/.config/imgview/config.toml                   │
│ CACHE:  ~/.cache/imgview/                               │
└─────────────────────────────────────────────────────────┘
```

Print this card for quick reference while learning the application.
