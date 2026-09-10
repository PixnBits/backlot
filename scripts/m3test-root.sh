#!/bin/sh
# Run host m3test as root (jailer). Invoked via: sudo -E ./scripts/m3test-root.sh
# Compile as the sudoing user. Only exec m3test as root.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export FIRECRACKER_BIN="${FIRECRACKER_BIN:-/usr/local/firecracker/v1.15.1/firecracker}"
export JAILER_BIN="${JAILER_BIN:-/usr/local/firecracker/v1.15.1/jailer}"
export BACKLOT_ROOT="${BACKLOT_ROOT:-$ROOT}"
export BACKLOT_FC_WAITKVM="${BACKLOT_FC_WAITKVM:-$ROOT/runtime/bin/fc-waitkvm}"

uid="${SUDO_UID:-${PKEXEC_UID:-}}"
if [ -z "$uid" ]; then
  echo "m3test-root.sh: need SUDO_UID or PKEXEC_UID (invoke via sudo or pkexec)" >&2
  exit 1
fi

ent="$(getent passwd "$uid")" || {
  echo "m3test-root.sh: getent passwd $uid failed" >&2
  exit 1
}
user="$(printf '%s' "$ent" | cut -d: -f1)"
gid="$(printf '%s' "$ent" | cut -d: -f4)"
home="$(printf '%s' "$ent" | cut -d: -f6)"

export SUDO_UID="${SUDO_UID:-$uid}"
export SUDO_GID="${SUDO_GID:-$gid}"
export SUDO_USER="${SUDO_USER:-$user}"

chown_if_root() {
  [ -e "$1" ] || return 0
  ow="$(stat -c %u "$1")"
  if [ "$ow" = 0 ]; then
    chown -R "$uid:$gid" "$1"
  fi
}
chown_if_root "$ROOT/runtime/bin"
chown_if_root "$ROOT/lot/bin"
chown_if_root "$ROOT/guest/artifacts"

discover_go() {
  flag="$1"
  line="$(runuser -u "$user" -- env HOME="$home" /bin/bash "$flag" \
    'builtin printf "GO=%s\n" "$(type -P go)"' </dev/null 2>/dev/null || true)"
  bin="$(printf '%s\n' "$line" | sed -n 's/^GO=//p' | tail -n 1)"
  case "$bin" in
    */go) ;;
    *) return 0 ;;
  esac
  if [ -x "$bin" ]; then
    printf '%s\n' "$bin"
  fi
}

go_bin="$(discover_go -lc || true)"
if [ -z "$go_bin" ]; then
  go_bin="$(discover_go -ic || true)"
fi
if [ -z "$go_bin" ]; then
  echo "m3test-root.sh: go not found on $user PATH" >&2
  exit 1
fi
go_dir="$(dirname "$go_bin")"

# Ensure guest artifacts exist (reuse sibling m2.1 rootfs if needed).
ensure_artifacts() {
  kern="$ROOT/guest/artifacts/vmlinux-6.1.102"
  rootfs="$ROOT/guest/artifacts/rootfs.ext4"
  mkdir -p "$ROOT/guest/artifacts"
  if [ ! -f "$kern" ]; then
    alt="/home/pixnbits/projects/backlot/feature/m2.1-jailer/guest/artifacts/vmlinux-6.1.102"
    if [ -f "$alt" ]; then
      cp -a "$alt" "$kern"
    fi
  fi
  if [ ! -f "$rootfs" ]; then
    alt="/home/pixnbits/projects/backlot/feature/m2.1-jailer/guest/artifacts/rootfs.ext4"
    if [ -f "$alt" ]; then
      cp -a "$alt" "$rootfs"
    fi
  fi
  if [ ! -f "$kern" ] || [ ! -f "$rootfs" ]; then
    runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" kernel
    runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" world-runtime
    runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" rootfs
  fi
}

runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" world-runtime lot-bins
ensure_artifacts

echo "m3test-root.sh: running lot/bin/m3test" >&2
exec "$ROOT/lot/bin/m3test"
