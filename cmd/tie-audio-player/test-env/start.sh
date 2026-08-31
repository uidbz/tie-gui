#!/usr/bin/env bash
# Bring up the full tie-audio-player playback test environment:
#   1. the tie stack (triplestore :2161 + filehost :2162 + seeded audio albums),
#      delegated to the tie repo's own test-env start.sh (idempotent);
#   2. a pwplay-server on :8080 that streams the filehost blobs over HTTP.
# Idempotent-ish: refuses to start a second pwplay-server if the PID file points
# at a live process.
set -euo pipefail
source "$(dirname "$0")/env.sh"

mkdir -p "$TAP_LOGS"

# --- 1. tie stack -----------------------------------------------------------
if [[ ! -x "$TIE_TEST_ENV/start.sh" ]]; then
	echo "tie test-env not found at $TIE_TEST_ENV (set TIE_TEST_ENV)" >&2
	exit 1
fi
echo "Starting tie stack via $TIE_TEST_ENV/start.sh ..."
"$TIE_TEST_ENV/start.sh"

# --- 2. pwplay-server -------------------------------------------------------
is_running() { [[ -f "$PWPLAY_PID" ]] && kill -0 "$(cat "$PWPLAY_PID")" 2>/dev/null; }

if is_running; then
	echo "pwplay-server already running (pid $(cat "$PWPLAY_PID"))"
	echo "  server: $PWPLAY_URL"
	exit 0
fi

echo "Building pwplay-server from $PWPLAY_SRC ..."
( cd "$PWPLAY_SRC" && go build -o "$PWPLAY_BIN" ./cmd/server )

# pwplay-server needs at least one file argument at startup. Generate a short
# silent placeholder track; the app replaces the queue over HTTP on first play.
if [[ ! -f "$PWPLAY_SEED" ]]; then
	echo "Generating silent placeholder track ..."
	ffmpeg -nostdin -loglevel error -f lavfi -i "anullsrc=r=44100:cl=stereo" \
		-t 1 -metadata title="(placeholder)" "$PWPLAY_SEED"
fi

echo "Starting pwplay-server on :$PWPLAY_PORT ..."
# Background a subshell that exec's the binary so $! is the server's own PID
# (see the same pattern in the tie test-env start.sh).
( exec nohup "$PWPLAY_BIN" "$PWPLAY_SEED" >"$PWPLAY_LOG" 2>&1 ) &
echo $! >"$PWPLAY_PID"
echo "  pid $(cat "$PWPLAY_PID"), log $PWPLAY_LOG"

echo "Waiting for pwplay-server to accept connections ..."
for _ in $(seq 1 30); do
	if curl -s -o /dev/null "$PWPLAY_URL/status"; then
		echo "pwplay-server is up."
		echo "  server:   $PWPLAY_URL"
		echo "  triplestore: http://localhost:2161"
		echo "  filehost: http://localhost:2162"
		exit 0
	fi
	sleep 0.2
done
echo "pwplay-server did not become ready; check $PWPLAY_LOG" >&2
exit 1
