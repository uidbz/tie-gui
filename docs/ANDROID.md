# Android builds

imgview and tie-view both build as Android APKs with in-app libmpv video
playback (arm64-v8a). `./build-android.sh` links against a cross-compiled
libmpv+ffmpeg vendored in `third_party/android-libs/` and bundles those native
libraries into the APK. A libmpv-free build is available via `NOMPV=1`.

tie-audio also packages as an APK through the same script, but as a
remote client (it controls a pwplay-server over HTTP) it bundles no native
libraries — a plain `fyne package` build.

## How the build works

libmpv is a native (cgo) dependency linked with `-lmpv`. Android has no system
libmpv, so we vendor a cross-compiled one and bundle it into the APK:

- `mpvplayer/mpv.go` and `mpvplayer/screenshot.go` carry `//go:build !nompv`, so
  they compile on Android. GL proc-address resolution and mpv options are
  factored into per-platform files: `mpvplayer/platform_desktop.go`
  (`//go:build !nompv && !android`, GLFW resolver) and
  `mpvplayer/platform_android.go` (`//go:build android && !nompv`, EGL resolver
  via `eglGetProcAddress`, `hwdec=mediacodec-copy` hardware decode, `ao=opensles`).
- `mpvplayer/mpv_stub.go` (`//go:build nompv`) provides the same public API as
  no-ops for libmpv-free builds. `NewMPVPlayer` returns an error and the frame
  extractors return `nil`, which callers handle by falling back to a placeholder
  thumbnail.

The `nompv` tag also makes a **desktop** build without libmpv:

```sh
go build -tags nompv ./...
```

Build the APKs:

```sh
git submodule update --init          # fetch the fyne fork (third_party/fyne)
./build-libmpv-android.sh            # first time: cross-compile + vendor libmpv
./build-android.sh                   # all three APKs, android/arm64, libmpv viewers
./build-android.sh imgview           # just one (imgview, tie-view, tie-audio)
NOMPV=1 ./build-android.sh           # libmpv-free viewer build (no video)
RELEASE=1 ./build-android.sh         # signed release build
```

`build-android.sh` exports `CGO_CFLAGS=-I third_party/android-libs/include` and
`CGO_LDFLAGS=-L third_party/android-libs/arm64-v8a` before `fyne package`, then
runs `bundle-native-libs.sh` to inject the `.so` files and re-sign (see below).

## Toolchain discovery (no hardcoded paths)

All the Android scripts source `android-env.sh`, which finds the Android SDK,
NDK and a working JDK on any machine instead of hardcoding one developer's
home directory. Order of preference:

- **SDK** — `$ANDROID_HOME`/`$ANDROID_SDK_ROOT` if valid, else common locations
  (`~/android-sdk`, `~/Android/Sdk`, `~/Library/Android/sdk`,
  `/opt/android-sdk-update-manager`, `/opt/android-sdk`, `/usr/lib/android-sdk`).
- **NDK** — `$ANDROID_NDK_HOME`/`$ANDROID_NDK_ROOT` if valid, else an NDK
  installed inside the SDK (`$ANDROID_HOME/ndk/*`, `ndk-bundle`), else a
  standalone NDK under `/apps/android-ndk-*`, `/opt/android-ndk-*`,
  `~/android-ndk-*` (newest wins).
- **JDK** — verifies `$JAVA_HOME` actually runs; if not (e.g. a Gentoo
  `java-config` user VM pointing at a removed JDK), it falls back to
  `/usr/libexec/java_home`, `java-config -O`, or a working JVM under
  `/usr/lib/jvm` / `/usr/lib64/jvm`, and puts its `bin/` first on `PATH`. This
  is why `apksigner`/`keytool` sign correctly even when the system default
  `java` wrapper is misconfigured.

Override any of them by exporting `ANDROID_HOME`, `ANDROID_NDK_HOME` or
`JAVA_HOME` before running a script.

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

Pinch zoom resizes the image widget every frame. Four things keep it smooth:

