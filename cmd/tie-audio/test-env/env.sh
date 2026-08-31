#!/usr/bin/env bash
# Shared environment for the tie-audio local test setup.
# Sourced by start.sh / stop.sh. This test-env layers a pwplay-server (:8080)
# on top of the tie stack (triplestore :2161 + filehost :2162) that lives in the tie
# repo's own test-env, so audio playback can be driven end-to-end.

# Absolute path to this directory and the tie-audio repo root.
export TAP_ENV="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export TAP_SRC="$(cd "$TAP_ENV/.." && pwd)"

# The tie stack test-env (triplestore + filehost + seeded audio albums). Override
# TIE_TEST_ENV to point at a different checkout.
export TIE_TEST_ENV="${TIE_TEST_ENV:-$(cd "$TAP_SRC/../tie/test-env" && pwd)}"

# The pwplay source repo (built into the server binary below). Override
# PWPLAY_SRC for a checkout elsewhere.
export PWPLAY_SRC="${PWPLAY_SRC:-$(cd "$TAP_SRC/../pwplay" && pwd)}"

# pwplay-server binds :8080 (hardcoded in its main.go). It requires PipeWire on
# the host and at least one initial file argument; we hand it a generated silent
# placeholder track since the real queue is filled over HTTP by the app.
export PWPLAY_PORT="8080"
export PWPLAY_URL="http://localhost:${PWPLAY_PORT}"

export TAP_LOGS="${TAP_ENV}/logs"
export PWPLAY_BIN="${TAP_LOGS}/pwplay-server"
export PWPLAY_PID="${TAP_LOGS}/pwplay-server.pid"
export PWPLAY_LOG="${TAP_LOGS}/pwplay-server.log"
export PWPLAY_SEED="${TAP_LOGS}/placeholder.mp3"
