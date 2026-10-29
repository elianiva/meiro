#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 || $# -gt 4 ]]; then
  echo "usage: $0 linux-{amd64,arm64} VERSION ARTIFACTS_DIR [minimal]" >&2
  exit 2
fi

platform=$1
version=$2
artifacts=$3
variant=${4:-full}
dist="dist/$platform"
arch=${platform#linux-}

if [[ "$variant" != full && "$variant" != minimal ]]; then
  echo "unsupported build variant: $variant" >&2
  exit 2
fi

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
  - libayatana-appindicator-gtk3
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

# The Flatpak runs in a sandbox with its own runtime, so it cannot find ffmpeg
# or yt-dlp on the host. Only the full build, which bundles them, gets one.
# It repackages the same files as the AppImage with `flatpak build-*`, which
# runs nothing from the target architecture, so arm64 needs no emulation.
if [[ "$variant" == full ]]; then
  if ! command -v flatpak >/dev/null; then
    echo "flatpak is required" >&2
    exit 2
  fi
  app_id=com.elianiva.meiro
  runtime_version=48
  build="$tmp/flatpak-build"
  repo="$tmp/flatpak-repo"
  flatpak build-init --arch="$appimage_arch" "$build" "$app_id" \
    org.gnome.Sdk org.gnome.Platform "$runtime_version"
  mkdir -p "$build/files/bin" "$build/files/share/applications" \
    "$build/files/share/icons/hicolor/512x512/apps"
  tar -xzf "$archive" -C "$build/files/bin"
  # Flatpak rejects an icon larger than 512x512, and the app icon is 1024x1024.
  convert "$dist/meiro.png" -resize 512x512 \
    "$build/files/share/icons/hicolor/512x512/apps/$app_id.png"
  sed -e 's/^Exec=.*/Exec=meiro %U/' -e "s/^Icon=.*/Icon=$app_id/" \
    "$dist/meiro.desktop" > "$build/files/share/applications/$app_id.desktop"
  # Import reads the user's browser profiles, and the Secret Service key that
  # encrypts a Chromium browser's cookies. The sign-in window also starts the
  # browser on the host through flatpak-spawn, which needs
  # org.freedesktop.Flatpak. That would leave the sandbox, so the bundle does
  # not ask for it: the dialog shows the override that grants it. A Flatpak
  # or Snap browser needs write access too: its sign-in profile is made in
  # its data.
  browser_access=(--talk-name=org.freedesktop.secrets)
  for dir in xdg-config/google-chrome xdg-config/BraveSoftware \
    xdg-config/microsoft-edge xdg-config/net.imput.helium \
    xdg-config/mozilla xdg-config/zen home/.mozilla home/.zen; do
    browser_access+=("--filesystem=$dir:ro")
  done
  for dir in com.google.Chrome org.chromium.Chromium com.microsoft.Edge \
    com.brave.Browser com.vivaldi.Vivaldi org.mozilla.firefox \
    app.zen_browser.zen; do
    browser_access+=("--filesystem=~/.var/app/$dir")
  done
  browser_access+=('--filesystem=~/snap/chromium' '--filesystem=~/snap/firefox')
  flatpak build-finish "$build" --command=meiro \
    --share=network --share=ipc \
    --socket=wayland --socket=fallback-x11 --socket=pulseaudio --device=dri \
    --talk-name=org.kde.StatusNotifierWatcher \
    --talk-name=org.freedesktop.Notifications \
    --own-name='org.mpris.MediaPlayer2.meiro.*' \
    "${browser_access[@]}"
  flatpak build-export --arch="$appimage_arch" "$repo" "$build"
  flatpak build-bundle --arch="$appimage_arch" \
    --runtime-repo=https://dl.flathub.org/repo/flathub.flatpakrepo \
    "$repo" "$artifacts/Meiro-$version-linux-$arch.flatpak" "$app_id"
fi

if [[ "$variant" == minimal ]]; then
  for file in "$artifacts"/*; do
    [[ -f "$file" ]] || continue
    if [[ "$file" == *.tar.gz ]]; then
      mv "$file" "${file%.tar.gz}-minimal.tar.gz"
    else
      mv "$file" "${file%.*}-minimal.${file##*.}"
    fi
  done
fi
