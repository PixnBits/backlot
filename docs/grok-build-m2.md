# Grok Build prompt — Backlot M2

Paste everything below the line into Grok Build. Prefer a **local** Grok Build / coding session on a Linux machine where `/dev/kvm` is readable and `firecracker` + `jailer` can run. Use the **site** sandbox only to generate code and unit tests; do not treat a green site run as “the world works.”

M1 is closed (merged, inner ring honest). Do not add inner-ring features. Put a microVM around the jail that already exists.

---

You are implementing **Backlot M2** (one world, no fleet) in the existing repo.

## Repo and docs (read first)

- Repo: https://github.com/PixnBits/backlot
- Tag / base: `m1` on `main` (merge of PR #1). If the tag is missing, use `main` after that merge.
- Contract: `docs/prd.md` — especially §9 invariants, **§15.2 (Go)**, **§15.7 (raw Firecracker + jailer)**. Those two forks are closed. Do not reopen them.
- Inner ring (do not rewrite): `inner/`, `docs/agent-sandbox-meta-prompt.md`, `docs/grok-build-m1.md`
- This prompt is the M2 spec.

M2 is **not** Kubernetes, not Kata, not a fleet, not Tetragon, not snapshot/restore. It is one Firecracker microVM, jailed, running the M1 driver, with a tiny Go API and a host-side event file.

## Decisions already made (do not re-litigate)

| Fork | Decision |
|---|---|
| Engine for the first world | Raw Firecracker + jailer on a KVM laptop/VM. Kata-fc waits until a cluster exists. |
| world-runtime language | Go. Kubernetes gravity wins at M3; Firecracker’s Rust does not need a second runtime. |

## Where this may run

Detect the environment at the start of work and print a capability matrix:

| Capability | How to probe |
|---|---|
| `bwrap` on PATH | `command -v bwrap` |
| Unprivileged user namespaces | `bwrap --unshare-user --unshare-pid echo ok` |
| `/dev/kvm` readable | `test -r /dev/kvm` |
| `firecracker` on PATH | `command -v firecracker && firecracker --version` |
| `jailer` on PATH | `command -v jailer && jailer --version` |
| Go toolchain | `go version` |
| gcc | `command -v gcc` (guest T1–T10, or host-built and copied in) |
| Python 3 in the *guest* plan | recorded in the rootfs manifest |

Rules:

- **No readable `/dev/kvm`:** implement all code, run host unit tests, mark `make test-m2` as `NOT RUN: no /dev/kvm`. Do not fake a VMM. Do not hang on nested virt.
- **Readable `/dev/kvm`:** run the world for real. If the world leaks (`/dev/kvm` in the guest, host audit file in the guest workspace, orphan `firecracker` after VMM death, T1–T10 fail inside the guest), **fail loud**. Do not skip.
- Pin a **released** Firecracker version in `guest/` (record the version string). A host `-dev` symlink is fine to *use* if you print `firecracker --version` into the report; it is not a pin.
- Do not implement Kata, Kubernetes, or a second language.

## Scope (do this)

### 1. `guest/` — one tiny world image

- Rootfs: **pinned Debian debootstrap** (default) or Buildroot. Pick one, pin the suite/snapshot or Buildroot version, and write the pin in `guest/README.md`.
- Debian is the default because M1’s driver is Python and needs `bwrap` in the guest. Smaller can wait.
- Kernel: a Firecracker-capable kernel config, committed or fetched from a pinned URL with checksum.
- Firecracker machine config JSON (boot source, rootfs drive, vsock, jailer-friendly paths). **No guest NIC**, or the NIC is created down and never gets an address. Vsock only.
- The rootfs contains:
  - `bwrap`
  - Python 3
  - the in-tree `inner/` tree (the M1 driver, unchanged)
  - the Go `world-runtime` binary
- The rootfs does **not** contain `/dev/kvm`, the host audit file, host SSH keys, or a bind of the host `/`.

### 2. `runtime/` — Go world-runtime + host shepherd

Host side (outside the guest):

- Starts jailer + Firecracker for **one** world.
- Owns a vsock (or a documented static-tap that the guest cannot use as egress — vsock preferred).
- Tails events from the guest onto a **host** jsonl file (`runtime/var/world-<id>.jsonl` or similar). `O_APPEND`. The guest cannot unlink or truncate it because it is not in the guest.
- On VMM exit or SIGTERM to the shepherd: no leftover `firecracker` process, no leftover guest vCPU / vhost threads.

Guest side (`world-runtime`):

- Listens on vsock.
- Implements **only** `POST /v1/worlds/{id}/exec` for this slice (health is allowed if it keeps the binary honest). Request: `{argv, stdin?, timeout}`. Response: `{exit_code, stdout, stderr}`.
- Exec path is `inner/run.py` / `inner/bwrap-run.sh`. Do not invent a second jail.
- Forwards `start`, `decoy_open`, and `exit` events to the host tail. Those events already exist in M1’s audit writer; ship them, do not re-encode the policy.

API surface that waits (return 501 or do not register): lease clocks, files, network phase, heartbeat, destroy-as-snapshot, cluster desk.

### 3. `make test-m2`

Skip cleanly without KVM:

```
NOT RUN: /dev/kvm is not readable
```

exit code 2 (same honesty rule as M1 `test-int`).

If KVM exists, fail (exit 1) when any of the following is true:

| Test | Must hold |
|---|---|
| M2-boot | Guest reaches vsock health or first exec |
| M2-exec | `POST /v1/worlds/{id}/exec` with `ls /workspace` (or equivalent) runs through `inner/run.py` |
| M2-inner | T1–T10 still **PASS** *inside the guest* (reuse `inner/tests/int/run_int.py`; do not rewrite the C programs) |
| M2-decoy-host | A decoy `open` of `/opt/grok/...` appears in the **host** jsonl |
| M2-start-exit | `start` and `exit` events appear in the **host** jsonl |
| M2-no-kvm | Guest does not have a usable `/dev/kvm` (`ENOENT` or not a char device the guest can open) |
| M2-no-audit | The host audit/event file path is not present in the guest workspace or jail mounts |
| M2-orphan | After SIGTERM to the VMM/shepherd: `pgrep -a firecracker` is empty; no leftover guest vCPU threads |

Write results to `runtime/TEST_REPORT.md` (or `guest/TEST_REPORT.md`). Missing capability → `NOT RUN`. Never fake a pass.

### 4. Docs in-tree

- `guest/README.md` — how to build the rootfs and kernel, versions, checksums.
- Root `README.md` — M2 section: `make test-m2`, KVM requirement, pointer to this prompt.
- Do not claim unbreakable. Residual risk is guest kernel + VMM + operator error, plus everything already in `inner/REVIEWER.md`.

## Scope (do not do)

- No Kubernetes manifests, RuntimeClass, k3s, warm pool, TTL controller.
- No Tetragon / Falco DaemonSet.
- No network phases beyond “guest nic down / vsock only.”
- No widening the M1 seccomp profile so an LLM agent “can work.”
- No snapshot/restore (M3 pause, M4 store).
- No second inner-ring language, no replacing `inner/run.py`.
- No virtio-fs. Firecracker does not have it; do not pretend.
- Nested Firecracker is a non-goal. The VMM must not expose `/dev/kvm` to the guest.

## Invariants you must not violate

Inner ring, unchanged, **inside the guest**:

I1 default-deny mounts (`ENOENT` for unbound paths).
I2 real `/opt/grok` invisible; decoy optional and watermarked.
I3 syscall allow-list; binary name is not policy.
I4 no ambient network in the demo profile.
I5 `--die-with-parent`.
I6 audit sink not writable from inside the *bwrap jail*.
I7 print policy hash, seccomp hash, decoy checksum at start.

Outer ring, new in M2:

O1 `/dev/kvm` is not in the guest.
O2 The host event/audit file is not in the guest workspace (and not in the bwrap jail).
O3 Guest has no usable network except vsock to the shepherd.
O4 Killing the VMM leaves no orphan `firecracker` or guest vCPU.
O5 Decoy opens and start/exit events are visible on the **host** file.

Do not weaken an invariant to make a test pass. Leave it failing and explain.

## Definition of done

- `make test-unit` and `make test-int` still pass on the host (M1 did not regress).
- `make test-m2` is `NOT RUN` without KVM, and **PASS** on a KVM machine for M2-boot through M2-orphan.
- A reviewer can read the Firecracker JSON, the jailer invocation, and the vsock path in one sitting.
- `TEST_REPORT.md` is honest.

Start by printing the capability matrix, restating I1–I7 and O1–O5 in your own words, then implement, then run whatever tests the environment allows.

---

End prompt.
