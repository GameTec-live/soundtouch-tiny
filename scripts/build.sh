#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${ROOT}/build"
mkdir -p "${OUT}"

export CGO_ENABLED="${CGO_ENABLED:-0}"

build() {
  local goos="$1"
  local goarch="$2"
  local goarm="${3:-}"
  local suffix="${goos}-${goarch}"
  if [[ -n "${goarm}" ]]; then
    suffix="${suffix}v${goarm}"
  fi

  echo "building ${suffix}"
  GOOS="${goos}" GOARCH="${goarch}" GOARM="${goarm}" \
    go build -trimpath -ldflags="-s -w" -o "${OUT}/soundtouch-tiny-${suffix}" "${ROOT}"
}

build linux arm 7
build linux arm64
build linux amd64

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${OUT}" && sha256sum soundtouch-tiny-* > SHA256SUMS)
fi
