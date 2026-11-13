#!/usr/bin/env bash
# Builds a production app for one platform, with its playback tools, into
# build/<platform>/.
#
#   bazel run //:dist -- linux-amd64
#   bazel run //:dist -- --minimal darwin-universal
#   bazel run //:dist -- --run darwin-universal
#
# MyGo owns the bundle and the package layout of each system, and the
# platform tooling it drives (codesign, hdiutil, NSIS) only exists on the
# system it targets, so this target prepares the resources and then hands the
# bundle to `mygo build`. Bazel caches the Go compiles that produce the app
# itself; see //:meiro and the --config=<platform> cross builds.
set -euo pipefail

cd "${BUILD_WORKSPACE_DIRECTORY:?run this with bazel run //:dist}"

usage() {
	echo "usage: bazel run //:dist -- [--minimal] [--out DIR] [--run] <platform>" >&2
	echo "platforms: darwin-universal darwin-arm64 darwin-amd64 linux-amd64 linux-arm64 windows-amd64" >&2
}

minimal=false
open=false
out=""
platform=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--minimal) minimal=true ;;
	--run) open=true ;;
	--out)
		out="${2:?--out needs a directory}"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		echo "unknown flag: $1" >&2
		usage
		exit 2
		;;
	*)
		if [[ -n "$platform" ]]; then
			echo "one platform at a time: got $platform and $1" >&2
			exit 2
		fi
		platform="$1"
		;;
	esac
	shift
done

if [[ -z "$platform" ]]; then
	usage
	exit 2
fi

# A universal macOS app holds one copy of each tool, built from the per-arch
# resource directories MyGo combines, so both are fetched.
case "$platform" in
darwin-universal) fetch_platforms=(darwin-arm64 darwin-amd64) ;;
*) fetch_platforms=("$platform") ;;
esac

for fetch_platform in "${fetch_platforms[@]}"; do
	# The release passes ffmpeg between jobs as an artifact, which loses the
	# executable bit; the app cannot ship a tool it cannot run, and
	# fetch-tools.sh would rebuild one that is not executable.
	for ffmpeg in "resources/$fetch_platform/bin/ffmpeg" "resources/$fetch_platform/bin/ffmpeg.exe"; do
		if [[ -f "$ffmpeg" ]]; then
			chmod +x "$ffmpeg"
		fi
	done
	if [[ "$minimal" == true ]]; then
		scripts/fetch-tools.sh --minimal "$fetch_platform"
	else
		scripts/fetch-tools.sh "$fetch_platform"
	fi
done

# The Last.fm application key and secret are baked into the app. `mygo build`
# sets its own -ldflags, so they cannot be passed with -X; a generated file
# sets them instead, and goes when the build is done. Without them the app
# builds, and its Settings say Last.fm cannot be connected.
lastfm_keys=lastfmkeys_gen.go
# The PostHog project key and the Sentry DSN are baked in the same way, for
# the install ping and crash reports. Without them the app builds and sends
# nothing: a build with no key never asks, and one with no DSN offers no
# crash reports.
privacy_keys=privacykeys_gen.go
trap 'rm -f "$lastfm_keys" "$privacy_keys"' EXIT
if [[ -n "${MEIRO_LASTFM_API_KEY:-}" || -n "${MEIRO_LASTFM_SECRET:-}" ]]; then
	for value in "${MEIRO_LASTFM_API_KEY:-}" "${MEIRO_LASTFM_SECRET:-}"; do
		if [[ ! "$value" =~ ^[A-Za-z0-9]+$ ]]; then
			echo "MEIRO_LASTFM_API_KEY and MEIRO_LASTFM_SECRET must both be set, to letters and digits only" >&2
			exit 2
		fi
	done
	printf 'package main\n\nfunc init() { lastFMAPIKey, lastFMSecret = "%s", "%s" }\n' \
		"$MEIRO_LASTFM_API_KEY" "$MEIRO_LASTFM_SECRET" >"$lastfm_keys"
fi
if [[ -n "${MEIRO_POSTHOG_KEY:-}" ]]; then
	if [[ ! "$MEIRO_POSTHOG_KEY" =~ ^[A-Za-z0-9_-]+$ ]]; then
		echo "MEIRO_POSTHOG_KEY must be set, to letters, digits, underscores and dashes only" >&2
		exit 2
	fi
	printf 'package main\n\nfunc init() { posthogProjectKey = "%s" }\n' "$MEIRO_POSTHOG_KEY" >"$privacy_keys"
fi
if [[ -n "${MEIRO_SENTRY_DSN:-}" ]]; then
	case "$MEIRO_SENTRY_DSN" in
	*[[:space:]\"\']*)
		echo "MEIRO_SENTRY_DSN must be a DSN with no spaces, quotes or backslashes" >&2
		exit 2
		;;
	esac
	# A release sets both, so the DSN joins the PostHog key's file, which
	# already has the one package clause a Go file takes.
	if [[ ! -f "$privacy_keys" ]]; then
		printf 'package main\n' >"$privacy_keys"
	fi
	printf '\nfunc init() { sentryProjectDSN = "%s" }\n' "$MEIRO_SENTRY_DSN" >>"$privacy_keys"
fi
build=(build -platform "${platform/-//}")
if [[ -n "$out" ]]; then
	build+=(-o "$out")
fi
go tool mygo "${build[@]}"

if [[ "$open" != true ]]; then
	exit 0
fi

dir="${out:-build}/$platform"
case "$platform" in
darwin-*) open "$dir/Meiro.app" ;;
windows-*) "$dir/Meiro.exe" ;;
*) "$dir/meiro" ;;
esac
