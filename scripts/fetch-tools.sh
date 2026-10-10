#!/usr/bin/env bash
# Downloads the programs a release ships inside the app, ffmpeg and yt-dlp,
# into resources/<platform>/, where `mygo build` picks them up.
#
#   scripts/fetch-tools.sh darwin-arm64
#
# Platforms: darwin-arm64, darwin-amd64, linux-amd64, linux-arm64.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

platform="${1:?usage: fetch-tools.sh <darwin-arm64|darwin-amd64|linux-amd64|linux-arm64>}"

# ffmpeg comes from Jellyfin's portable GPL builds, which are static, carry
# no non-free parts and come with the source they were built from. Bump the
# version and the checksums together.
FFMPEG_VERSION="8.1.3-1"
ffmpeg_url="https://github.com/jellyfin/jellyfin-ffmpeg/releases/download/v${FFMPEG_VERSION}"

# yt-dlp ships as the one-directory build: the one-file build unpacks itself to
# a temporary directory on every launch, which costs about 0.4 s per resolve.
# yt-dlp breaks whenever YouTube changes, so a release takes the newest one
# unless YT_DLP_VERSION pins it. Its checksums come from the release itself.
YT_DLP_VERSION="${YT_DLP_VERSION:-latest}"
if [[ "$YT_DLP_VERSION" == latest ]]; then
	ytdlp_url="https://github.com/yt-dlp/yt-dlp/releases/latest/download"
else
	ytdlp_url="https://github.com/yt-dlp/yt-dlp/releases/download/${YT_DLP_VERSION}"
fi

# yt-dlp needs a JavaScript runtime to run the challenge solver scripts that
# come with it. The app bundles QuickJS rather than depending on one the user
# happens to have installed. Bump the version and the checksums together.
QUICKJS_VERSION="0.17.0"
quickjs_url="https://github.com/quickjs-ng/quickjs/releases/download/v${QUICKJS_VERSION}"

case "$platform" in
	darwin-arm64)
		ffmpeg_asset="macarm64"
		ffmpeg_sha256="22445d7299742749ad2eeb9ce87963d50def0357e45b3e6c7b69987c8365dbf6"
		ytdlp_asset="yt-dlp_macos.zip"
		quickjs_asset="qjs-darwin-arm64"
		quickjs_sha256="8be3ddfe3397d2e692e4e1e8972ee9d032a0a580505d2f8b4ea528cf1b651c11"
		;;
	darwin-amd64)
		ffmpeg_asset="mac64"
		ffmpeg_sha256="cb2b5c154d49a6b6bfe29fa6c16510e85dcbf60b805764121a167b093d4bb6fb"
		ytdlp_asset="yt-dlp_macos.zip"
		quickjs_asset="qjs-darwin-x86_64"
		quickjs_sha256="9e5e101b4fd13cda3204222ca9f8be35412c41dcdef3745829633b7a67245412"
		;;
	linux-amd64)
		ffmpeg_asset="linux64"
		ffmpeg_sha256="b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a"
		ytdlp_asset="yt-dlp_linux.zip"
		quickjs_asset="qjs-linux-x86_64"
		quickjs_sha256="0bfc02511a9f549c28b53880d988fc7cd5d361e90c5e8afdfcd7dc6774ceace5"
		;;
	linux-arm64)
		ffmpeg_asset="linuxarm64"
		ffmpeg_sha256="7bc8e8d0986f7f4693f63e9728570f671f07b6e21c05d5465dd7ce461d1631dc"
		ytdlp_asset="yt-dlp_linux_aarch64.zip"
		quickjs_asset="qjs-linux-aarch64"
		quickjs_sha256="3372133484edf50a69f3c67903af41206d22a061e930e3cfb63269272ef56d2e"
		;;
	*)
		printf 'Unsupported platform: %s\n' "$platform" >&2
		exit 1
		;;
esac

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

