# Android builds

imgview and tieview both build as Android APKs. There are two levels of
support:

- **Fast path (implemented):** an APK with everything *except* libmpv video
  playback. This is what `./build-android.sh` produces today.
- **Full path (not yet done):** an APK with hardware-accelerated video playback
  backed by a libmpv cross-compiled for Android. This document describes what
  that would take.

## Fast path — how it works today

libmpv is a native (cgo) dependency linked with `-lmpv`. Android devices have no
system libmpv, so on Android we compile the mpv integration *out*:

- `mpvplayer/mpv.go`, `mpvplayer/screenshot.go`, and the `nativedisplay_*.go`
  files carry a `//go:build !nompv && !android` constraint. Note that Go sets
  the `linux` build tag on Android too, so the `!android` term is what stops the
  GLFW-based `nativedisplay_both.go` from being compiled there.
- `mpvplayer/mpv_stub.go` (`//go:build nompv || android`) provides the same
  public API — `MPVPlayer`, `NewMPVPlayer`, `ExtractFrame`,
  `ExtractFrameFromReader` — as no-ops. `NewMPVPlayer` returns an error and the
  frame extractors return `nil`, which callers already handle by falling back to
  a placeholder thumbnail.

The same `nompv` tag lets you make a **desktop** build without libmpv:

```sh
go build -tags nompv ./...
```

Build the APKs:

```sh
git submodule update --init          # fetch the fyne fork (third_party/fyne)
./build-android.sh                   # both APKs, android/arm64
./build-android.sh imgview           # just one
RELEASE=1 ./build-android.sh         # signed release build
```

## Touch gestures & mobile UX

On mobile imgview behaves like a phone photo gallery. All of the touch code is
gated behind `fyne.CurrentDevice().IsMobile()`, so the desktop experience
(free-drag pan, scroll-wheel zoom, keyboard navigation) is completely unchanged.

### Gestures

- **Swipe left / right** — page to the next / previous image.
- **Pinch** — zoom toward the midpoint between the two fingers (focal-point
  zoom, the same idea as desktop scroll-zoom-toward-cursor).
- **Drag while zoomed in** — pan the image, clamped so it can't be pulled past
  its own edges.
- **Drag past the edge (or a fast flick)** — once the image is at a border, the
  extra drag "overscrolls" and pages to the next/previous image.

These live on `ImageView` in `gallery/imageview.go`:

- `ImageView` implements `mobile.Touchable` (`TouchDown`/`TouchUp`/
  `TouchCancel`) and `mobile.Movable` (`TouchMoved`). It tracks each finger by
  its `TouchEvent.ID` in the `touches` map, so a two-finger pinch can be
  distinguished from a one-finger drag.
