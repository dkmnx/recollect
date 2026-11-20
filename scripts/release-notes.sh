#!/bin/sh
# Extract the CHANGELOG.md section for one tag.
# Usage: release-notes.sh <tag> [changelog]

set -eu

tag="${1:-}"
file="${2:-CHANGELOG.md}"

if [ -z "$tag" ]; then
	echo "usage: release-notes.sh <tag> [changelog]" >&2
	exit 2
fi
if [ ! -f "$file" ]; then
	echo "missing changelog: $file" >&2
	exit 1
fi

notes=$(awk -v tag="$tag" '
	$0 ~ "^## \\[" tag "\\]" { found = 1; next }
	found && /^## \[/ { exit }
	found { print }
' "$file")

if [ -z "$notes" ]; then
	echo "no changelog section for $tag in $file" >&2
	exit 1
fi

printf '%s\n' "$notes" | sed '/./,$!d'
