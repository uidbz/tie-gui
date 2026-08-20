#!/bin/bash
# Recreate third_party/android-libs/ (the cross-compiled libmpv + ffmpeg used by
# the Android video build) from an mpv-android build prefix and the NDK.
#
# The .so binaries are NOT committed to this repo — this script regenerates
# them. Run it once after cross-compiling libmpv, before ./build-android.sh.
#
# Prerequisites — build libmpv (+ ffmpeg + deps) for arm64 with mpv-android's
# scripts (https://github.com/mpv-android/mpv-android):
#
#   cd <mpv-android>/buildscripts
#   ./buildall.sh --arch arm64 mpv        # produces prefix/arm64/{lib,include}
#
# Then run this script from the repo root. Override the locations with env vars:
#   MPV_PREFIX        default: $HOME/src/mpv-android/buildscripts/prefix/arm64
#   ANDROID_NDK_HOME  default: $HOME/src/mpv-android/buildscripts/sdk/android-ndk-r29

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

MPV_PREFIX="${MPV_PREFIX:-$HOME/src/mpv-android/buildscripts/prefix/arm64}"
ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$HOME/src/mpv-android/buildscripts/sdk/android-ndk-r29}"

DST="$ROOT/third_party/android-libs"
ABI_DIR="$DST/arm64-v8a"

if [ ! -f "$MPV_PREFIX/lib/libmpv.so" ]; then
    echo "error: $MPV_PREFIX/lib/libmpv.so not found." >&2
    echo "  Cross-compile libmpv first (see the header of this script)." >&2
    echo "  Or set MPV_PREFIX to your mpv-android prefix/arm64 directory." >&2
    exit 1
fi

TC="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64"
STRIP="$TC/bin/llvm-strip"
LIBCXX="$TC/sysroot/usr/lib/aarch64-linux-android/libc++_shared.so"

if [ ! -x "$STRIP" ]; then
    echo "error: llvm-strip not found at $STRIP (check ANDROID_NDK_HOME)" >&2
    exit 1
fi
if [ ! -f "$LIBCXX" ]; then
    echo "error: libc++_shared.so not found at $LIBCXX (check ANDROID_NDK_HOME)" >&2
    exit 1
fi

echo "Vendoring native libs from $MPV_PREFIX ..."
rm -rf "$ABI_DIR" "$DST/include"
mkdir -p "$ABI_DIR" "$DST/include/mpv"

# libmpv + the ffmpeg libraries it links (NEEDED), plus the NDK C++ runtime.
cp "$MPV_PREFIX"/lib/libmpv.so "$MPV_PREFIX"/lib/libav*.so "$MPV_PREFIX"/lib/libsw*.so "$ABI_DIR/"
cp "$LIBCXX" "$ABI_DIR/"
cp "$MPV_PREFIX"/include/mpv/*.h "$DST/include/mpv/"

# Strip to keep the APK small (~113 MB -> ~31 MB).
for f in "$ABI_DIR"/*.so; do "$STRIP" --strip-unneeded "$f"; done

echo "  arm64-v8a: $(ls "$ABI_DIR"/*.so | wc -l) libs, $(du -sh "$ABI_DIR" | cut -f1)"
echo "  headers:   $(ls "$DST/include/mpv"/*.h | xargs -n1 basename | tr '\n' ' ')"
echo "Done. Now run: ./build-android.sh"
