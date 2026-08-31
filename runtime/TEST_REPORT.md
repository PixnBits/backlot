# TEST_REPORT — Backlot M2 one world

**Date:** 2026-08-30  
**Branch:** `feature/m2-world`  
**Honesty rule:** do not fake passes. Missing capability → `NOT RUN`.

## Capability matrix (this run)

| Capability | Probe | Result |
|---|---|---|
| `bwrap` | `command -v bwrap` | **yes** — 0.11.1 |
| Unprivileged user namespaces | `bwrap --unshare-all … -- /usr/bin/id` | **yes** |
| `/dev/kvm` readable | `test -r /dev/kvm` | **yes** (ACL; also writable) |
| `firecracker` | pinned binary `--version` | **v1.15.1** at `/usr/local/firecracker/v1.15.1/` (PATH `main` symlink is v1.16.0-dev; unused) |
| `jailer` | same dir `--version` | **v1.15.1** — see engine note |
| Go | `go version` | go1.26.3 linux/amd64 |
| gcc | host | 15.2.0 (host M1 tests); guest image has bookworm gcc for in-guest T1–T10 |
| Python 3 | host / guest | host 3.14.4; guest bookworm 3.11 |
| debootstrap | on PATH | **no** — rootfs built with Docker `debian:bookworm` (no passwordless sudo) |

## Engine note (jailer)

`jailer` v1.15.1 is pinned. Direct invoke as uid 1000 fails: `Failed to unshare into new mount namespace: Operation not permitted`. This sitting is not root and `sudo -n` is not available. `make test-m2` therefore started **Firecracker v1.15.1 as the kvm-capable user**, still with **no NIC** in the machine config and **vsock only**. Layout on disk is jailer-shaped (`jails/firecracker/<id>/root`). When euid is 0 the shepherd runs jailer. Do not pretend jailer confined this particular process tree.

## `make test-unit` / `make test-int` (host)

**PASS** — 33 unit, 15 int (T1–T10). M1 did not regress.

## `make test-m2`

**PASS** on this desktop.

| Test | Result | Detail |
|---|---|---|
| M2-boot | PASS | engine=firecracker; guest vsock `:8080` + event connect host:5252 |
| M2-exec | PASS | `POST /exec` `ls /workspace` → `hello.txt` through `inner/run.py` |
| M2-inner | PASS | `run_int.py` in the guest (outside bwrap; it drives bwrap). `summary: 15 pass / 0 fail / 0 not-run` |
| M2-decoy-host | PASS | `decoy_open` in **host** jsonl. Guest emitted on vsock; shepherd `O_APPEND`s. Host path was not a guest disk. |
| M2-start-exit | PASS | `start` and `exit` on the host file |
| M2-no-kvm | PASS | guest `NO_KVM` |
| M2-no-audit | PASS | host jsonl path absent from guest |
| M2-orphan | PASS | SIGTERM; vmm pid gone |

## Event path (O2 / O5)

M1 `inner/run.py` writes an audit jsonl on the kernel that runs bwrap. In M2 that kernel is the **guest** (`/run/backlot/inner-*.jsonl`). The host file is different. Guest `world-runtime` forwards those lines over AF_VSOCK to host CID 2 port 5252. Shepherd listens on `$uds_path_5252` and `O_APPEND`s. Not virtio-blk.

## What this does *not* prove

- Jailer chroot on this uid (needs CAP_SYS_ADMIN).
- Kubernetes / Kata / Tetragon / snapshot.
- Unbreakability. Residual risk: guest kernel + VMM + operator error, plus `inner/REVIEWER.md`.
