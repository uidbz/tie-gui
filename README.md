# tie-gui — GUI clients for tie

Graphical applications for [tie](https://github.com/uidbz/tie), a personal
information-management system built on a triple store and content-addressed
file storage. tie organizes data by **tags** instead of fixed directory
hierarchies: files live in a content-addressed blob store, their tags and
metadata live in the triple store, and the content hash joins the two.

These clients are how you browse, view, play, and organize that data.

## Applications

| App | What it does |
|-----|--------------|
| **imgview** | Local-filesystem image viewer. Directory browsing, archive support (zip, rar, tar, 7z, cbr, cbz), and video playback via libmpv. Works without tie. |
| **tie-view** | Network image viewer for tie collections. Tag filtering with include/exclude semantics, co-tag refinement, in-app image tagging, virtual filesystem navigation, server-cached thumbnails. |
| **tie-fm** | Twin-panel file manager. Local files and `tie:` locations side by side; copy/move/delete locally, tag files *and directories* in tie, browse USB drives and MTP phones. |
| **tie-audio-player** | Tag-driven audio player for tie collections. |

All four are built on the [Fyne](https://fyne.io) toolkit and run on Linux,
Windows, macOS, and Android.

## Repository layout

```
cmd/
├── imgview/            local image viewer entry point
├── tie-view/           tie network image viewer entry point
├── tie-fm/             twin-panel file manager entry point
│   └── internal/       config, fs providers (local/tie/mtp), UI, table widget
└── tie-audio-player/   audio player entry point
gallery/                shared gallery library: layout engine, tile widget,
                        image view, platform abstraction, config
tagselection/           tag-picker widget (used by tie-view and tie-fm)
mpvplayer/              libmpv video player widget
third_party/fyne/       vendored Fyne fork (git submodule, "imgview" branch)
docs/                   user and developer documentation
```

The Fyne fork adds `canvas.GLVideo` (libmpv video embedding), Android
system-bar control, and platform-tuned texture-cache lifetimes. It is
vendored as a **git submodule**; `go.mod` points at it via a `replace`
directive, so the submodule must be checked out before building.

## Building

**Requirements:**

- Go 1.25+ and a C compiler (Fyne requires CGo)
- Fyne system dependencies — on Debian/Ubuntu: `gcc libgl1-mesa-dev xorg-dev`
  (see [Fyne Getting Started](https://developer.fyne.io/started))
- **libmpv** (optional) — video playback and video thumbnails in
  imgview/tie-view; Debian/Ubuntu: `libmpv-dev`. Omit with `-tags nompv`.
- tie-fm only: for removable-device browsing on Linux, `libmtp-dev
  pkg-config` plus udisks2/gvfs at runtime — see
  [docs/QUICKSTART.md](docs/QUICKSTART.md). Not needed for basic local/tie
  use.

**Clone and build:**

```sh
git clone --recurse-submodules https://github.com/uidbz/tie-gui
cd tie-gui

# …or, after a plain clone:
git submodule update --init

go build ./cmd/imgview            # local image viewer
go build ./cmd/tie-view           # tie network viewer
go build ./cmd/tie-fm             # file manager
go build ./cmd/tie-audio-player   # audio player

# Without libmpv (no video):
go build -tags nompv ./cmd/imgview ./cmd/tie-view

# Run tests:
go test ./...
```

> **Note:** `go install github.com/uidbz/tie-gui/cmd/imgview@latest` does
> **not** work — the `replace` directive points into the git submodule,
> which module-proxy downloads do not contain. Build from a clone as above.

**Android:** imgview, tie-view, and tie-audio-player package as arm64-v8a
APKs (the viewers with in-app libmpv playback; the audio player is a plain
remote-client build). See [docs/ANDROID.md](docs/ANDROID.md).

## Usage

### imgview — local images

```sh
imgview /path/to/directory      # browse a folder
imgview photo.jpg               # view one image
imgview comics.cbz              # open an archive
imgview -config my-config.toml  # custom config
```

### tie-view — images in tie

```sh
tie-view -tag favorite                    # query by tag
tie-view -tag vacation -tag 2024          # AND multiple tags
tie-view -tag '!archived'                 # exclude a tag
tie-view -c production.toml -tag work     # specific tie config
```

Features: tag sidebar with co-tag refinement, in-app image tag panel (tap
the image), profile switching across tie instances, server-cached archive
covers and thumbnails.

### tie-fm — files and tags together

```sh
tie-fm    # opens twin panels; local default on the left, tie on the right
```

Browse local paths and `tie:` tag queries side by side. The tag panel
filters the listing (include/exclude tags, co-tag suggestions) or shows and
edits the tags of the current selection — directories are taggable too.
Favorite tags (☆/★) are stored in the tie collection and shared with the
other clients. Copy/move progress is shown in-app; USB storage and MTP
phones appear under removable devices (Linux).

### tie-audio-player — music in tie

```sh
tie-audio-player    # browse and play the collection's audio by tag
```

## Configuration

- **imgview / tie-view:** `~/.config/imgview/config.toml` (Linux),
  `%AppData%\imgview\config.toml` (Windows). Defaults in
  [gallery/config.toml](gallery/config.toml) — tile size, page size,
  thumbnail cache, hotkeys.
- **tie-fm:** `~/.config/tie-fm/config.toml` — bookmarks, per-filetype
  applications, and which tie client config to use.
- **tie connection:** the tie clients use the standard tie client config
  (`~/.config/tie/config.toml`); see the
  [tie README](https://github.com/uidbz/tie#filehosts-and-config).

## Documentation

- **[docs/QUICKSTART.md](docs/QUICKSTART.md)** — install, first run,
  keyboard reference, troubleshooting
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — gallery library design,
  extension API, threading model
- **[docs/ANDROID.md](docs/ANDROID.md)** — Android build internals
- **[docs/OPTIMIZATIONS.md](docs/OPTIMIZATIONS.md)** — memory and
  performance notes
- **[docs/README.md](docs/README.md)** — full documentation index
- **[CLAUDE.md](CLAUDE.md)** — detailed codebase reference (LLM-oriented)

## License

The monorepo is GPL-3 licensed; see [LICENSE](LICENSE). The tie-fm subtree
retains its original BSD 3-Clause license (`cmd/tie-fm/LICENSE`), and its
bundled KDE Breeze icons are LGPL-3+ (`cmd/tie-fm/internal/ui/icons/LICENSE`).
