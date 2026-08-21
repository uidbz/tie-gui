#!/bin/bash
# Install imgview and/or tieview APKs on a connected Android device via adb.
#
# By default it installs both APKs produced by `build-android.sh`
# (cmd/imgview/imgview.apk and cmd/tieview/tieview.apk). Pass an app name as
# the first argument to install just one. Override the APK path via the APK
# env var (only meaningful when installing a single app). Pass a device serial
# via DEVICE (see `adb devices`).
#
# Usage:
#   ./install-android.sh                       # install both APKs
#   ./install-android.sh imgview               # install just imgview
#   ./install-android.sh tieview               # install just tieview
#   DEVICE=2ab30210670b7ece ./install-android.sh
#   LAUNCH=1 ./install-android.sh              # also launch each app after install

set -euo pipefail

cd "$(dirname "$0")"

# Discover the Android SDK generically (for the adb fallback below).
source "$(dirname "$0")/android-env.sh"

# Locate adb: prefer PATH, then a standard Android SDK location.
if command -v adb >/dev/null 2>&1; then
    ADB="adb"
elif [ -n "${ANDROID_HOME:-}" ] && [ -x "$ANDROID_HOME/platform-tools/adb" ]; then
    ADB="$ANDROID_HOME/platform-tools/adb"
else
    echo "error: adb not found. Install platform-tools or set ANDROID_HOME." >&2
    exit 1
fi

# Optional explicit device selection.
DEVICE_ARGS=()
if [ -n "${DEVICE:-}" ]; then
    DEVICE_ARGS=(-s "$DEVICE")
fi

app_id() {
    case "$1" in
        imgview) echo "sr.ht.uid.imgview" ;;
        tieview) echo "sr.ht.uid.tieview" ;;
        *) echo "error: unknown app '$1' (expected imgview or tieview)" >&2; exit 1 ;;
    esac
}

install_app() {
    local app="$1"
    local id apk
    id="$(app_id "$app")"
    apk="${APK:-cmd/$app/$app.apk}"

    if [ ! -f "$apk" ]; then
        echo "error: APK not found: $apk" >&2
        echo "Build it first with: ./build-android.sh $app" >&2
        return 1
    fi

    echo "Installing $apk ..."
    "$ADB" "${DEVICE_ARGS[@]}" install -r "$apk"

    if [ "${LAUNCH:-0}" = "1" ]; then
        echo "Launching $id ..."
        "$ADB" "${DEVICE_ARGS[@]}" shell monkey -p "$id" -c android.intent.category.LAUNCHER 1 >/dev/null
    fi
}

if [ "$#" -ge 1 ]; then
    install_app "$1"
else
    install_app imgview
    install_app tieview
fi

echo "Done."
