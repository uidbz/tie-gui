#!/bin/bash
# Build and install imgview and/or tie-view APKs on Android in one step.
#
# Combines build-android.sh and install-android.sh: builds the APK(s), then
# immediately installs them on a connected Android device.
#
# Requirements:
#   - the fyne command: go install fyne.io/fyne/v2/cmd/fyne@latest
#   - Android SDK + NDK, with ANDROID_HOME / ANDROID_NDK_HOME set
#   - the fyne fork submodule: git submodule update --init
#   - adb (Android platform-tools) and a connected device
#
# Usage:
#   ./build-install-android.sh                  # build & install both APKs, arm64
#   ./build-install-android.sh tie-view          # build & install just tie-view
#   TARGET=android ./build-install-android.sh   # all ABIs (needs 32-bit NDK)
#   NOMPV=1 ./build-install-android.sh          # libmpv-free build (no video)
#   RELEASE=1 ./build-install-android.sh        # release build (signed)
#   DEVICE=2ab30210670b7ece ./build-install-android.sh  # specific device
#   LAUNCH=1 ./build-install-android.sh         # also launch each app after install

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

TARGET="${TARGET:-android/arm64}"

# Discover the Android SDK/NDK generically (see android-env.sh). Override by
# exporting ANDROID_HOME / ANDROID_NDK_HOME before running this script.
source "$ROOT/android-env.sh"

if [ -z "${ANDROID_HOME:-}" ]; then
    echo "error: Android SDK not found. Set ANDROID_HOME to your SDK root." >&2
    exit 1
fi
export PATH="$ANDROID_HOME/platform-tools:$PATH"

# ═══════════════════════════════════════════════════════════════════════════
# Build phase
# ═══════════════════════════════════════════════════════════════════════════

if ! command -v fyne >/dev/null 2>&1; then
    echo "error: 'fyne' command not found. Install it with:" >&2
    echo "  go install fyne.io/fyne/v2/cmd/fyne@latest" >&2
    exit 1
fi

if [ ! -f third_party/fyne/go.mod ]; then
    echo "fyne submodule missing; initializing..." >&2
    git submodule update --init --recursive
fi

app_id() {
    case "$1" in
        imgview) echo "sr.ht.uid.imgview" ;;
        tie-view) echo "sr.ht.uid.tieview" ;;
        *) echo "error: unknown app '$1' (expected imgview or tie-view)" >&2; exit 1 ;;
    esac
}

# Build the requested app(s)
APPS_TO_BUILD=()
if [ "$#" -ge 1 ]; then
    APPS_TO_BUILD=("$1")
else
    APPS_TO_BUILD=(imgview tie-view)
fi

# Delegate the build phase to build-android.sh so the libmpv CGo wiring
# (CGO_CFLAGS/CGO_LDFLAGS pointing at third_party/android-libs, plus the
# bundle-native-libs.sh step) stays in a single place. Passing TARGET/NOMPV/
# RELEASE through the environment keeps behavior identical.
for app in "${APPS_TO_BUILD[@]}"; do
    TARGET="$TARGET" NOMPV="${NOMPV:-0}" RELEASE="${RELEASE:-0}" \
        "$ROOT/build-android.sh" "$app"
done

echo
echo "APKs built:"
ls -1 "$ROOT"/cmd/*/*.apk 2>/dev/null

# ═══════════════════════════════════════════════════════════════════════════
# Install phase
# ═══════════════════════════════════════════════════════════════════════════

# Locate adb: prefer PATH, then a standard Android SDK location.
if command -v adb >/dev/null 2>&1; then
    ADB="adb"
elif [ -x "${ANDROID_HOME}/platform-tools/adb" ]; then
    ADB="${ANDROID_HOME}/platform-tools/adb"
else
    echo "error: adb not found. Install platform-tools or set ANDROID_HOME." >&2
    exit 1
fi

# Optional explicit device selection.
DEVICE_ARGS=()
if [ -n "${DEVICE:-}" ]; then
    DEVICE_ARGS=(-s "$DEVICE")
fi

install_app() {
    local app="$1"
    local id apk
    id="$(app_id "$app")"
    apk="cmd/$app/$app.apk"

    if [ ! -f "$apk" ]; then
        echo "error: APK not found: $apk" >&2
        return 1
    fi

    echo
    echo "Installing $apk ..."
    "$ADB" "${DEVICE_ARGS[@]}" install -r "$apk"

    if [ "${LAUNCH:-0}" = "1" ]; then
        echo "Launching $id ..."
        "$ADB" "${DEVICE_ARGS[@]}" shell monkey -p "$id" -c android.intent.category.LAUNCHER 1 >/dev/null
    fi
}

for app in "${APPS_TO_BUILD[@]}"; do
    install_app "$app"
done

echo
echo "Done."
