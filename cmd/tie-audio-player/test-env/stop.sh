#!/usr/bin/env bash
# Tear down the pwplay-server. By default the underlying tie stack is left
# running (other tools may share it); pass --all to also stop the tie stack.
set -euo pipefail
source "$(dirname "$0")/env.sh"

if [[ -f "$PWPLAY_PID" ]]; then
	pid="$(cat "$PWPLAY_PID")"
	if kill -0 "$pid" 2>/dev/null; then
		echo "Stopping pwplay-server (pid $pid) ..."
		kill "$pid" 2>/dev/null || true
	fi
	rm -f "$PWPLAY_PID"
else
	echo "pwplay-server not running (no pid file)"
fi

if [[ "${1:-}" == "--all" ]]; then
	echo "Stopping tie stack via $TIE_TEST_ENV/stop.sh ..."
	"$TIE_TEST_ENV/stop.sh"
fi
