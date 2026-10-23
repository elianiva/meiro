#!/usr/bin/env bash
# Verify both published Meiro releases exist, are public, and have assets.
#
# It does not check GitHub's latest marker. GET /releases/latest returns the
# newest non-draft, non-prerelease release even when it was created with
# --latest=false, so desktop-vX.Y.Z being "latest" is expected and needs no
# action. Never mark or unmark a release as latest by hand.
set -euo pipefail
version=${1:?usage: verify_release.sh VERSION}
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
	echo "version must be numeric x.y.z without a v prefix" >&2
	exit 2
}

for tag in "desktop-v$version" "desktop-minimal-v$version"; do
	release=$(gh release view "$tag" --json tagName,isDraft,isPrerelease,assets,url)
	echo "$release" | jq -e --arg tag "$tag" '.tagName == $tag and (.isDraft | not) and (.isPrerelease | not) and (.assets | length > 0)' >/dev/null || {
		echo "$tag is a draft, a prerelease, or has no assets" >&2
		exit 1
	}
	echo "$release" | jq -r '"published \(.tagName): \(.assets | length) assets\n\(.url)"'
done
