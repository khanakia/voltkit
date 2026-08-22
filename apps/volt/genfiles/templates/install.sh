#!/bin/sh
# [[.Binary]] installer for macOS / Linux.
#
# Usage:
#   curl -fsSL [[.RawScriptURL]] | sh
#   VERSION=v1.2.0 curl -fsSL ... | sh        # pin a version
#   INSTALL_DIR=~/.local/bin curl -fsSL ... | sh   # no sudo needed
set -eu

REPO="[[.Repo]]"
BINARY="[[.Binary]]"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS (use install.ps1 on Windows)" >&2; exit 1 ;;
esac

# /releases/latest/download/ is a redirect with NO rate limit; the JSON API
# is limited to 60 anonymous requests/hour/IP and would 403 on shared IPs.
if [ -n "$VERSION" ]; then
  BASE="[[.DownloadBase]]"
else
  BASE="[[.LatestBase]]"
  # A forge with no latest-release redirect renders this empty — then a
  # version is required, loudly (FG-D4).
  [ -z "${BASE}" ] && { echo "this forge has no 'latest' redirect — pass a version: install.sh vX.Y.Z" >&2; exit 1; }
  VERSION="latest"
fi
# Asset names mirror volt's platform.AssetName — generated from the same
# constants, so they cannot drift from what the build produced.
ASSET="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  # latest/download serves assets by exact name; we cannot know the version
  # half of the name, so fetch checksums.txt first and read it from there.
  ASSET=$(curl -fsSL "${BASE}/checksums.txt" | awk '{print $2}' | grep "_${OS}_${ARCH}\.tar\.gz$" | head -n1)
  [ -n "$ASSET" ] || { echo "no ${OS}/${ARCH} asset in the latest release of ${REPO}" >&2; exit 1; }
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading ${ASSET} from ${REPO} (${VERSION})..."
curl -fsSL -o "${tmp}/${ASSET}" "${BASE}/${ASSET}"
curl -fsSL -o "${tmp}/checksums.txt" "${BASE}/checksums.txt"

# Verify BEFORE installing — never pipe an unverified binary into PATH.
# sha256sum is coreutils; stock macOS ships only shasum — support both.
if command -v sha256sum >/dev/null 2>&1; then
  SUMCHECK="sha256sum -c -"
else
  SUMCHECK="shasum -a 256 -c -"
fi
(cd "$tmp" && grep " ${ASSET}\$" checksums.txt | $SUMCHECK >/dev/null) || {
  echo "checksum verification FAILED for ${ASSET} — refusing to install" >&2; exit 1; }

tar xzf "${tmp}/${ASSET}" -C "$tmp"
[ -f "${tmp}/${BINARY}" ] || { echo "${BINARY} not found in archive" >&2; exit 1; }

if [ -x "${INSTALL_DIR}/${BINARY}" ]; then
  echo "Replacing $("${INSTALL_DIR}/${BINARY}" --version 2>/dev/null || echo "existing install")"
fi
if [ -w "$INSTALL_DIR" ]; then
  mv "${tmp}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
elif command -v sudo >/dev/null 2>&1; then
  sudo mv "${tmp}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  echo "${INSTALL_DIR} is not writable and sudo is unavailable." >&2
  echo "Re-run with INSTALL_DIR set to a writable directory, e.g.:" >&2
  echo "  INSTALL_DIR=\$HOME/.local/bin sh install.sh" >&2
  exit 1
fi
chmod +x "${INSTALL_DIR}/${BINARY}"

case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) echo "NOTE: ${INSTALL_DIR} is not on your PATH." >&2 ;;
esac
echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"
