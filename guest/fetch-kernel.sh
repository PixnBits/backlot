#!/usr/bin/env bash
# Fetch the pinned Firecracker CI vmlinux. Recorded in pins.env / README.md.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=pins.env
source "${HERE}/pins.env"
OUT="${HERE}/artifacts/${KERNEL_NAME}"
mkdir -p "${HERE}/artifacts"
if [[ -f "${OUT}" ]]; then
  got="$(sha256sum "${OUT}" | awk '{print $1}')"
  if [[ "${got}" == "${KERNEL_SHA256}" ]]; then
    echo "kernel ok ${OUT}"
    exit 0
  fi
  echo "kernel checksum mismatch; re-downloading" >&2
fi
curl -fL --retry 3 -o "${OUT}.part" "${KERNEL_URL}"
got="$(sha256sum "${OUT}.part" | awk '{print $1}')"
if [[ "${got}" != "${KERNEL_SHA256}" ]]; then
  echo "checksum ${got} != ${KERNEL_SHA256}" >&2
  exit 1
fi
mv "${OUT}.part" "${OUT}"
echo "kernel ok ${OUT}"