- `Dragged`/`DragEnd` branch to `draggedMobile`/`dragEndMobile` on mobile.
  `panBounds` computes the clamp; horizontal drag past a bound accumulates into
  `swipeAccum`, and `dragEndMobile` pages when that exceeds a threshold (20% of
  width when the image fits, 40% when zoomed in — a firmer throw so panning
  doesn't accidentally flip). Fast flicks page for free via Fyne's post-release
  drag momentum.
- Paging is wired by `Viewer.ChangeImage`, which sets `img.nextFn`/`img.prevFn`
  to `ChangeImage(NextImage()/PrevImage())` — the same navigation the keyboard
  hotkeys use.

### Performance notes

Pinch zoom resizes the image widget every frame. Two things keep it smooth:

- **Direct resize, not `Refresh`.** During a live pinch, `TouchMoved` calls
  `iv.Resize`/`iv.Move` directly rather than `container.Refresh()`. A full
  refresh cascades into `canvas.Image.Refresh()` and frees/re-uploads the GL
  texture every frame. The high-quality state (zoom-% title, texture) is synced
  once when the pinch ends in `TouchUp`.
- **Downscale on load (`downscaleForMobile`).** Full-resolution phone photos are
  12MP+ (~48MB of RGBA); re-uploading that texture per frame is the dominant
  cost. On mobile the decoded image is scaled so its longest edge is ≤ 2× the
  screen's longest edge — a few MB, with headroom to stay sharp when zooming in.
  The GPU (`ImageScaleFastest`) handles the actual zoom scaling. Desktop keeps
  full resolution.

### Loading images — the folder picker

Mobile apps get no command-line arguments and no accessible working directory,
so imgview starts with an empty gallery. When that happens (`cmd/imgview/main.go`
detects `IsMobile() && viewer.ImageCount() == 0`), it shows a **Select folder**
button that opens the Android document chooser (`dialog.ShowFolderOpen`).

Picked images come back as `content://` URIs, not filesystem paths, so they are
loaded through the gallery's existing `CustomReader` pipeline: `uriReader`
(in `cmd/imgview/main.go`) wraps a `fyne.URI`, reading it once into memory
because `GetReader` must return a seekable stream and content URIs aren't
re-openable like `os` files. `readersFromFolder` lists the folder, filters by
image extension, and sorts by name.

## Full path — video on Android

Getting real libmpv playback on Android is substantial work in three areas:
building the native libraries, replacing the desktop GPU glue, and wiring it all
into the build. None of this is done yet.

### 1. Cross-compile libmpv (+ ffmpeg) for each Android ABI

libmpv is not distributed for Android; you build it from source against the NDK.

- Build ffmpeg first (libmpv depends on it), then libmpv, for every ABI you
  ship: at minimum `arm64-v8a`, likely also `armeabi-v7a`, `x86_64` for
  emulators.
- Use the NDK toolchain (`aarch64-linux-android<API>-clang`, etc.). The
  practical route is the mpv-android project's build scripts
  (<https://github.com/mpv-android/mpv-android>), which already assemble ffmpeg,
  libmpv, and their dependencies for the NDK — reuse those rather than
  hand-rolling the dependency tree.
- Output is a set of `.so` files per ABI plus the mpv C headers.

### 2. Replace the GLFW/desktop render path

The desktop player is coupled to a desktop OpenGL context and an X11/Wayland
display:

- `mpv.go` initializes mpv's OpenGL render API via
  `MPV_RENDER_PARAM_OPENGL_INIT_PARAMS`, resolving GL symbols through GLFW
  (`goGetProcAddress` → GLFW) and passing a native display from
  `nativedisplay_both.go` (`glfw.GetX11Display()` / `GetWaylandDisplay()`).
- On Android there is no GLFW and no X11/Wayland display. Fyne's mobile driver
  runs on EGL/GLES against an `ANativeWindow`.

A full Android path needs an `android`-tagged implementation that:

- Resolves GLES proc addresses via EGL (`eglGetProcAddress`) instead of GLFW.
- Drops the X11/Wayland display params entirely (they do not apply); mpv's
  Android video output negotiates hardware decode through MediaCodec.
- Feeds frames into the same `canvas.GLVideoRenderer` contract the `Video`
  widget already uses (`RenderInto(fbo, w, h)`, `NeedsPaint()`, `Aspect()`), so
  the widget layer needs no changes.
- Sets mpv options appropriate for Android (`hwdec=mediacodec` or
  `mediacodec-copy`, and an `ao` such as `opensles`/`aaudio`).

The software frame extractor in `screenshot.go` (used for thumbnails, no GL
context required) is the easier half — it should port with just the ffmpeg/mpv
libraries in place and an `android` build tag.

### 3. Bundle the native libraries and wire the build

- The `.so` files must be packaged into the APK under `lib/<abi>/`. `fyne
  package` does not do this for arbitrary native libs, so this likely means a
  post-processing step on the APK (it is a zip) or a Gradle-based packaging flow
  instead of plain `fyne package`.
- Point cgo at the cross-compiled headers/libs for the Android build:
  `CGO_CFLAGS=-I<mpv-headers>`, `CGO_LDFLAGS=-L<abi-lib-dir> -lmpv`, with
  `CC=<ndk-clang>` for the target ABI.
- Introduce an `android`-tagged mpv implementation and *remove* `android` from
  the `!android` constraints on the files that become Android-capable (the
  render path needs an Android variant; the software extractor can share code
  guarded by tags). Keep the `nompv` stub as the escape hatch for libmpv-free
  builds.
- At runtime, load/refer to the bundled `.so` (Android's linker finds libraries
  in the APK's `lib/<abi>/` automatically once packaged correctly).

### Effort estimate

The cross-compile (step 1) and the APK packaging of native libs (step 3) are the
bulk of the work and the most toolchain-fragile parts. The render-path rewrite
(step 2) is contained because the `Video` widget is already decoupled behind the
`GLVideoRenderer` interface — only the mpv-context setup and proc-address
resolution change. Budget days, not hours, mostly for getting the NDK
cross-compilation and APK bundling reproducible.
