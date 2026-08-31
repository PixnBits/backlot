#!/usr/bin/env bash
# Build a pinned Debian bookworm ext4 with inner/ + world-runtime.
# Uses Docker (this host has no passwordless sudo for debootstrap).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "${HERE}/.." && pwd)"
# shellcheck source=pins.env
source "${HERE}/pins.env"

OUT="${HERE}/artifacts/rootfs.ext4"
RUNTIME_BIN="${REPO}/runtime/bin/world-runtime"
if [[ ! -x "${RUNTIME_BIN}" ]]; then
  echo "missing ${RUNTIME_BIN}; run: make -C runtime world-runtime" >&2
  exit 1
fi
command -v docker >/dev/null || { echo "docker required to debootstrap without sudo" >&2; exit 1; }

mkdir -p "${HERE}/artifacts"
echo "building bookworm rootfs via docker (debootstrap ${DEBIAN_SUITE})..."

docker run --rm --privileged \
  -v "${HERE}:/guest:ro" \
  -v "${REPO}/inner:/inner:ro" \
  -v "${RUNTIME_BIN}:/world-runtime:ro" \
  -v "${HERE}/artifacts:/out" \
  debian:bookworm bash /guest/docker-debootstrap.sh

ls -lh "${OUT}"
echo "rootfs ok ${OUT}"
