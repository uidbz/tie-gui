#!/bin/bash
# Cross-compile libmpv (+ ffmpeg and deps) for Android arm64 and vendor the
# result into third_party/android-libs/, so ./build-android.sh can link video
# playback into the APKs.
#
# This wraps the upstream mpv-android buildscripts
# (https://github.com/mpv-android/mpv-android) so the whole thing works on a
# fresh machine with no hardcoded paths: it clones the buildscripts under
# build/, makes sure the NDK version they need is available (reusing an
# already-installed one, or downloading it), fetches the library sources, runs
# the native cross-compile, then calls vendor-android-libs.sh.
#
# Only the `mpv` target is built (native libraries) — not the mpv-android
# Gradle app — so the full Android SDK is not required, just the NDK plus the
# usual host build tools (meson, ninja, nasm, autoconf, libtool, pkg-config,
# gperf, cmake, wget, git, a JDK).
#
# Usage:
#   ./build-libmpv-android.sh            # build arm64 libmpv and vendor it
#   MPV_ANDROID_REF=<sha> ./build-libmpv-android.sh   # pin buildscripts commit
#
# Env overrides:
#   ARCH            default: arm64 (matches third_party/android-libs/arm64-v8a)
#   ANDROID_NDK_HOME  reuse an existing NDK of the required version if it matches
#   JOBS / cores    parallelism for the native build

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

ARCH="${ARCH:-arm64}"
BUILD_DIR="$ROOT/build"
BS_PARENT="$BUILD_DIR/mpv-android"
BS="$BS_PARENT/buildscripts"

source "$ROOT/android-env.sh"

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: missing host tool '$1'" >&2; MISSING=1; }; }
MISSING=0
for t in git wget unzip meson ninja nasm autoconf automake libtool pkg-config gperf cmake javac python3; do
    need "$t"
done
[ "$MISSING" = "1" ] && { echo "Install the missing tools above and re-run." >&2; exit 1; }

# ── 1. mpv-android buildscripts checkout ────────────────────────────────────
mkdir -p "$BUILD_DIR"
if [ ! -d "$BS" ]; then
    echo "Cloning mpv-android buildscripts into $BS_PARENT ..."
    git clone https://github.com/mpv-android/mpv-android.git "$BS_PARENT"
fi
if [ -n "${MPV_ANDROID_REF:-}" ]; then
    ( cd "$BS_PARENT" && git fetch --depth 1 origin "$MPV_ANDROID_REF" && git checkout "$MPV_ANDROID_REF" )
fi

# Required NDK version, as declared by the buildscripts.
# shellcheck disable=SC1091
. "$BS/include/depinfo.sh"
echo "buildscripts want NDK $v_ndk ($v_ndk_n)"

mkdir -p "$BS/sdk" "$BS/bin"
NDK_LINK="$BS/sdk/android-ndk-${v_ndk}"

ndk_version_ok() {
    [ -f "$1/source.properties" ] && grep -qF "${v_ndk_n}" "$1/source.properties"
}

# ── 2. Make the required NDK available at sdk/android-ndk-<ver> ──────────────
if ndk_version_ok "$NDK_LINK"; then
    echo "NDK already present at $NDK_LINK"
else
    rm -f "$NDK_LINK"
    # Reuse a matching NDK we can find, else download the standalone zip.
    reused=""
    for cand in \
        "${ANDROID_NDK_HOME:-}" \
        "${ANDROID_HOME:-}/ndk/${v_ndk_n}" \
        /apps/android-ndk-"${v_ndk}" \
        /opt/android-ndk-"${v_ndk}" \
        "$HOME"/android-ndk-"${v_ndk}"; do
        if [ -n "$cand" ] && ndk_version_ok "$cand"; then
            echo "Reusing NDK at $cand"
            ln -sfn "$cand" "$NDK_LINK"
            reused=1
            break
        fi
    done
    if [ -z "$reused" ]; then
        echo "Downloading NDK ${v_ndk} ..."
        zip="$BS/sdk/android-ndk-${v_ndk}-linux.zip"
        wget -O "$zip" "https://dl.google.com/android/repository/android-ndk-${v_ndk}-linux.zip"
        unzip -q -d "$BS/sdk" "$zip"
        rm -f "$zip"
        ndk_version_ok "$NDK_LINK" || { echo "error: downloaded NDK is not ${v_ndk_n}" >&2; exit 1; }
    fi
fi

# gas-preprocessor (used by ffmpeg's arm build); harmless to always have.
if [ ! -x "$BS/bin/gas-preprocessor.pl" ]; then
    wget -O "$BS/bin/gas-preprocessor.pl" \
        "https://github.com/FFmpeg/gas-preprocessor/raw/master/gas-preprocessor.pl"
    chmod +x "$BS/bin/gas-preprocessor.pl"
fi

# ── 3. Fetch library sources (ffmpeg, mpv, deps) ────────────────────────────
echo "Fetching library sources (this can take a while the first time) ..."
( cd "$BS" && IN_CI=1 ./include/download-deps.sh )

# ── 4. Cross-compile the native libraries ───────────────────────────────────
echo "Cross-compiling libmpv for $ARCH (long; grab a coffee) ..."
( cd "$BS" && cores="${JOBS:-${cores:-$(nproc)}}" ./buildall.sh --arch "$ARCH" mpv )

PREFIX="$BS/prefix/$ARCH"
if [ ! -f "$PREFIX/lib/libmpv.so" ]; then
    echo "error: build finished but $PREFIX/lib/libmpv.so is missing" >&2
    exit 1
fi

# ── 5. Vendor into third_party/android-libs/ ────────────────────────────────
echo "Vendoring libraries ..."
# Vendor libc++_shared from the same NDK mpv was built against.
MPV_PREFIX="$PREFIX" ANDROID_NDK_HOME="$(readlink -f "$NDK_LINK")" \
    "$ROOT/vendor-android-libs.sh"

echo
echo "libmpv for Android is ready. Now build the APKs:"
echo "  ./build-android.sh"
