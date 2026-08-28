# shellcheck shell=bash
# Generic Android SDK / NDK discovery, sourced by the android build scripts.
#
# The goal is that the Android build works on any machine without editing
# hardcoded paths: set ANDROID_HOME / ANDROID_NDK_HOME explicitly, or let this
# helper find a toolchain in the usual places.
#
# After sourcing, these are exported (when found):
#   ANDROID_HOME, ANDROID_SDK_ROOT   - the SDK root
#   ANDROID_NDK_HOME, ANDROID_NDK_ROOT - the NDK root
#
# Override anything by exporting it before running a build script.

# Pick the newest entry from a glob of candidate directories (version-sorted).
# Usage: _android_newest_dir "/glob/pattern-*"  (unquoted glob expanded here)
_android_newest_dir() {
    local newest="" d
    for d in "$@"; do
        [ -d "$d" ] || continue
        newest="$d"
    done
    printf '%s' "$newest"
}

# Verify a directory looks like an NDK (has the llvm prebuilt toolchain).
_android_is_ndk() {
    [ -n "$1" ] && [ -d "$1/toolchains/llvm/prebuilt" ]
}

# Verify a directory looks like an SDK (has build-tools or platform-tools).
_android_is_sdk() {
    [ -n "$1" ] && { [ -d "$1/build-tools" ] || [ -d "$1/platform-tools" ]; }
}

# ── SDK discovery ──────────────────────────────────────────────────────────
detect_android_sdk() {
    # Respect an explicit, valid setting first.
    local cand
    for cand in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}"; do
        if _android_is_sdk "$cand"; then
            export ANDROID_HOME="$cand" ANDROID_SDK_ROOT="$cand"
            return 0
        fi
    done

    # Common install locations, version-sorted globs picked newest-last.
    local guesses=(
        "$HOME/android-sdk"
        "$HOME/Android/Sdk"
        "$HOME/Library/Android/sdk"
        "/opt/android-sdk-update-manager"
        "/opt/android-sdk"
        "/usr/lib/android-sdk"
    )
    for cand in "${guesses[@]}"; do
        if _android_is_sdk "$cand"; then
            export ANDROID_HOME="$cand" ANDROID_SDK_ROOT="$cand"
            return 0
        fi
    done
    return 1
}

# ── NDK discovery ──────────────────────────────────────────────────────────
detect_android_ndk() {
    local cand
    for cand in "${ANDROID_NDK_HOME:-}" "${ANDROID_NDK_ROOT:-}"; do
        if _android_is_ndk "$cand"; then
            export ANDROID_NDK_HOME="$cand" ANDROID_NDK_ROOT="$cand"
            return 0
        fi
    done

    # NDKs installed inside the SDK (sdkmanager "ndk;<ver>" or ndk-bundle).
    if [ -n "${ANDROID_HOME:-}" ]; then
        cand="$(_android_newest_dir "$ANDROID_HOME"/ndk/*)"
        _android_is_ndk "$cand" && { export ANDROID_NDK_HOME="$cand" ANDROID_NDK_ROOT="$cand"; return 0; }
        cand="$ANDROID_HOME/ndk-bundle"
        _android_is_ndk "$cand" && { export ANDROID_NDK_HOME="$cand" ANDROID_NDK_ROOT="$cand"; return 0; }
    fi

    # Standalone NDK unpacked somewhere common; pick the newest match.
    cand="$(_android_newest_dir \
        /apps/android-ndk-* \
        /opt/android-ndk-* \
        "$HOME"/android-ndk-* \
        "$HOME"/src/mpv-android/buildscripts/sdk/android-ndk-* \
        "$HOME"/downloads/android-ndk-*)"
    _android_is_ndk "$cand" && { export ANDROID_NDK_HOME="$cand" ANDROID_NDK_ROOT="$cand"; return 0; }

    # A plain /opt/android-ndk symlink or dir.
    _android_is_ndk "/opt/android-ndk" && { export ANDROID_NDK_HOME="/opt/android-ndk" ANDROID_NDK_ROOT="/opt/android-ndk"; return 0; }
    return 1
}

# ── JDK discovery / repair ──────────────────────────────────────────────────
# fyne, keytool and apksigner all need a working JDK. Some systems (e.g. a
# Gentoo box whose java-config user VM points at a since-removed JDK) leave
# JAVA_HOME dangling, which breaks every Java tool. Verify it and fall back to
# any working JDK we can find.
_java_home_ok() {
    [ -n "$1" ] && [ -x "$1/bin/java" ] && "$1/bin/java" -version >/dev/null 2>&1
}

detect_java() {
    # A valid, working JAVA_HOME wins.
    if _java_home_ok "${JAVA_HOME:-}"; then
        export JAVA_HOME
        export PATH="$JAVA_HOME/bin:$PATH"
        return 0
    fi

    local cand
    # macOS helper.
    if [ -x /usr/libexec/java_home ]; then
        cand="$(/usr/libexec/java_home 2>/dev/null || true)"
        _java_home_ok "$cand" && { export JAVA_HOME="$cand" PATH="$cand/bin:$PATH"; return 0; }
    fi
    # Gentoo java-config.
    if command -v java-config >/dev/null 2>&1; then
        cand="$(java-config -O 2>/dev/null || true)"
        _java_home_ok "$cand" && { export JAVA_HOME="$cand" PATH="$cand/bin:$PATH"; return 0; }
    fi
    # Common JVM install roots; pick the newest that works.
    local d
    for d in $(_android_newest_dir /usr/lib/jvm/* /usr/lib64/jvm/* /Library/Java/JavaVirtualMachines/*/Contents/Home) \
             /usr/lib/jvm/* /usr/lib64/jvm/*; do
        if _java_home_ok "$d"; then export JAVA_HOME="$d" PATH="$d/bin:$PATH"; return 0; fi
    done
    # Fall back to whatever java is already on PATH (if it actually runs).
    if command -v java >/dev/null 2>&1 && java -version >/dev/null 2>&1; then
        return 0
    fi
    return 1
}

# Run detection; callers decide whether a missing toolchain is fatal.
detect_android_sdk || true
detect_android_ndk || true
detect_java || true
