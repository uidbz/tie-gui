#!/bin/bash
# Commit everything and push.
#
# Usage: ./push-to-repo.sh "commit message"
set -euo pipefail

if [ $# -eq 0 ]; then
	echo "usage: $(basename "$0") <commit message>" >&2
	exit 1
fi

git add -A
git commit -m "$*"
git push
