#!/usr/bin/env bash
# Wrap a MyGo Linux archive in a Fedora RPM.
#
#   scripts/package-rpm.sh dist/linux-amd64/meiro-1.2.3-linux-amd64.tar.gz artifacts
set -euo pipefail

archive="${1:?usage: scripts/package-rpm.sh <archive.tar.gz> <output-directory>}"
out="${2:?usage: scripts/package-rpm.sh <archive.tar.gz> <output-directory>}"
archive="$(realpath "$archive")"
out="$(realpath -m "$out")"
name="$(basename "$archive")"

case "$name" in
	meiro-*-linux-amd64.tar.gz)
		version="${name#meiro-}"
		version="${version%-linux-amd64.tar.gz}"
		rpm_arch=x86_64
		;;
	meiro-*-linux-arm64.tar.gz)
		version="${name#meiro-}"
		version="${version%-linux-arm64.tar.gz}"
		rpm_arch=aarch64
		;;
	*)
	printf 'Unsupported archive name: %s\n' "$name" >&2
	exit 2
	;;
esac

[[ -f "$archive" ]] || { printf 'No archive at %s\n' "$archive" >&2; exit 2; }
mkdir -p "$out"
top="$(mktemp -d)"
trap 'rm -rf "$top"' EXIT
mkdir -p "$top/SOURCES"
install -m 0644 "$archive" "$top/SOURCES/$name"

rpmbuild --target "$rpm_arch" -bb \
	--define "_topdir $top" \
	--define "_build_id_links none" \
	--define "app_version $version" \
	--define "archive $name" \
	--define "rpm_arch $rpm_arch" \
	packaging/meiro.spec

packages=("$top/RPMS/$rpm_arch"/*.rpm)
[[ ${#packages[@]} == 1 ]] || { printf 'Expected one RPM, found %s\n' "${#packages[@]}" >&2; exit 1; }
install -m 0644 "${packages[0]}" "$out/"