verify() { # file expected
	local actual
	actual="$(sha256 "$1")"
	if [[ "$actual" != "$2" ]]; then
		printf 'Checksum mismatch for %s\n  expected %s\n  got      %s\n' "$1" "$2" "$actual" >&2
		exit 1
	fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

out="resources/${platform}"
mkdir -p "$out/bin" "$out/licenses"

# cached reports whether the file at path already has the expected checksum,
# so a tool that is already downloaded is not fetched again.
cached() { # path expected
	[[ -f "$1" ]] || return 1
	[[ "$(sha256 "$1")" == "$2" ]]
}

echo "ffmpeg ${FFMPEG_VERSION} for ${platform}"
archive="jellyfin-ffmpeg_${FFMPEG_VERSION}_portable_${ffmpeg_asset}-gpl.tar.xz"
if cached "$out/bin/ffmpeg" "$ffmpeg_sha256"; then
	echo "  already downloaded"
else
	curl -fsSL -o "$work/ffmpeg.tar.xz" "${ffmpeg_url}/${archive}"
	verify "$work/ffmpeg.tar.xz" "$ffmpeg_sha256"
	tar -xJf "$work/ffmpeg.tar.xz" -C "$work" ffmpeg
	install -m 0755 "$work/ffmpeg" "$out/bin/ffmpeg"
fi
if [[ ! -f "$out/licenses/ffmpeg-COPYING.GPLv3" ]]; then
	curl -fsSL -o "$out/licenses/ffmpeg-COPYING.GPLv3" \
		"https://raw.githubusercontent.com/jellyfin/jellyfin-ffmpeg/v${FFMPEG_VERSION}/COPYING.GPLv3"
fi

echo "yt-dlp ${YT_DLP_VERSION} for ${platform}"
curl -fsSL -o "$work/SHA2-256SUMS" "${ytdlp_url}/SHA2-256SUMS"
expected="$(awk -v name="$ytdlp_asset" '$2 == name || $2 == "*" name { print $1 }' "$work/SHA2-256SUMS")"
[[ -n "$expected" ]] || { printf 'No checksum for %s\n' "$ytdlp_asset" >&2; exit 1; }
# The zip holds the executable and an _internal/ directory it finds beside
# itself, so both are installed into bin/. The zip's checksum is recorded to
# tell a later run that the download is current.
if [[ -x "$out/bin/yt-dlp" && -d "$out/bin/_internal" && "$(cat "$out/bin/.yt-dlp.sha256" 2>/dev/null)" == "$expected" ]]; then
	echo "  already downloaded"
else
	curl -fsSL -o "$work/yt-dlp.zip" "${ytdlp_url}/${ytdlp_asset}"
	verify "$work/yt-dlp.zip" "$expected"
	unzip -q "$work/yt-dlp.zip" -d "$work/yt-dlp"
	rm -rf "$out/bin/_internal" "$out/bin/yt-dlp"
	cp -R "$work/yt-dlp/_internal" "$out/bin/_internal"
	install -m 0755 "$work/yt-dlp/${ytdlp_asset%.zip}" "$out/bin/yt-dlp"
	# The macOS zip flattens the versioned-framework symlinks of
	# _internal/Python.framework into duplicate real files, which codesign
	# rejects as an ambiguous bundle format. Restore the canonical layout
	# so the build's nested signing accepts it.
	fw="$out/bin/_internal/Python.framework"
	if [[ -d "$fw" && -d "$fw/Versions/Current" && ! -L "$fw/Versions/Current" ]]; then
		ver=""
		for d in "$fw"/Versions/*/; do
			b="$(basename "$d")"
			[[ "$b" == "Current" ]] && continue
			ver="$b"
			break
		done
		if [[ -n "$ver" && -f "$fw/Versions/$ver/Python" ]]; then
			rm -f "$fw/Python"
			rm -rf "$fw/Resources" "$fw/Versions/Current"
			ln -s "$ver" "$fw/Versions/Current"
			ln -s "Versions/Current/Python" "$fw/Python"
			ln -s "Versions/Current/Resources" "$fw/Resources"
		fi
	fi
	printf '%s\n' "$expected" > "$out/bin/.yt-dlp.sha256"
fi

echo "quickjs ${QUICKJS_VERSION} for ${platform}"
if cached "$out/bin/qjs" "$quickjs_sha256"; then
	echo "  already downloaded"
else
	curl -fsSL -o "$work/qjs" "${quickjs_url}/${quickjs_asset}"
	verify "$work/qjs" "$quickjs_sha256"
	install -m 0755 "$work/qjs" "$out/bin/qjs"
fi
if [[ ! -f "$out/licenses/quickjs-MIT.txt" ]]; then
	curl -fsSL -o "$out/licenses/quickjs-MIT.txt" \
		"https://raw.githubusercontent.com/quickjs-ng/quickjs/v${QUICKJS_VERSION}/LICENSE"
fi

echo "ready in ${out}"
