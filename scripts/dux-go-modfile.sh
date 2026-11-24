#!/usr/bin/env bash
# Writes a copy of go.mod that builds dux-go with
# patches/dux_go_lyrics_windows.patch applied, and prints its path. Bazel
# patches the module itself. Plain go and `mygo build` read the module cache,
# where lyrics.Command does not compile for Windows, and Go refuses overlays
# for files in the module cache, so the copy replaces the whole module.
#
#   GOFLAGS="-modfile=$(scripts/dux-go-modfile.sh)" go test ./...
#
# The files go to .mygo/dux-go/. GOFLAGS splits at spaces, so the checkout's
# path must not have any.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

# Go on Windows cannot read Git Bash paths such as /d/..., so ask for D:/...
root="$(pwd -W 2>/dev/null || pwd)"
if [[ "$root" == *" "* ]]; then
	echo "dux-go-modfile.sh: GOFLAGS cannot hold a path with spaces: $root" >&2
	exit 1
fi
# Read the module from the real go.mod, even when GOFLAGS already names a copy.
# A fresh module cache, as on a release runner, does not have it yet.
GOFLAGS='' go mod download github.com/duxlabs-dev/dux-go
module="$(GOFLAGS='' go list -m -f '{{.Dir}}' github.com/duxlabs-dev/dux-go)"
if [[ -z "$module" ]]; then
	echo "dux-go-modfile.sh: cannot find github.com/duxlabs-dev/dux-go in the module cache" >&2
	exit 1
fi
out="$root/.mygo/dux-go"
rm -rf "$out"
mkdir -p "$out"
cp -R "$module" "$out/module"
chmod -R u+w "$out/module"
# A dux-go update that already has the fix, or that moved this code, makes
# the patch fail here rather than build a stale copy.
# Short flags only: the BSD patch on macOS has no long ones.
patch -s -t -N -p1 -d "$out/module" <patches/dux_go_lyrics_windows.patch

cp go.mod "$out/go.mod"
cp go.sum "$out/go.sum"
GOFLAGS='' go mod edit -modfile="$out/go.mod" -replace="github.com/duxlabs-dev/dux-go=$out/module"
echo "$out/go.mod"
