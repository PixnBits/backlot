#!/bin/sh
# Run host m2test as root (jailer). Invoked via: sudo -n /path/to/m2test-root.sh
# Compile as the sudoing user (discover go from their interactive PATH).
# Only exec m2test as root. Do not widen sudoers. Do not bake a host go path.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export FIRECRACKER_BIN="${FIRECRACKER_BIN:-/usr/local/firecracker/v1.15.1/firecracker}"
export JAILER_BIN="${JAILER_BIN:-/usr/local/firecracker/v1.15.1/jailer}"
export BACKLOT_ROOT="${BACKLOT_ROOT:-$ROOT}"

uid="${SUDO_UID:-${PKEXEC_UID:-}}"
if [ -z "$uid" ]; then
  echo "m2test-root.sh: need SUDO_UID or PKEXEC_UID (invoke via sudo or pkexec)" >&2
  exit 1
fi

ent="$(getent passwd "$uid")" || {
  echo "m2test-root.sh: getent passwd $uid failed" >&2
  exit 1
}
user="$(printf '%s' "$ent" | cut -d: -f1)"
gid="$(printf '%s' "$ent" | cut -d: -f4)"
home="$(printf '%s' "$ent" | cut -d: -f6)"

# dropIDs reads these from m2test's env (sudo injects them; pkexec has PKEXEC_UID only).
# Do not use env -i. gid comes from getent, never gid=uid.
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
chown_if_root "$ROOT/guest/artifacts"

# .bashrc (where go often lives) is skipped by non-interactive bash, including
# `bash -lc`. Interactive `-i` is what actually sources it. Do not bake a path.
# stderr discarded: interactive bash may warn about no TTY.
go_bin="$(runuser -u "$user" -- bash -ic 'command -v go' 2>/dev/null || true)"
if [ -z "$go_bin" ]; then
  go_bin="$(runuser -u "$user" -- bash -lc 'command -v go' 2>/dev/null || true)"
fi
if [ -z "$go_bin" ]; then
  echo "m2test-root.sh: go not found on $user interactive PATH" >&2
  exit 1
fi
go_dir="$(dirname "$go_bin")"

# Discovery may use bash -i; make stays non-login/non-interactive so cwd is not HOME.
# rootfs is .PHONY so this always rebuilds guest/artifacts/rootfs.ext4 (not -nt).
runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" world-runtime
runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" rootfs

echo "m2test-root.sh: artifacts before exec:" >&2
ls -l -- "$ROOT/runtime/bin/world-runtime" "$ROOT/runtime/bin/m2test" "$ROOT/guest/artifacts/rootfs.ext4" >&2
sha256sum -- "$ROOT/runtime/bin/world-runtime" "$ROOT/runtime/bin/m2test" "$ROOT/guest/artifacts/rootfs.ext4" >&2

exec "$ROOT/runtime/bin/m2test"
