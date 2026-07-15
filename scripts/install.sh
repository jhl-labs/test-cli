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

if [[ ! "${REPO}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "invalid REPO (expected owner/name): ${REPO}" >&2
  exit 1
fi

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
  # Prefer the API because the releases/latest redirect can lag behind the
  # actual latest release after a new tag is published.
  body="$(curl -fsSL "${auth[@]}" "${api}/latest" 2>/dev/null || true)"
  VERSION="$(printf '%s' "${body}" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  if [ -z "${VERSION}" ] || [ "${VERSION}" = "latest" ]; then
    resolved="$(curl -fsSL "${auth[@]}" -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" 2>/dev/null || true)"
    VERSION="${resolved##*/tag/}"
  fi
fi
if [ -z "${VERSION}" ] || [ "${VERSION}" = "latest" ]; then
  echo "could not resolve a release version for ${REPO}" >&2; exit 1
fi
if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid VERSION (expected semver vX.Y.Z): ${VERSION}" >&2
  exit 1
fi

asset="${APP}_${VERSION}_${os}_${arch}${ext}"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
sums_url="https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS"

echo "Installing ${APP} ${VERSION} (${os}/${arch}) -> ${INSTALL_DIR}"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

curl -fsSL "${auth[@]}" -o "${tmp}/${asset}" "${url}"

# --- verify checksum (required; portable across Linux and macOS) ---
sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    echo "no SHA-256 tool found (need sha256sum, shasum, or openssl)" >&2
    return 1
  fi
}

if ! curl -fsSL "${auth[@]}" -o "${tmp}/SHA256SUMS" "${sums_url}"; then
  echo "could not download SHA256SUMS for ${VERSION}" >&2
  exit 1
fi
want="$(awk -v asset="${asset}" '$2 == asset {print $1; exit}' "${tmp}/SHA256SUMS" | tr '[:upper:]' '[:lower:]')"
if [ -z "${want}" ]; then
  echo "SHA256SUMS has no entry for ${asset}" >&2
  exit 1
fi
if [[ ! "${want}" =~ ^[0-9a-f]{64}$ ]]; then
  echo "SHA256SUMS contains an invalid digest for ${asset}" >&2
  exit 1
fi
got="$(sha256_file "${tmp}/${asset}" | tr '[:upper:]' '[:lower:]')"
if [ "${want}" != "${got}" ]; then
  echo "checksum mismatch for ${asset}" >&2
  exit 1
fi
echo "checksum verified"

staged="${tmp}/${APP}${ext}"
install -m 0755 "${tmp}/${asset}" "${staged}"
"${staged}" --version
install -m 0755 "${staged}" "${INSTALL_DIR}/${APP}${ext}"
echo "Installed ${INSTALL_DIR}/${APP}${ext}"
