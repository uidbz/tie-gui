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
