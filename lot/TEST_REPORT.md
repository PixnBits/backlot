# Lot-boss / M3 Phase 2 — TEST_REPORT

## Capability matrix (this sitting)

| Probe | Result |
|---|---|
| Date (PT) | 2026-09-07 |
| Host | pixnbits-Desktop (AMD Ryzen AI Max) |
| euid (agent shell) | 1000 (pixnbits) |
| `sudo -n` for m3test-root | **no** (NOPASSWD only for m2.1-jailer path until sudoers updated) |
| `/dev/kvm` | readable+writable (ACL user:pixnbits) |
| jailer / firecracker | `/usr/local/firecracker/v1.15.1/` → **v1.15.1** |
| bwrap | present |
| Go / Docker / compose | present |
| Tag `m2.1` | `6d9a148` (engine=jailer) — not retargeted |
| PR #6 | open on `feature/m3-compose-cp` |

## Phase 1 bars (must stay green)

| Bar | Result |
|---|---|
| `make test-compose-cp` | run on this branch before push |
| desk+router no `/dev/kvm` | required |
| lease without lot-boss → 503 | required |
| `/health` 200 | required |

## Phase 2 — `make test-m3`

| Test | Result |
|---|---|
| M3-lease-3 | **NOT RUN** — need `sudo -E ./scripts/m3test-root.sh` (jailer euid=0 + SUDO_UID) |
| M3-exec | **NOT RUN** |
| M3-isolation | **NOT RUN** |
| M3-decoy | **NOT RUN** |
| M3-scale-down | **NOT RUN** |
| M3-demand | **NOT RUN** |
| M3-no-kvm-guest | **NOT RUN** |
| M3-orphan | **NOT RUN** |
| M3-tenant-jail | **NOT RUN** |

`make test-m3` exits **2** without KVM or without root/jailer. That is intentional.

### How the Tester sits `make test-m3`

1. Install sudoers fragment (once): copy `scripts/backlot-sudoers.example` → `/etc/sudoers.d/backlot` (paths for this worktree).
2. Ensure guest artifacts: `guest/artifacts/vmlinux-6.1.102` and `rootfs.ext4` (or let `m3test-root.sh` copy from m2.1 / rebuild).
3. From repo root:

```bash
make test-compose-cp   # Phase 1 still green
sudo -E ./scripts/m3test-root.sh
# equivalent: sudo -E make test-m3
```

4. Expect `summary: M3 Phase 2 PASS` and `engine=jailer` on all three worlds.

## Implementation notes

- Lot-boss shepherds via `runtime/world` (jailer+Firecracker v1.15.1). Cap 3, warm pool 0|1.
- Unique guest CIDs per world (`GuestCID` in StartOpts).
- Guest vsock events are tailed into the continuity **desk** (not only per-world tempfile).
- Compose packing: desk+router unprivileged; lot-boss is host shepherd — not three microVMs in one container.
- Phase 3: `ttl_pause` clock + Firecracker Pause/Resume via API sock; userspace egress-proxy (`dark`/`proxy`). `ttl_store`/`ttl_prune` still not claimed.
- Guests remain vsock-only (no NIC). Kubernetes is not the M3 product.


## Phase 3 — pause + userspace proxy (2026-09-12)

Unit sit on `feature/m3-phase3` (no sudo this run):

| Bar | Result |
|---|---|
| `ttl_pause` clock pauses after idle, Resume on activity | PASS (`TestConsiderPauseHoldsRAMState`) |
| Firecracker API sock (`--api-sock`, not `--no-api`) | present in `runtime/world` |
| PUT `/v1/worlds/{id}/network` `dark`/`proxy` (not 501) | PASS (`TestNetworkDarkAndProxy`) |
| Bad phase / unknown world | PASS |
| `getPM` N/A — egress-proxy default deny, allowlist env | PASS (`proxy` `TestHostOf`) |
| PRD/README claim Kubernetes as M3 product | gone; §15.1 is Compose-local |
| `ttl_store` / `ttl_prune` | still not claimed |
| Live `make test-m3` (Phase 2 fleet) | needs sudo/jailer — Tester re-sit |

