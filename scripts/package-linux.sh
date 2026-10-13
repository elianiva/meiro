#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 linux-{amd64,arm64} VERSION ARTIFACTS_DIR" >&2
  exit 2
fi

platform=$1
version=$2
artifacts=$3
dist="dist/$platform"
arch=${platform#linux-}

case "$platform" in
  linux-amd64) appimage_arch=x86_64 ;;
  linux-arm64) appimage_arch=aarch64 ;;
  *) echo "unsupported Linux platform: $platform" >&2; exit 2 ;;
esac

if [[ -z "${APPIMAGETOOL:-}" || ! -x "$APPIMAGETOOL" ]]; then
  echo "APPIMAGETOOL must point to an executable appimagetool" >&2
  exit 2
fi
if ! command -v nfpm >/dev/null || ! command -v dpkg-deb >/dev/null; then
  echo "nfpm and dpkg-deb are required" >&2
  exit 2
fi

mkdir -p "$artifacts"
deb=$(find "$dist" -maxdepth 1 -type f -name '*.deb' -print -quit)
archive=$(find "$dist" -maxdepth 1 -type f -name '*.tar.gz' -print -quit)
if [[ -z "$deb" || -z "$archive" || ! -f "$dist/meiro.desktop" || ! -f "$dist/meiro.png" ]]; then
  echo "MyGo Linux build is missing its .deb, archive, desktop entry, or icon: $dist" >&2
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# MyGo packs the .deb with gzip. xz makes it about a fifth smaller, and every
# dpkg reads it.
deb_root="$tmp/deb-root"
dpkg-deb --raw-extract "$deb" "$deb_root"
dpkg-deb --root-owner-group -Zxz -Sextreme --build "$deb_root" "$artifacts/$(basename "$deb")"
cp "$archive" "$artifacts/"

# Build an RPM with the same installed file layout as MyGo's Debian package.
rpm_root="$tmp/rpm-root"
mkdir -p "$rpm_root"
dpkg-deb --extract "$artifacts/$(basename "$deb")" "$rpm_root"
cat > "$tmp/nfpm.yaml" <<'EOF'
name: meiro
arch: ${ARCH}
platform: linux
version: ${VERSION}
maintainer: Meiro contributors
description: YouTube Music desktop client
license: GPL-3.0-only
homepage: https://github.com/elianiva/meiro
depends:
  - gtk3
  - webkit2gtk4.1
rpm:
  compression: xz
contents:
  - src: ${RPM_ROOT}/
    dst: /
    type: tree
    expand: true
EOF
ARCH="$arch" VERSION="$version" RPM_ROOT="$rpm_root" \
  nfpm package --config "$tmp/nfpm.yaml" --packager rpm \
    --target "$artifacts/Meiro-$version-linux-$arch.rpm"

# Keep the executable and its resources together under usr/bin so MyGo can
# resolve bundled resources beside its executable inside the AppImage.
appdir="$tmp/Meiro.AppDir"
mkdir -p "$appdir/usr/bin"
tar -xzf "$archive" -C "$appdir/usr/bin"
cp "$dist/meiro.desktop" "$appdir/meiro.desktop"
cp "$dist/meiro.png" "$appdir/meiro.png"
sed -i 's/^Exec=.*/Exec=AppRun/' "$appdir/meiro.desktop"
cat > "$appdir/AppRun" <<'EOF'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
exec "$HERE/usr/bin/meiro" "$@"
EOF
chmod +x "$appdir/AppRun"

APPIMAGETOOL_APP_NAME=Meiro VERSION="$version" ARCH="$appimage_arch" \
  "$APPIMAGETOOL" --appimage-extract-and-run --no-appstream \
    "$appdir" "$artifacts/Meiro-$version-linux-$arch.AppImage"
