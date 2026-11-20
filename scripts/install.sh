#!/bin/sh
# Install the recollect engine from a GitHub release.
# Usage: install.sh [--version vX.Y.Z] [--dir DIR]

set -eu

REPO="dkmnx/recollect"
BINARY="recollect"
BASE_URL="${RECOLLECT_BASE_URL:-https://github.com/$REPO/releases/download}"
INSTALL_DIR="${RECOLLECT_INSTALL_DIR:-$HOME/.local/bin}"
VERSION=""

usage() {
	cat <<'EOF'
usage: install.sh [--version vX.Y.Z] [--dir DIR]

  --version   release tag to install (default: latest)
  --dir       install directory (default: $RECOLLECT_INSTALL_DIR or ~/.local/bin)
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		VERSION="${2:-}"
		shift 2
		;;
	--dir)
		INSTALL_DIR="${2:-}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

need() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "missing required command: $1" >&2
		exit 1
	}
}

need curl
need tar

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*)
	echo "unsupported OS: $os" >&2
	exit 1
	;;
esac

machine=$(uname -m)
case "$machine" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
	echo "unsupported architecture: $machine" >&2
	exit 1
	;;
esac

if [ -z "$VERSION" ]; then
	VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
	if [ -z "$VERSION" ]; then
		echo "cannot resolve the latest release" >&2
		exit 1
	fi
fi

case "$VERSION" in
v*) tag="$VERSION" ;;
*) tag="v$VERSION" ;;
esac
version="${tag#v}"
asset="${BINARY}_${version}_${os}_${arch}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' INT TERM EXIT

curl -fsSL "$BASE_URL/$tag/$asset" -o "$tmp/$asset"
curl -fsSL "$BASE_URL/$tag/checksums.txt" -o "$tmp/checksums.txt"

expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/checksums.txt" | head -n1)
if [ -z "$expected" ]; then
	echo "no checksum for $asset" >&2
	exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
else
	actual=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
fi
if [ "$actual" != "$expected" ]; then
	echo "checksum mismatch for $asset" >&2
	exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$INSTALL_DIR"
if command -v install >/dev/null 2>&1; then
	install -m 755 "$tmp/$BINARY" "$INSTALL_DIR/$BINARY"
else
	cp "$tmp/$BINARY" "$INSTALL_DIR/$BINARY"
	chmod 755 "$INSTALL_DIR/$BINARY"
fi

echo "installed $INSTALL_DIR/$BINARY ($tag)"
case ":$PATH:" in
*":$INSTALL_DIR:"*) ;;
*) echo "Add $INSTALL_DIR to PATH: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
