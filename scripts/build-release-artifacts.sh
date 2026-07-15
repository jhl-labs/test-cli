#!/usr/bin/env bash
# Build cross-platform test-cli release artifacts (bare binaries + archives +
# SHA256SUMS). Mirrors the security-cli release layout so the install script and
# GitHub Action can consume either tool identically.
set -euo pipefail

APP_NAME="${APP_NAME:-test-cli}"
VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUT_DIR="${OUT_DIR:-dist/release}"
VERSION_PACKAGE="${VERSION_PACKAGE:-github.com/jhl-labs/test-cli/internal/version}"
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}"
INCLUDE_ARCHIVES="${INCLUDE_ARCHIVES:-true}"

if [[ ! "${APP_NAME}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  echo "invalid APP_NAME: ${APP_NAME}" >&2
  exit 1
fi
if [[ ! "${VERSION}" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "invalid VERSION: ${VERSION}" >&2
  exit 1
fi
if [ "${INCLUDE_ARCHIVES}" != "true" ] && [ "${INCLUDE_ARCHIVES}" != "false" ]; then
  echo "INCLUDE_ARCHIVES must be true or false" >&2
  exit 1
fi
if [ -z "${OUT_DIR}" ] || [ -L "${OUT_DIR}" ]; then
  echo "OUT_DIR must be a non-symlink directory path" >&2
  exit 1
fi
mkdir -p "${OUT_DIR}"
out_abs="$(cd "${OUT_DIR}" && pwd -P)"
work_abs="$(pwd -P)"
home_abs="$(cd "${HOME}" && pwd -P)"
case "${out_abs}" in
  /|"${work_abs}"|"${home_abs}")
    echo "refusing unsafe OUT_DIR: ${out_abs}" >&2
    exit 1
    ;;
esac
OUT_DIR="${out_abs}"

LDFLAGS="-s -w"
LDFLAGS="${LDFLAGS} -X ${VERSION_PACKAGE}.Version=${VERSION}"
LDFLAGS="${LDFLAGS} -X ${VERSION_PACKAGE}.Commit=${COMMIT}"
LDFLAGS="${LDFLAGS} -X ${VERSION_PACKAGE}.Date=${DATE}"

rm -rf -- "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

echo "Building ${APP_NAME} ${VERSION} (${COMMIT})"
for platform in ${PLATFORMS}; do
  goos="${platform%%/*}"
  goarch="${platform##*/}"
  case "${goos}/${goarch}" in
    linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
    *) echo "unsupported release platform: ${platform}" >&2; exit 1 ;;
  esac
  ext=""
  if [ "${goos}" = "windows" ]; then ext=".exe"; fi

  base="${APP_NAME}_${VERSION}_${goos}_${goarch}"
  bin_path="${OUT_DIR}/${base}${ext}"

  echo "  -> ${base}${ext}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${bin_path}" ./cmd/test-cli

  if [ "${INCLUDE_ARCHIVES}" = "true" ]; then
    if [ "${goos}" = "windows" ]; then
      ( cd "${OUT_DIR}" && zip -q "${base}.zip" "${base}${ext}" )
    else
      ( cd "${OUT_DIR}" && tar -czf "${base}.tar.gz" "${base}${ext}" )
    fi
  fi
done

echo "Generating SHA256SUMS"
( cd "${OUT_DIR}" && sha256sum -- * > SHA256SUMS )

echo "Artifacts written to ${OUT_DIR}:"
ls -1 "${OUT_DIR}"
