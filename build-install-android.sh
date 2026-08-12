#!/bin/bash
# Build and install imgview and/or tieview APKs on Android in one step.
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
#   ./build-install-android.sh tieview          # build & install just tieview
#   TARGET=android ./build-install-android.sh   # all ABIs (needs 32-bit NDK)
#   RELEASE=1 ./build-install-android.sh        # release build (signed)
#   DEVICE=2ab30210670b7ece ./build-install-android.sh  # specific device
#   LAUNCH=1 ./build-install-android.sh         # also launch each app after install

set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"

TARGET="${TARGET:-android/arm64}"

export ANDROID_HOME="${ANDROID_HOME:-$HOME/android-sdk}"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$ANDROID_HOME}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$HOME/downloads/android-ndk-r27d}"
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
        tieview) echo "sr.ht.uid.tieview" ;;
        *) echo "error: unknown app '$1' (expected imgview or tieview)" >&2; exit 1 ;;
    esac
}

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

# Build the requested app(s)
APPS_TO_BUILD=()
if [ "$#" -ge 1 ]; then
    APPS_TO_BUILD=("$1")
else
    APPS_TO_BUILD=(imgview tieview)
fi

for app in "${APPS_TO_BUILD[@]}"; do
    build "$app"
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