- **Fork patch: `canvas.Image.Resize` does not invalidate the texture for
  `ImageScaleFastest`/`ImageScalePixels`.** Upstream Fyne queues a texture
  refresh on every resize, so a resize per pinch frame meant deleting and
  re-uploading the whole bitmap each frame (plus a CPU pixel conversion). With
  those scale modes the uploaded texture holds the source pixels unchanged, so
  the fork only requests a repaint and the GPU rescales the cached texture
  (`third_party/fyne/canvas/image.go`). A pinch frame is now one textured quad.
- **Direct resize, not `Refresh`.** During a live pinch, `TouchMoved` calls
  `iv.Resize`/`iv.Move` directly rather than `container.Refresh()`. The zoom-%
  title and layout state are synced once when the pinch ends (`endPinch`).
- **Upload-ready bitmaps.** `LoadImage` and `decodeFullImage` convert the decoded
  image to `*image.RGBA` (`toRGBA`), the only type the GL painter uploads
  without a full-bitmap `draw.Draw` on the UI thread. The full-res decode does
  this on its background goroutine.
- **Downscale on load (`downscaleForMobile`).** Full-resolution phone photos are
  12MP+ (~48MB of RGBA). On mobile the decoded image is scaled so its longest
  edge is ≤ 2× the screen's longest edge, keeping the initial texture a few MB;
  `applyZoomQuality` swaps in the full-resolution decode once the user zooms
  past 1.15×. Desktop keeps full resolution.

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

## Video playback internals

Three pieces make libmpv video work on Android: the cross-compiled native
libraries, an EGL-based mpv render path, and an Android GLVideo painter in the
vendored Fyne fork.

### 1. Cross-compiled libmpv (+ ffmpeg), vendored

libmpv is not distributed for Android; it is built from source against the NDK.
The `.so` binaries are **not committed** to this repo (they are large and
reproducible) — `third_party/android-libs/` is gitignored and regenerated.

### One command (recommended)

```sh
./build-libmpv-android.sh          # cross-compile libmpv for arm64 + vendor it
```

This wraps the [mpv-android](https://github.com/mpv-android/mpv-android)
buildscripts so it works on a fresh machine with no manual setup:

1. Clones the buildscripts under `build/mpv-android/` (gitignored).
2. Makes the NDK version the buildscripts require available under
   `build/mpv-android/buildscripts/sdk/` — reusing a matching NDK already on
   the machine, or downloading the standalone NDK zip. (Only the `mpv` native
   target is built, so the full Android SDK / Gradle app is not needed.)
3. Fetches the library sources (ffmpeg, mpv, dav1d, mbedtls, libass, …).
4. Runs `buildall.sh --arch arm64 mpv`.
5. Calls `vendor-android-libs.sh` with the right `MPV_PREFIX`/NDK.

Host build tools required (checked up front): `git wget unzip meson ninja nasm
autoconf automake libtool pkg-config gperf cmake javac python3`. The first run
downloads a lot and the native compile takes a while; subsequent runs reuse the
checkout under `build/`.

### Manual, if you already have an mpv-android prefix

```sh
# in a checkout of mpv-android/buildscripts, with the required NDK:
./buildall.sh --arch arm64 mpv     # builds ffmpeg + libmpv + deps for arm64

# then, from this repo root:
MPV_PREFIX=/path/to/prefix/arm64 ./vendor-android-libs.sh
```

`vendor-android-libs.sh` copies `prefix/arm64/lib/lib{mpv,av*,sw*}.so` plus the
NDK's `libc++_shared.so` into `third_party/android-libs/arm64-v8a/` (stripping
with `llvm-strip --strip-unneeded`, ~113 MB → ~31 MB) and the mpv headers
(`prefix/arm64/include/mpv/*.h`) into `third_party/android-libs/include/mpv/`.
`libmpv.so`'s `NEEDED` deps (libav*, libsw*, libc++_shared) are all vendored so
the runtime linker resolves them from the APK. It auto-detects the NDK and
looks for the prefix under `build/mpv-android/.../prefix/arm64` then
`~/src/mpv-android/...`; override `MPV_PREFIX` / `ANDROID_NDK_HOME` to point
elsewhere.

