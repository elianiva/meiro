#!/usr/bin/env bash
# Builds the ffmpeg a release ships, into resources/<platform>/bin/ffmpeg.
#
#   scripts/build-ffmpeg.sh darwin-arm64
#   scripts/build-ffmpeg.sh windows-amd64
#
# Meiro uses ffmpeg for one thing: decoding the audio of one stream (Opus in
# WebM, AAC in MP4, and the files of the audio cache) to raw PCM, and reading
# the length of a cached file. So the build keeps just that: the demuxers,
# decoders and filters it needs, plus HTTP(S) for the stream itself. It is
# about 7 MB, where a full build is over 100 MB, and it is LGPL.
#
# Build tools: a C compiler, make, curl, xz, and on x86 nasm. Linux also needs
# OpenSSL's static libraries (libssl.a), and links them in. Linux releases
# build inside an Alpine container (see release.yml), so the binary is static
# against musl and runs on any distribution. The Windows build is cross-compiled
# on Linux with MinGW-w64 (gcc-mingw-w64-x86-64) and uses Windows' own TLS
# (Schannel), so it needs no OpenSSL.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

platform="${1:?usage: build-ffmpeg.sh <darwin-arm64|darwin-amd64|linux-amd64|linux-arm64|windows-amd64>}"

# Bump the version and the checksum together.
FFMPEG_VERSION="8.1"
ffmpeg_sha256="b072aed6871998cce9b36e7774033105ca29e33632be5b6347f3206898e0756a"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

exe=""
strip="strip"
if [[ "$platform" == windows-* ]]; then
	exe=".exe"
	strip="x86_64-w64-mingw32-strip"
fi

out="resources/${platform}"
mkdir -p "$out/bin" "$out/licenses"

# The binary is as current as the source and this script, so a later run, or
# a build that already got the binary from the release's ffmpeg job, can tell
# it need not build again.
stamp="${ffmpeg_sha256}-$(sha256 "${BASH_SOURCE[0]}")"
if [[ -x "$out/bin/ffmpeg$exe" && "$(cat "$out/bin/.ffmpeg.stamp" 2>/dev/null)" == "$stamp" ]]; then
	echo "ffmpeg ${FFMPEG_VERSION} for ${platform}: already built"
	exit 0
fi

# Everything is off unless listed. udp is listed because OpenSSL's TLS code
# calls into it.
# shellcheck disable=SC2054 # the commas are ffmpeg's own list syntax
configure=(
	--disable-everything --disable-autodetect --disable-doc --disable-debug
	--disable-ffplay --disable-ffprobe --enable-ffmpeg
	--disable-avdevice --disable-swscale --enable-swresample
	--enable-small --enable-version3
	--enable-protocol=file,pipe,http,https,httpproxy,tcp,tls,crypto,udp
	--enable-demuxer=matroska,mov,ogg,aac,mp3,flac,wav
	--enable-parser=opus,aac,vorbis,flac,mpegaudio
	--enable-decoder=opus,aac,vorbis,mp3,mp3float,flac,pcm_s16le,pcm_f32le
	--enable-encoder=pcm_s16le --enable-muxer=pcm_s16le
	--enable-filter=abuffer,abuffersink,aformat,aresample,anull,atrim,apad
)
case "$platform" in
	linux-*)
		configure+=(--enable-openssl --pkg-config-flags=--static --extra-ldflags=-static)
		;;
	darwin-arm64)
		configure+=(--enable-securetransport)
		;;
	darwin-amd64)
		# Built on an Apple Silicon runner for Intel.
		configure+=(
			--enable-securetransport --enable-cross-compile --arch=x86_64 --target-os=darwin
			--disable-x86asm --extra-cflags="-arch x86_64" --extra-ldflags="-arch x86_64"
		)
		;;
	windows-amd64)
		# Cross-compiled with MinGW-w64, and linked static so the one file
		# runs without any DLL beside it.
		configure+=(
			--enable-schannel --enable-cross-compile --arch=x86_64 --target-os=mingw32
			--cross-prefix=x86_64-w64-mingw32- --extra-ldflags=-static
		)
		;;
	*)
		printf 'Unsupported platform: %s\n' "$platform" >&2
		exit 1
		;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "ffmpeg ${FFMPEG_VERSION} for ${platform}"
curl -fsSL -o "$work/ffmpeg.tar.xz" "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"
if [[ "$(sha256 "$work/ffmpeg.tar.xz")" != "$ffmpeg_sha256" ]]; then
	printf 'Checksum mismatch for ffmpeg-%s.tar.xz\n' "$FFMPEG_VERSION" >&2
	exit 1
fi
tar -xJf "$work/ffmpeg.tar.xz" -C "$work"

src="$work/ffmpeg-${FFMPEG_VERSION}"
jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu)"
(cd "$src" && ./configure "${configure[@]}" && make -j"$jobs" "ffmpeg$exe")

install -m 0755 "$src/ffmpeg$exe" "$out/bin/ffmpeg$exe"
"$strip" "$out/bin/ffmpeg$exe"
cp "$src/COPYING.LGPLv3" "$out/licenses/ffmpeg-COPYING.LGPLv3"
printf '%s\n' "$stamp" > "$out/bin/.ffmpeg.stamp"
echo "  built $(du -h "$out/bin/ffmpeg$exe" | cut -f 1)"
