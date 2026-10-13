#!/usr/bin/env bash
# Gets the programs a release ships inside the app, ffmpeg and yt-dlp, into
# resources/<platform>/, where `mygo build` picks them up. yt-dlp and QuickJS
# are downloaded; ffmpeg is built by scripts/build-ffmpeg.sh.
#
#   scripts/fetch-tools.sh darwin-arm64
#
# Platforms: darwin-arm64, darwin-amd64, linux-amd64, linux-arm64.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

platform="${1:?usage: fetch-tools.sh <darwin-arm64|darwin-amd64|linux-amd64|linux-arm64>}"

# yt-dlp ships as the one-directory build: the one-file build unpacks itself to
# a temporary directory on every launch, which costs about 0.4 s per resolve.
# yt-dlp breaks whenever YouTube changes, so every release takes the newest one
# unless YT_DLP_VERSION pins it. "latest" is turned into the release's tag up
# front, so one run names the version it fetched, and the checksums come from
# that same release. The release workflow resolves the tag once and passes it
# in, so every platform of a release bundles the same yt-dlp.
YT_DLP_VERSION="${YT_DLP_VERSION:-latest}"
if [[ "$YT_DLP_VERSION" == latest ]]; then
	# /releases/latest redirects to /releases/tag/<tag>.
	latest_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/yt-dlp/yt-dlp/releases/latest)"
	YT_DLP_VERSION="${latest_url##*/}"
	if [[ -z "$YT_DLP_VERSION" || "$YT_DLP_VERSION" == latest ]]; then
		printf 'Could not find the latest yt-dlp release\n' >&2
		exit 1
	fi
fi
ytdlp_url="https://github.com/yt-dlp/yt-dlp/releases/download/${YT_DLP_VERSION}"

# yt-dlp needs a JavaScript runtime to run the challenge solver scripts that
# come with it. The app bundles QuickJS rather than depending on one the user
# happens to have installed. Bump the version and the checksums together.
QUICKJS_VERSION="0.17.0"
quickjs_url="https://github.com/quickjs-ng/quickjs/releases/download/v${QUICKJS_VERSION}"

case "$platform" in
	darwin-arm64)
		ytdlp_asset="yt-dlp_macos.zip"
		quickjs_asset="qjs-darwin-arm64"
		quickjs_sha256="8be3ddfe3397d2e692e4e1e8972ee9d032a0a580505d2f8b4ea528cf1b651c11"
		;;
	darwin-amd64)
		ytdlp_asset="yt-dlp_macos.zip"
		quickjs_asset="qjs-darwin-x86_64"
		quickjs_sha256="9e5e101b4fd13cda3204222ca9f8be35412c41dcdef3745829633b7a67245412"
		;;
	linux-amd64)
		ytdlp_asset="yt-dlp_linux.zip"
		quickjs_asset="qjs-linux-x86_64"
		quickjs_sha256="0bfc02511a9f549c28b53880d988fc7cd5d361e90c5e8afdfcd7dc6774ceace5"
		;;
	linux-arm64)
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

# download fetches url into path. On a terminal it shows curl's progress bar,
# so a large file does not look like a hang; elsewhere (CI logs) it prints one
# line, as a bar would only fill the log with redraws.
download() { # url path
	if [[ -t 2 ]]; then
		echo "  downloading $(basename "$2")"
		curl -fL --progress-bar -o "$2" "$1"
	else
		echo "  downloading $(basename "$2") from $1"
		curl -fsSL -o "$2" "$1"
	fi
}

# prune_ytdlp drops what Meiro never uses from the one-directory build, which
# is more than half of it. Meiro only resolves YouTube audio URLs and reads
# browser cookies. curl_cffi (browser impersonation, 38 MB), Cryptodome and
# brotli are optional: yt-dlp falls back to its pure-Python AES and to
# no-brotli responses without them. cryptography stays, as the Linux keyring
# cookie path needs it through secretstorage.
prune_ytdlp() { # bin platform
	local bin="$1" platform="$2"
	rm -rf "$bin"/_internal/curl_cffi* "$bin"/_internal/Cryptodome* \
		"$bin"/_internal/_brotli.cpython-*.so "$bin"/_internal/websockets*

	[[ "$platform" == darwin-* ]] || return 0

	# The macOS zip is universal2, and every Mach-O in it carries both
	# architectures. Keep the one this build runs on. The executable itself
	# stays whole: PyInstaller finds its embedded archive by the file's layout,
	# and a thinned x86_64 one no longer starts.
	local arch=arm64
	[[ "$platform" == darwin-amd64 ]] && arch=x86_64
	local f
	while IFS= read -r -d '' f; do
		if [[ "$(lipo -archs "$f" 2>/dev/null)" == *" "* ]]; then
			lipo -thin "$arch" "$f" -output "$f.thin"
			mv "$f.thin" "$f"
			chmod +x "$f" 2>/dev/null || true
		fi
	done < <(find "$bin/_internal" -type f -print0)

	# _internal/Python is a second copy of the framework's binary.
	local fw
	fw="$(find "$bin/_internal/Python.framework/Versions" -maxdepth 2 -name Python -type f 2>/dev/null | head -n 1)"
	if [[ -n "$fw" && -f "$bin/_internal/Python" ]] && cmp -s "$fw" "$bin/_internal/Python"; then
		rm "$bin/_internal/Python"
		ln -s "${fw#"$bin/_internal/"}" "$bin/_internal/Python"
	fi
}

scripts/build-ffmpeg.sh "$platform"

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
	download "${ytdlp_url}/${ytdlp_asset}" "$work/yt-dlp.zip"
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
	prune_ytdlp "$out/bin" "$platform"
	printf '%s\n' "$expected" > "$out/bin/.yt-dlp.sha256"
fi

echo "quickjs ${QUICKJS_VERSION} for ${platform}"
if cached "$out/bin/qjs" "$quickjs_sha256"; then
	echo "  already downloaded"
else
	download "${quickjs_url}/${quickjs_asset}" "$work/qjs"
	verify "$work/qjs" "$quickjs_sha256"
	install -m 0755 "$work/qjs" "$out/bin/qjs"
fi
if [[ ! -f "$out/licenses/quickjs-MIT.txt" ]]; then
	curl -fsSL -o "$out/licenses/quickjs-MIT.txt" \
		"https://raw.githubusercontent.com/quickjs-ng/quickjs/v${QUICKJS_VERSION}/LICENSE"
fi

echo "ready in ${out}"
