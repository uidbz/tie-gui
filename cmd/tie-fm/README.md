# tie-fm

A twin-panel desktop file manager for local files and the [tie](https://github.com/uidbz/tie)
tagging filesystem, built with [Fyne](https://fyne.io/). Part of the
[tie-gui](https://github.com/uidbz/tie-gui) monorepo.

tie-fm browses two kinds of location side by side:

- **Local files** — ordinary paths (`/home/you/…` or `file:` URIs).
- **tie** — the tie tagging store, addressed with `tie:` URIs. Instead of a
  fixed directory tree, tie content is discovered by **tags**: a dedicated tag
  panel filters the listing by include/exclude tag sets, and files *and
  directories* can be tagged.

## Features

- Twin panels with independent navigation history, breadcrumb, and a favorites
  sidebar (bookmarks).
- Local file operations: copy, move, and delete (local-to-local), with a
  progress view.
- tie browsing with a tag panel that swaps between two modes:
  - **Filter by tags** when nothing is selected — pick include/exclude tags to
    query the listing. The quick-pick list shows your **favorite tags**, and
    switches to **related (co-occurring) tags** once a filter tag is chosen.
  - **Tags of selection** when one or more rows are selected — view and edit the
    tags of the selection, with a **Close** button to clear it and return to the
    filter. Directories are taggable, not just files.
- **Favorite tags:** click the ☆/★ toggle beside a tag to pin/unpin it.
  Favorites are stored in the tie collection (shared with other tie clients such
  as imgview), so they follow the collection rather than the machine.
- **Single-click, multi-select navigation:** click a row to open a file or enter
  a directory; click a row's **icon** to toggle it in the selection. Selecting
  several files shows the tags **common to all** of them — adding a tag applies
  it to every selected file, removing it removes it from all.
- **Removable devices** (Linux): browse and transfer files on USB storage
  (via udisks2) and MTP phones/cameras (via libmtp, with a gvfs fallback), with
  pausable/stoppable copy progress. See [Requirements](#requirements) for the
  extra dependencies.
- File-type **icons** from the bundled KDE Breeze set, in light and dark
  variants following the current theme.
- Configurable **per-filetype applications** for opening files, falling back to
  `xdg-open`.
- Selectable tie client config at runtime (defaults to a local test server).

## Requirements

- Go 1.25+ and a C compiler (the monorepo's vendored Fyne fork requires CGo).
  Build from the repository root: `go build ./cmd/tie-fm` — see the
  [root README](../../README.md#building).
- Linux with `xdg-open` for the default file-open behavior.
- A running tie daemon is only needed to browse `tie:` locations; local
  browsing works without it.

### Removable devices (USB storage & phones)

Browsing USB drives and MTP devices (phones, cameras) is a Linux-only feature
with extra dependencies:

- **CGO** must be enabled (a C compiler such as `gcc` or `clang`) and
  **libmtp** development headers + `pkg-config` must be installed, to build the
  direct MTP backend. Debian/Ubuntu: `libmtp-dev pkg-config`; Fedora:
  `libmtp-devel pkgconf-pkg-config`; Arch: `libmtp pkgconf`.
- **udisks2** (system D-Bus service) is used at runtime to detect, mount, and
  eject USB block devices (sticks, SD cards).
- **gvfs** with the MTP backend and `gvfsd-fuse`, plus the `gio` command, are
  used at runtime as an MTP fallback when a resident desktop daemon holds the
  device and libmtp cannot claim it. Debian/Ubuntu: `gvfs-backends gvfs-fuse`.

Without libmtp/CGO the app still builds and runs — MTP devices simply report
that they are unsupported in that build; USB block devices and the gvfs
fallback (if present) still work.

## Build & run

From the repository root (requires the Fyne-fork submodule — see the
[root README](../../README.md#building)):

```sh
go build ./cmd/tie-fm        # CGO_ENABLED=1 by default when a C compiler is present
go run ./cmd/tie-fm          # or ./tie-fm after building
go test ./cmd/tie-fm/...
```

To build without the MTP backend (no libmtp needed; note the vendored Fyne
fork still requires CGo for OpenGL):

```sh
go build -tags nomtp ./cmd/tie-fm
```

## Configuration

tie-fm keeps its own settings (separate from the tie client config) at
`$XDG_CONFIG_HOME/tie-fm/config.toml`, created on first run. It stores:

- `TieConfig` — path to the tie client config to use (empty ⇒ built-in local
  default: daemon `:1161`, filehost `:1162`).
- `Bookmarks` — the favorites sidebar entries (directory bookmarks).
- `FileApps` — per-extension open commands (see below).

Favorite *tags* are not kept here — they live in the tie collection itself (the
`("tags","favorite",<tag>)` triple), shared across tie clients.

The tie client config itself is a separate TOML file; select one at runtime via
**tie-fm → Select tie config…**, or use the built-in local-server default.

### File associations

Set which program opens each file type from **tie-fm → File associations…**, or
right-click a file and choose **Open with…**. Associations are keyed by file
extension. A command may contain `%f` (replaced by the file path); if `%f` is
absent the path is appended as the final argument. Arguments are split on
whitespace (no shell). Files without a matching association open with
`xdg-open`.

Example `config.toml` fragment:

```toml
[FileApps]
pdf = "okular %f"
mkv = "mpv %f"
png = "gimp"
```

## Layout

```
main.go                      window, menu, sidebar, wiring
internal/config              persisted settings (bookmarks, tie config, file apps)
internal/fs                  filesystem providers: local + tie (TagStore), operations
internal/ui                  file-manager panel, tag panel, file-op view, icons, launcher
internal/ui/icons            vendored KDE Breeze mimetype icons (light + dark)
internal/widget/tablewidget  paginated, sortable table widget
```

The tag-selector widget is shared with the other tie clients and lives at the
repository root: [`tagselection/`](../../tagselection).

## Licensing

tie-fm is BSD 3-Clause licensed; see [LICENSE](LICENSE).

The bundled icons under `internal/ui/icons/` are from the KDE Breeze icon theme
and are licensed LGPL-3+; their license and attribution are kept alongside them
(`internal/ui/icons/LICENSE` and `internal/ui/icons/COPYRIGHT`).