### 2. EGL mpv render path (`mpvplayer/`)

`mpv.go` initializes mpv's OpenGL render API via
`MPV_RENDER_PARAM_OPENGL_INIT_PARAMS`, resolving GL symbols through a
platform-specific proc-address function:

- `platform_desktop.go` (`!android`) resolves via GLFW and passes an X11/Wayland
  native display.
- `platform_android.go` (`android`) resolves via `eglGetProcAddress`
  (`dlsym(RTLD_DEFAULT, ...)` fallback), passes no native display, and selects
  `hwdec=mediacodec-copy` (hardware decode) and `ao=opensles`. MediaCodec needs
  the process JavaVM registered with ffmpeg via `av_jni_set_java_vm`; the VM
  pointer is surfaced from the mobile driver's `JNI_OnLoad` through
  `fyne/v2/driver.RunNative` (see the `mediacodec-copy` commit). Frames are
  decoded on the GPU/DSP then copied to system memory, so the existing texture
  upload path is unchanged (no Surface / zero-copy interop).

`RenderInto(fbo, w, h)` (`render_to_fbo`) uses `MPV_RENDER_PARAM_OPENGL_FBO`, so
mpv binds the supplied framebuffer and renders into it itself. The software frame
extractor in `screenshot.go` (`!nompv`, no GL context) restores real video
thumbnails on mobile.

### 3. Android GLVideo painter (fork)

Fyne's mobile GL driver (`internal/driver/mobile/gl`) is an x/mobile-style
work-queue binding: the EGL context is current only on the driver's GL worker
thread, and the paint traversal goroutine merely *enqueues* GL calls. libmpv,
however, resolves GL entry points itself and renders **synchronously**, so it
must run on the GL thread. The fork adds:

- **Framebuffer ops** to the mobile GL binding (`CreateFramebuffer`,
  `BindFramebuffer`, `FramebufferTexture2D`, `DeleteFramebuffer`,
  `CheckFramebufferStatus`) — the binding previously had none.
- **`RunOnGLThread(func())`** — enqueues a blocking Go callback that `DoWork`
  runs on the GL worker thread with the context current. This is the seam that
  lets libmpv's direct GL render execute on the right thread.
- **`glvideo_mobile.go`** — the mobile `drawGLVideo`: sets up an offscreen
  FBO+texture via the work queue, runs `RenderInto` inside `RunOnGLThread`,
  restores the window framebuffer, then composites the texture with
  `drawTextureRegion` (identical FBO-composite logic to `glvideo_gles.go`).

### Build wiring

`build-android.sh` points cgo at the vendored headers/libs and, after
`fyne package` links the app `.so`, calls `bundle-native-libs.sh` to inject the
native `.so` files into the APK's `lib/arm64-v8a/`, `zipalign`, and re-sign
(`fyne package`'s signature is invalidated by the edit). The debug key
(`~/.android/debug.keystore`) is used unless `RELEASE=1` with `KEYSTORE` /
`KEYSTORE_PASS` / `KEY_ALIAS` is set. The generated manifest already grants
`INTERNET`, which tie-view needs for streaming via `StreamURL()`.

Verify a built APK contains the libs and the app links libmpv:

```sh
unzip -l cmd/imgview/imgview.apk | grep lib/arm64-v8a
# libimgview.so should list libmpv.so as a NEEDED dependency (readelf -d)
```

### Not yet done

- **Other ABIs.** Only `arm64-v8a` is built/vendored. `armeabi-v7a` and
  `x86_64` (emulator) would each need their own cross-compile and a bundling
  tweak.
- **On-device verification** of audio output is pending (video playback with
  `mediacodec-copy` hardware decode is verified on a Note9).
