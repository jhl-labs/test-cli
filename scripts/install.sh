#!/usr/bin/env bash
# Install the test-cli binary from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/jhl-labs/test-cli/main/scripts/install.sh | bash
#
# Environment:
#   VERSION       Release tag to install (default: latest)
#   INSTALL_DIR   Destination directory (default: /usr/local/bin, or ./bin if not writable)
#   REPO          owner/repo (default: jhl-labs/test-cli)
#   GITHUB_TOKEN  Optional token to avoid API rate limits.
set -euo pipefail

REPO="${REPO:-jhl-labs/test-cli}"
APP="test-cli"
VERSION="${VERSION:-latest}"

# --- detect platform ---
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${os}" in
  linux) os=linux ;;
  darwin) os=darwin ;;
  msys*|mingw*|cygwin*) os=windows ;;
  *) echo "unsupported OS: ${os}" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: ${arch}" >&2; exit 1 ;;
esac
ext=""; [ "${os}" = "windows" ] && ext=".exe"

# --- resolve install dir ---
if [ -z "${INSTALL_DIR:-}" ]; then
  if [ -w "/usr/local/bin" ]; then INSTALL_DIR="/usr/local/bin"; else INSTALL_DIR="./bin"; fi
fi
mkdir -p "${INSTALL_DIR}"

auth=()
[ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")

# --- resolve version ---
api="https://api.github.com/repos/${REPO}/releases"
if [ "${VERSION}" = "latest" ]; then
  # Resolve the latest tag from the releases/latest redirect first — it avoids
  # the api.github.com rate limit that unauthenticated environments hit. Fall
  # back to the API if the redirect is unavailable.
  resolved="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" 2>/dev/null || true)"
  VERSION="${resolved##*/tag/}"
  if [ "${VERSION}" = "${resolved}" ] || [ -z "${VERSION}" ] || [ "${VERSION}" = "latest" ]; then
    VERSION="$(curl -fsSL "${auth[@]}" "${api}/latest" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  fi
fi
if [ -z "${VERSION}" ] || [ "${VERSION}" = "latest" ]; then
  echo "could not resolve a release version for ${REPO}" >&2; exit 1
fi

asset="${APP}_${VERSION}_${os}_${arch}${ext}"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
sums_url="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"

echo "Installing ${APP} ${VERSION} (${os}/${arch}) -> ${INSTALL_DIR}"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

curl -fsSL "${auth[@]}" -o "${tmp}/${asset}" "${url}"

# --- verify checksum (best effort: skip if SHA256SUMS absent) ---
if curl -fsSL "${auth[@]}" -o "${tmp}/SHA256SUMS" "${sums_url}" 2>/dev/null; then
  want="$(grep " ${asset}\$" "${tmp}/SHA256SUMS" | awk '{print $1}')"
  if [ -n "${want}" ]; then
    got="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
    if [ "${want}" != "${got}" ]; then
      echo "checksum mismatch for ${asset}" >&2; exit 1
    fi
    echo "checksum verified"
  fi
fi

install -m 0755 "${tmp}/${asset}" "${INSTALL_DIR}/${APP}${ext}"
echo "Installed ${INSTALL_DIR}/${APP}${ext}"
"${INSTALL_DIR}/${APP}${ext}" --version || true
