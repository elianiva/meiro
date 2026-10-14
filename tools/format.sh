#!/usr/bin/env bash
# Formats every Go file in the workspace with the pinned gofumpt, or, with
# --check, lists the ones that are not formatted and fails.
#
# The files come from walking the workspace rather than from a Bazel file
# list: a glob stops at a package boundary, and every directory of Go files
# here is its own package, so a list built in one package would see only that
# package's files.
set -euo pipefail

tool="$(realpath "$1")"
shift

check=false
if [[ "${1:-}" == "--check" ]]; then
	check=true
	shift
fi

cd "${BUILD_WORKSPACE_DIRECTORY:?run this with bazel run //:format}"

# Only the repository's own files: the bazel-* trees and .git are skipped, so
# the formatter never rewrites a generated or vendored copy.
mapfile -t files < <(find . -name '*.go' -not -path './bazel-*' -not -path './.git/*')
if [[ ${#files[@]} -eq 0 ]]; then
	echo "no Go files found" >&2
	exit 1
fi

if [[ "$check" != true ]]; then
	exec "$tool" -w "${files[@]}" "$@"
fi

# gofumpt -l exits 0 whether or not it listed a file, so the list is what
# decides; a non-zero exit means it could not read one, which is a failure of
# the check rather than a list of files to report.
if output="$("$tool" -l "${files[@]}" "$@")"; then
	:
else
	echo "$output" >&2
	echo "gofumpt could not read the files above." >&2
	exit 1
fi

if [[ -n "$output" ]]; then
	echo "Files needing gofumpt:"
	echo "$output"
	exit 1
fi
echo "gofumpt: all files formatted."
