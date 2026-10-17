#!/bin/bash
# Tag the current commit and push the tag.
#
# Usage: ./release_version.sh 7.1      # -> tag v0.7.1
set -euo pipefail

if [ $# -ne 1 ]; then
	echo "usage: $(basename "$0") <version>   (e.g. 7.1 -> tag v0.7.1)" >&2
	exit 1
fi

# The old form was `git tag v0.$@`: with no argument it tagged "v0." and with
# several it produced a tag name from the joined arguments.
if ! [[ $1 =~ ^[0-9]+(\.[0-9]+)+$ ]]; then
	echo "version must look like 7.1 or 7.1.2, got '$1'" >&2
	exit 1
fi

tag="v0.$1"
git tag "$tag"
git push origin "$tag"
echo "pushed $tag"
