# Guest image — Backlot M2

One world. No fleet. Pins are closed here before any Go is written.

## Pins (2026-08-30)

| Piece | Pin | Notes |
|---|---|---|
| Engine | **Firecracker v1.15.1** | Host path `/usr/local/firecracker/v1.15.1/`. Do not use the `main` → `v1.16.0-dev` symlink. |
| Jailer | **Jailer v1.15.1** | Same directory. sha256 `4830a9b1fc6cece036d8992ff12f1fe9c5247aacad77f42c7aba683c7a08622e` |
| Firecracker binary | v1.15.1 | sha256 `7e8b57e88c459396d4680d83dcdd8c7f72305447cb55b11f4ac98ad70a3f7825` |
| Debian | **bookworm** (12) | `debootstrap --variant=minbase` from `http://deb.debian.org/debian`. Built in Docker because this host has no passwordless sudo. |
| Guest kernel | Firecracker CI **vmlinux-6.1.102** x86_64 | URL and sha256 below. Includes `CONFIG_VIRTIO_VSOCKETS`. |

Kernel:

```
https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.11/x86_64/vmlinux-6.1.102
sha256: cf42303c29e8c4a02798f357ba056c5567baf074aaed4eec78c997fb9df08cf9
```

Fetch:

```bash
guest/fetch-kernel.sh
```

Rootfs (needs Docker; writes `guest/artifacts/rootfs.ext4`, not committed):

```bash
guest/build-rootfs.sh
```

Packages in the image: `python3`, `bubblewrap`, `gcc`, `libc6-dev` (so in-guest `run_int.py` can compile T1–T10), `iproute2`, `util-linux`, `procps`. Plus in-tree `inner/` and the Go `world-runtime` binary.

## Network

No guest NIC in the Firecracker config. If a virtio-net ever appears, `/sbin/init` brings it down and does not assign an address. Control plane is **vsock only**.

- Guest CID `3`
- Guest listens `AF_VSOCK:8080` (HTTP exec API)
- Guest **connects** to host CID `2` port `5252` and writes JSON lines
- Host listens on `$uds_path_5252` and `O_APPEND`s those lines to a **host** jsonl

The host jsonl is a different file from M1’s in-guest `inner/run.py` audit. Do not virtio-blk it into the guest. Guest emits; shepherd appends. That is O2/O5.

Jailer v1.15.1 is the pin. Unprivileged `jailer` on this desktop fails `unshare` (needs CAP_SYS_ADMIN). The shepherd runs jailer when euid is 0; otherwise Firecracker as the kvm-capable user, same config (no NIC, vsock only).

M2.1: Firecracker `boot_args` are generated in `runtime/world`. Product shepherd does not add `backlot.bare_exec=1`. Only `m2test` does; guest `init.sh` then exports `BACKLOT_BARE_EXEC=1` so `world-runtime` registers `POST /v1/internal/bare-exec` for in-guest `run_int.py`. Tenant `POST /v1/worlds/{id}/exec` always jails.

## What is not in the image

`/dev/kvm`, the host event jsonl, host SSH keys, a bind of host `/`.
