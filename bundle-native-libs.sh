#!/bin/bash
# Inject the cross-compiled libmpv + ffmpeg .so files into an APK's
# lib/arm64-v8a/ directory, then re-align and re-sign it.
#
# `fyne package` produces a signed, zipaligned APK but has no way to place
# arbitrary native libraries under lib/<abi>/. Android's dynamic linker loads
# everything in lib/arm64-v8a/ automatically at runtime, so we post-process the
# APK zip: add the libs (stored uncompressed for page alignment), zipalign, and
# re-sign (which invalidates the signature fyne applied).
#
# Usage: bundle-native-libs.sh <path-to.apk>
#
# Signing key selection:
#   RELEASE=1 with KEYSTORE / KEYSTORE_PASS / KEY_ALIAS  -> release key
#   otherwise                                            -> Android debug key
#     (~/.android/debug.keystore, password "android", alias "androiddebugkey")

set -euo pipefail

APK="${1:?usage: bundle-native-libs.sh <path-to.apk>}"
cd "$(dirname "$0")"
ROOT="$PWD"

# Ensure ANDROID_HOME (zipalign/apksigner) and a working JAVA_HOME/PATH are set
# even when this script is run standalone. apksigner and keytool are Java tools;
# on systems whose default `java` wrapper is misconfigured, android-env.sh
# repairs JAVA_HOME and puts a working JDK first on PATH.
source "$ROOT/android-env.sh"

ABI_DIR="$ROOT/third_party/android-libs/arm64-v8a"

if [ ! -d "$ABI_DIR" ]; then
    echo "error: $ABI_DIR not found (nothing to bundle)" >&2
    exit 1
fi
if [ ! -f "$APK" ]; then
    echo "error: APK not found: $APK" >&2
    exit 1
fi

# Prefer build-tools binaries if the SDK is present, else fall back to PATH.
BT=""
if [ -n "${ANDROID_HOME:-}" ] && [ -d "$ANDROID_HOME/build-tools" ]; then
    BT="$ANDROID_HOME/build-tools/$(ls "$ANDROID_HOME/build-tools" | sort -V | tail -1)"
fi
zipalign_bin="${BT:+$BT/zipalign}"; command -v "$zipalign_bin" >/dev/null 2>&1 || zipalign_bin="$(command -v zipalign)"
apksigner_bin="${BT:+$BT/apksigner}"; command -v "$apksigner_bin" >/dev/null 2>&1 || apksigner_bin="$(command -v apksigner)"

echo "Bundling native libs into $(basename "$APK")..."

# Stage lib/arm64-v8a/*.so and add them to the APK zip, stored (-0) so zipalign
# can page-align them. -X drops extra file attributes for a reproducible entry.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT
mkdir -p "$staging/lib/arm64-v8a"
cp "$ABI_DIR"/*.so "$staging/lib/arm64-v8a/"

abs_apk="$(cd "$(dirname "$APK")" && pwd)/$(basename "$APK")"
( cd "$staging" && zip -q -X -0 -r "$abs_apk" lib )
echo "  added: $(ls "$ABI_DIR"/*.so | xargs -n1 basename | tr '\n' ' ')"

# Re-align (16 KiB page alignment for uncompressed .so) then re-sign.
aligned="$abs_apk.aligned"
"$zipalign_bin" -f -p 4 "$abs_apk" "$aligned"
mv -f "$aligned" "$abs_apk"

if [ "${RELEASE:-0}" = "1" ]; then
    : "${KEYSTORE:?RELEASE=1 requires KEYSTORE}"
    : "${KEYSTORE_PASS:?RELEASE=1 requires KEYSTORE_PASS}"
    : "${KEY_ALIAS:?RELEASE=1 requires KEY_ALIAS}"
    "$apksigner_bin" sign --ks "$KEYSTORE" --ks-pass "pass:$KEYSTORE_PASS" \
        --ks-key-alias "$KEY_ALIAS" "$abs_apk"
else
    debug_ks="$HOME/.android/debug.keystore"
    if [ ! -f "$debug_ks" ]; then
        echo "  creating debug keystore at $debug_ks"
        mkdir -p "$(dirname "$debug_ks")"
        keytool -genkeypair -keystore "$debug_ks" -storepass android -keypass android \
            -alias androiddebugkey -keyalg RSA -keysize 2048 -validity 10000 \
            -dname "CN=Android Debug,O=Android,C=US"
    fi
    "$apksigner_bin" sign --ks "$debug_ks" --ks-pass pass:android \
        --ks-key-alias androiddebugkey --key-pass pass:android "$abs_apk"
fi

"$apksigner_bin" verify "$abs_apk" >/dev/null && echo "  re-signed and verified OK"
