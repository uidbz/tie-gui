#!/bin/bash
# Build imgview and tieview as Android APKs using `fyne package`.
#
# Video playback (libmpv) is compiled out on Android automatically: the mpv
# files carry a `!android` build constraint and a no-op stub takes their place
# (see mpvplayer/mpv_stub.go). This is the "fast path" build - it produces
# working APKs without any native libmpv. See docs/ANDROID.md for what a full
# libmpv-backed video build would require.
#
# Requirements:
#   - the fyne command: go install fyne.io/fyne/v2/cmd/fyne@latest
#   - Android SDK + NDK, with ANDROID_HOME / ANDROID_NDK_HOME set (or the
#     defaults below adjusted to your machine)
#   - the fyne fork submodule checked out: git submodule update --init
#
# Usage:
#   ./build-android.sh                    # build both APKs, arm64
#   ./build-android.sh imgview            # build just one (imgview or tieview)
#   TARGET=android ./build-android.sh     # all ABIs (needs 32-bit NDK support)
#   RELEASE=1 ./build-android.sh          # release build (signed)

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

TARGET="${TARGET:-android/arm64}"

export ANDROID_HOME="${ANDROID_HOME:-$HOME/android-sdk}"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$ANDROID_HOME}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$HOME/downloads/android-ndk-r27d}"
export PATH="$ANDROID_HOME/platform-tools:$PATH"

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

# app_id for each buildable command.
app_id() {
    case "$1" in
        imgview) echo "sr.ht.uid.imgview" ;;
        tieview) echo "sr.ht.uid.tieview" ;;
        *) echo "error: unknown app '$1' (expected imgview or tieview)" >&2; exit 1 ;;
    esac
}

# build <app> packages cmd/<app>/ into cmd/<app>/<app>.apk. fyne resolves the
# icon relative to the package directory, so we run it from there.
build() {
    local app="$1" id
    id="$(app_id "$app")"

    local args=(package -os "$TARGET" -app-id "$id" -icon Icon.png)
    if [ "${RELEASE:-0}" = "1" ]; then
        args+=(--release)
    fi

    echo "Building $app for $TARGET (this may take a while on first compile)..."
    ( cd "$ROOT/cmd/$app" && fyne "${args[@]}" )
    echo "  -> cmd/$app/$app.apk"
}

if [ "$#" -ge 1 ]; then
    build "$1"
else
    build imgview
    build tieview
fi

echo
echo "APKs built:"
ls -1 "$ROOT"/cmd/*/*.apk 2>/dev/null
