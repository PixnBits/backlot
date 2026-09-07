# TEST_REPORT — Backlot M2 one world

**Date:** 2026-09-02  
**Branch:** `feature/m2.1-jailer`  
**SHA:** `f8a02f5` (`world: bind-mount host /dev/kvm over jailer mknod`)  
**Honesty rule:** do not fake passes. Missing capability → `NOT RUN`.

## M2.1 note

Tenant `jail` JSON field removed; `POST /v1/worlds/{id}/exec` always runs `python3 /opt/backlot/inner/run.py` (leftover `"jail"` keys ignored). `m2test` fails M2-boot unless `engine=jailer`. Product shepherd does not pass `backlot.bare_exec=1`. Bare exec is cmdline-gated at `POST /v1/internal/bare-exec`.

**euid-0 jailer sitting: PASS** at `f8a02f5` (Tester, 2026-09-02). `M2-boot PASS engine=jailer`. Suite `exit 0`.

## Capability matrix (host inventory, 2026-08-30)

Not re-probed on the jailer sitting. Same desktop as the prior M2 report.

| Capability | Probe | Result |
|---|---|---|
| `bwrap` | `command -v bwrap` | **yes** — 0.11.1 |
| Unprivileged user namespaces | `bwrap --unshare-all … -- /usr/bin/id` | **yes** |
| `/dev/kvm` readable | `test -r /dev/kvm` | **yes** (ACL `user:pixnbits:rw-`; pixnbits not in group `kvm`) |
| `firecracker` | pinned binary `--version` | **v1.15.1** at `/usr/local/firecracker/v1.15.1/` (PATH `main` symlink is v1.16.0-dev; unused) |
| `jailer` | same dir `--version` | **v1.15.1** |
| Go | `go version` | go1.26.3 linux/amd64 |
| gcc | host | 15.2.0 (host M1 tests); guest image has bookworm gcc for in-guest T1–T10 |
| Python 3 | host / guest | host 3.14.4; guest bookworm 3.11 |
| debootstrap | on PATH | **no** — rootfs built with Docker `debian:bookworm` (no passwordless sudo) |

## Engine note (jailer)

This sitting ran jailer. Wrapper `scripts/m2test-root.sh` discovers `go` via `SUDO_USER`'s interactive shell (`bash -ic`), compiles `world-runtime` and rebuilds `guest/artifacts/rootfs.ext4` as that user, then execs `m2test` as root keeping `SUDO_UID`/`SUDO_GID`. Jailer `--parent-cgroup backlot-m2`. Dropped uid is not in group `kvm`; jailer mknods a 0600 chroot `/dev/kvm` that does not copy the host ACL. Shepherd bind-mounts host `/dev/kvm` over that node (no chown of the overlay, no kvm-group, no sudoers widen). Do not treat the Aug 30 `engine=firecracker` sitting as this run.

## `make test-unit` / `make test-int` (host)

**PASS** on 2026-08-30 — 33 unit, 15 int (T1–T10). M1 did not regress. **Not re-run** on the 2026-09-02 jailer sitting.

M2.1 code sitting (2026-08-30, uid 1000): `cd runtime && go test ./cmd/world-runtime ./world ./cmd/m2test ./cmd/shepherd` **PASS** (no Jail field; product boot_args have no `backlot.bare_exec`; `/v1/internal/bare-exec` is 404 unless `BACKLOT_BARE_EXEC=1`). **Not re-run** on 2026-09-02.

## `make test-m2` / euid-0 jailer sitting

**PASS** at `f8a02f5` (Tester, 2026-09-02). `M2-boot PASS engine=jailer`. Tester reported exec / inner 15 / decoy / no-kvm / no-audit / orphan all PASS and `exit 0`. `m2test` returns 1 on any of those fails, including `M2-start-exit`.

| Test | Result | Detail |
|---|---|---|
| M2-boot | PASS | `engine=jailer` (Tester). |
| M2-exec | PASS | Tester; m2test criterion is `POST /exec` `ls /workspace` → `hello.txt` through `inner/run.py`. |
| M2-inner | PASS | Tester: inner 15. Guest `run_int.py` (outside bwrap; it drives bwrap). |
| M2-decoy-host | PASS | Tester: decoy. |
| M2-start-exit | PASS | Implied by suite `exit 0` (`m2test` returns 1 if start/exit missing from host jsonl). |
| M2-no-kvm | PASS | Tester: guest has no `/dev/kvm`. |
| M2-no-audit | PASS | Tester: host jsonl path absent from guest. |
| M2-orphan | PASS | Tester: no leftover firecracker after SIGTERM. |

## Event path (O2 / O5)

M1 `inner/run.py` writes an audit jsonl on the kernel that runs bwrap. In M2 that kernel is the **guest** (`/run/backlot/inner-*.jsonl`). The host file is different. Guest `world-runtime` forwards those lines over AF_VSOCK to host CID 2 port 5252. Shepherd listens on `$uds_path_5252` and `O_APPEND`s. Not virtio-blk.

## What this does *not* prove

- Kubernetes / Kata / Tetragon / snapshot.
- Unbreakability. Residual risk: guest kernel + VMM + operator error, plus `inner/REVIEWER.md`.
- Lot-boss host API / session router / continuity desk (later PRs; Tester's guest-count check is an acceptance gate there).
