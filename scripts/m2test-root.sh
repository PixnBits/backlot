#!/bin/sh
# Run host m2test as root (jailer). Invoked via: sudo -n /path/to/m2test-root.sh
# Compile as the sudoing user (discover go from their shell PATH).
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

# Discover go without baking a host path. /bin/bash explicitly (wrapper is dash).
# type -P not command -v (aliases). Sentinel GO= so rc noise cannot pollute.
# </dev/null so a bashrc read/ssh-add cannot hang. HOME from getent, never
# --preserve-environment (that keeps HOME=/root). stderr ignored (no job control).
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
  echo "m2test-root.sh: go not found on $user PATH (login then interactive bash); not faking a tty" >&2
  exit 1
fi
go_dir="$(dirname "$go_bin")"

# make stays non-login/non-interactive so cwd is not HOME.
# rootfs is .PHONY so this always rebuilds guest/artifacts/rootfs.ext4 (not -nt).
runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" world-runtime
runuser -u "$user" -- env HOME="$home" PATH="$go_dir:$PATH" make -C "$ROOT" rootfs

echo "m2test-root.sh: artifacts before exec:" >&2
ls -l -- "$ROOT/runtime/bin/world-runtime" "$ROOT/runtime/bin/m2test" "$ROOT/guest/artifacts/rootfs.ext4" >&2
sha256sum -- "$ROOT/runtime/bin/world-runtime" "$ROOT/runtime/bin/m2test" "$ROOT/guest/artifacts/rootfs.ext4" >&2

exec "$ROOT/runtime/bin/m2test"
