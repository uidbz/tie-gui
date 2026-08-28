#!/bin/bash
# Build tie-audio-player as an Android APK using `fyne package`.
#
# tie-audio-player is a remote client (it controls a pwplay-server over HTTP),
# so the current phases need no native audio libraries on the device — this is a
# plain `fyne package` build against the vendored fyne fork. When the local
# libmpv playback backend lands (Phase 6), this script will grow a
# bundle-native-libs step like imgview's.
#
# Requirements:
#   - the fyne command: go install fyne.io/fyne/v2/cmd/fyne@latest
#   - Android SDK + NDK, with ANDROID_HOME / ANDROID_NDK_HOME set (or the
#     generic discovery in android-env.sh finds them)
#   - the fyne fork submodule at ../../third_party/fyne (referenced by the
#     replace directive in the monorepo go.mod)
#
# Usage:
#   ./build-android.sh              # debug APK, android/arm64
#   RELEASE=1 ./build-android.sh    # signed release build

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

TARGET="${TARGET:-android/arm64}"
APPID="sr.ht.uid.tieaudioplayer"
ICON="${ICON:-Icon.png}"

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

RELEASE_FLAG=""
if [ "${RELEASE:-0}" = "1" ]; then
    RELEASE_FLAG="--release"
fi

fyne package -os "$TARGET" --id "$APPID" -icon "$ICON" $RELEASE_FLAG \
    --src "$ROOT"

echo "Built APK for $TARGET"
