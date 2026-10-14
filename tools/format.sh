#!/usr/bin/env bash
# Formats every Go file in the workspace with the pinned gofumpt.
set -euo pipefail

tool="$(realpath "$1")"
shift
cd "${BUILD_WORKSPACE_DIRECTORY:?run this with bazel run //:format}"

# Only the repository's own files: the bazel-* trees and .git are skipped, so
# the formatter never rewrites a generated or vendored copy.
mapfile -t files < <(find . -name '*.go' -not -path './bazel-*' -not -path './.git/*')
if [[ ${#files[@]} -eq 0 ]]; then
	echo "no Go files found" >&2
	exit 1
fi
exec "$tool" -w "${files[@]}" "$@"
