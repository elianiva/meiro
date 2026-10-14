#!/usr/bin/env bash
# Fails when a Go file is not gofumpt-formatted. The files are declared inputs
# of this test, so it runs hermetically and its result is cached until one of
# them changes.
set -euo pipefail

tool="$(realpath "$1")"
cd "${TEST_SRCDIR:?}/$TEST_WORKSPACE"

if output="$("$tool" -l .)"; then
	:
else
	# gofumpt exits non-zero when it cannot parse a file: that is a failure of
	# the check, not a list of files to report.
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
