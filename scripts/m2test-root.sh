#!/bin/sh
# Run host m2test as root (jailer). Invoked via: sudo -n /path/to/m2test-root.sh
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export FIRECRACKER_BIN="${FIRECRACKER_BIN:-/usr/local/firecracker/v1.15.1/firecracker}"
export JAILER_BIN="${JAILER_BIN:-/usr/local/firecracker/v1.15.1/jailer}"
export BACKLOT_ROOT="${BACKLOT_ROOT:-$ROOT}"
cd "$ROOT"
make world-runtime
exec "$ROOT/runtime/bin/m2test"
