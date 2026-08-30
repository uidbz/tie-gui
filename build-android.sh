#!/bin/bash
# Build the tie-gui apps as Android APKs using `fyne package`.
#
# imgview and tie-view get the full, libmpv-backed build: in-app video playback
# and real video thumbnails work on Android, matching desktop. They link against
# a libmpv (plus ffmpeg) cross-compiled for arm64-v8a and vendored under
# third_party/android-libs/ (see docs/ANDROID.md for how those were produced).
# After `fyne package` links the app, bundle-native-libs.sh injects the native
# .so files into the APK's lib/arm64-v8a/ and re-signs it.
#
# tie-audio-player is a remote client (it controls a pwplay-server over HTTP),
# so it needs no native libraries — a plain `fyne package` build. (When the
# local libmpv playback backend lands there, it will join the bundling path.)
#
# For a libmpv-free build of the viewers (no video, no native libs to bundle),
# pass NOMPV=1 — it adds `-tags nompv` and skips the bundling step.
#
# Requirements:
#   - the fyne command: go install fyne.io/fyne/v2/cmd/fyne@latest
#   - Android SDK + NDK, with ANDROID_HOME / ANDROID_NDK_HOME set (or the
#     defaults below adjusted to your machine)
#   - the fyne fork submodule checked out: git submodule update --init
#   - vendored native libs in third_party/android-libs/ (viewers only, unless NOMPV=1)
#
# Usage:
#   ./build-android.sh                    # build all APKs, arm64, with libmpv
#   ./build-android.sh imgview            # build just one app
#   ./build-android.sh tie-audio-player   # (no libmpv involved either way)
#   NOMPV=1 ./build-android.sh            # libmpv-free viewer build (no video)
#   RELEASE=1 ./build-android.sh          # release build (signed)

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

TARGET="${TARGET:-android/arm64}"
NOMPV="${NOMPV:-0}"

# app_id for each buildable command.
app_id() {
    case "$1" in
        imgview) echo "sr.ht.uid.imgview" ;;
        tie-view) echo "sr.ht.uid.tieview" ;;
        tie-audio-player) echo "sr.ht.uid.tieaudioplayer" ;;
        *) echo "error: unknown app '$1' (expected imgview, tie-view or tie-audio-player)" >&2; exit 1 ;;
    esac
}

# needs_mpv: the viewers link libmpv and need the vendored native libs;
# tie-audio-player is a remote client and bundles none.
needs_mpv() {
    case "$1" in
        imgview|tie-view) return 0 ;;
        *) return 1 ;;
    esac
}

# Decide which apps to build.
if [ "$#" -ge 1 ]; then
    APPS=("$@")
else
    APPS=(imgview tie-view tie-audio-player)
fi

# Does any requested app link libmpv?
mpv_required=0
for app in "${APPS[@]}"; do
    if needs_mpv "$app"; then
        mpv_required=1
    fi
done

# Discover the Android SDK/NDK generically (see android-env.sh). Override by
# exporting ANDROID_HOME / ANDROID_NDK_HOME before running this script.
source "$ROOT/android-env.sh"

if [ -z "${ANDROID_HOME:-}" ]; then
    echo "error: Android SDK not found. Set ANDROID_HOME to your SDK root." >&2
    exit 1
fi
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
    echo "error: Android NDK not found. Set ANDROID_NDK_HOME to your NDK root." >&2
    exit 1
fi
export PATH="$ANDROID_HOME/platform-tools:$PATH"
echo "Using Android SDK: $ANDROID_HOME"
echo "Using Android NDK: $ANDROID_NDK_HOME"

# Vendored native libs + headers for the libmpv-backed build.
VENDOR="$ROOT/third_party/android-libs"
ABI_DIR="$VENDOR/arm64-v8a"

if ! command -v fyne >/dev/null 2>&1; then
    echo "error: 'fyne' command not found. Install it with:" >&2
    echo "  go install fyne.io/fyne/v2/cmd/fyne@latest" >&2
    exit 1
fi

# The libmpv fork lives in a submodule; without it the replace directive in
# go.mod points at an empty directory and the build fails.
if [ ! -f third_party/fyne/go.mod ]; then
    echo "fyne submodule missing; initializing..." >&2
    git submodule update --init --recursive
fi

# For the libmpv-backed build, point cgo at the vendored headers/libs so the
# app .so links against libmpv (resolved at runtime from the APK lib dir).
if [ "$NOMPV" != "1" ] && [ "$mpv_required" = "1" ]; then
    if [ ! -f "$ABI_DIR/libmpv.so" ]; then
        echo "error: $ABI_DIR/libmpv.so not found." >&2
        echo "  Vendor the native libs first: ./vendor-android-libs.sh" >&2
        echo "  (see docs/ANDROID.md), or build without video: NOMPV=1 $0" >&2
        exit 1
    fi
    export CGO_CFLAGS="-I$VENDOR/include ${CGO_CFLAGS:-}"
    export CGO_LDFLAGS="-L$ABI_DIR ${CGO_LDFLAGS:-}"
fi

# apk_path: `fyne package` names the APK after the package's executable name,
# converting hyphens to underscores (tie-view -> tie_view.apk).
apk_path() {
    echo "$ROOT/cmd/$1/${1//-/_}.apk"
}

# build <app> packages cmd/<app>/ into cmd/<app>/<app>.apk. fyne resolves the
# icon relative to the package directory, so we run it from there.
build() {
    local app="$1" id
    id="$(app_id "$app")"

    local args=(package -os "$TARGET" --id "$id" -icon Icon.png)
    if [ "$NOMPV" = "1" ]; then
        args+=(-tags nompv)
    fi
    if [ "${RELEASE:-0}" = "1" ]; then
        args+=(--release)
    fi

    echo "Building $app for $TARGET (this may take a while on first compile)..."
    ( cd "$ROOT/cmd/$app" && fyne "${args[@]}" )
    local apk
    apk="$(apk_path "$app")"
    echo "  -> ${apk#$ROOT/}"

    # Inject libmpv + ffmpeg .so into the APK and re-sign.
    if [ "$NOMPV" != "1" ] && needs_mpv "$app"; then
        RELEASE="${RELEASE:-0}" "$ROOT/bundle-native-libs.sh" "$apk"
    fi
}

for app in "${APPS[@]}"; do
    build "$app"
done

echo
echo "APKs built:"
ls -1 "$ROOT"/cmd/*/*.apk 2>/dev/null
