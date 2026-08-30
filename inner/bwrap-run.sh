#!/usr/bin/env bash
# Backlot M1 inner-ring wrapper.
#
# This script does not invoke bwrap itself. It execs the Python driver, which:
#   1. loads policy.yaml
#   2. materializes a watermarked decoy tree (never the real secret path)
#   3. compiles a classic BPF seccomp filter (default ERRNO(EPERM))
#   4. creates a memfd holding struct sock_filter[] and passes that fd
#      to `bwrap --seccomp FD` — the fd number is whatever memfd_create
#      returned, not a magic 12
#   5. writes a hash-chained audit log *outside* the jail
#
# bwrap flags the driver always passes for the demo profile:
#   --unshare-all       new user/pid/net/ipc/uts/cgroup ns (I4: no --share-net)
#   --new-session       new session
#   --die-with-parent   I5; process tree dies with the wrapper
#   --tmpfs /           I1 default-deny mounts (unbound paths → ENOENT)
#   --proc /proc        fresh procfs, not a bind of host /proc
#   --dev /dev          synthetic devices, not host /dev
#   --bind <scratch> /workspace
#   --ro-bind <decoy> /opt/grok     decoy only; real /opt/grok is not a source
#   --seccomp FD
#   --chdir /workspace
#   --clearenv
#
# Usage:
#   inner/bwrap-run.sh [--policy PATH] [--audit PATH] [--workspace HOSTDIR]
#                      [--print-plan] [--no-decoy] -- command [args...]
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export PYTHONPATH="${HERE}${PYTHONPATH:+:$PYTHONPATH}"
exec python3 "${HERE}/run.py" "$@"
