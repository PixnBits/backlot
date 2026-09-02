#!/bin/sh
# Guest init. No DHCP. No NIC address. Vsock is the only control plane.
set -eu
export PATH=/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin

mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
mkdir -p /dev/pts /run /tmp /workspace
mount -t devpts devpts /dev/pts 2>/dev/null || true
mount -t tmpfs tmpfs /run
mount -t tmpfs tmpfs /tmp

# O3: if a nic exists, it stays down and unnumbered.
if command -v ip >/dev/null 2>&1; then
  for n in /sys/class/net/*; do
    [ -e "$n" ] || continue
    iface=$(basename "$n")
    [ "$iface" = lo ] && continue
    ip link set "$iface" down 2>/dev/null || true
    ip addr flush dev "$iface" 2>/dev/null || true
  done
  ip link set lo up 2>/dev/null || true
fi

# Guest-local inner audit (I6 vs bwrap). Not the host jsonl.
mkdir -p /run/backlot
chmod 755 /run/backlot

echo "backlot-m2 init: starting world-runtime" >/dev/kmsg 2>/dev/null || true

# m2test-only: kernel cmdline backlot.bare_exec=1 enables in-guest run_int.py
# via POST /v1/internal/bare-exec. Product boots do not set this.
if grep -q 'backlot.bare_exec=1' /proc/cmdline 2>/dev/null; then
  export BACKLOT_BARE_EXEC=1
fi

exec /usr/local/bin/world-runtime
