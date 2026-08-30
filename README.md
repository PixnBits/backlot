# Backlot

Open worlds for LLM agents.

Not a disposable sandbox. A **leased world**: Firecracker (or Kata-fc) as the outer ring, Bubblewrap as the inner ring, eBPF tripwires on host *and* guest, honeypot binds as false fronts. The agent works in one world for the length of the job. Hostility is an input.

**Repo is private.** Product name is the film lot: constructed streets, real doors that open onto plywood, continuity notes on every take.

## Status

M1 — inner ring, merged ([PR #1](https://github.com/PixnBits/backlot/pull/1)). Tag `m1` is that merge. See [inner/](inner/).

M0/M2 paper is the contract: [docs/prd.md](docs/prd.md). Engine and language are closed (§15.2 Go, §15.7 raw Firecracker + jailer).

## Rings

```
router → KVM node (host eBPF) → microVM → world-runtime
                                      → tool jails (bwrap + seccomp)
                                      → decoy flats
                                      → guest telemetry sidecar
                                      → audit sink (off-world)
```

## M1 — inner ring on a laptop

This is **not** Kubernetes, not Firecracker, not a fleet. It is a
Bubblewrap jail with a generated seccomp allow-list, a watermarked decoy
at `/opt/grok`, and a hash-chained audit log the jail cannot rewrite.

### Install Bubblewrap

```bash
# Debian / Ubuntu
sudo apt install bubblewrap

# Fedora
sudo dnf install bubblewrap
```

Unprivileged user namespaces must be enabled (default on modern distros).
Confirm:

```bash
command -v bwrap
bwrap --unshare-all --die-with-parent --tmpfs / --proc /proc --dev /dev \
  --ro-bind /usr /usr --symlink usr/bin /bin --symlink usr/lib /lib \
  --ro-bind /lib64 /lib64 -- /usr/bin/id
```

### Tests

```bash
make test-unit   # Python 3 only — policy compiler, argv, seccomp table, audit chain
make test-int    # needs bwrap + gcc; runs T1–T10 as real C programs
make test        # both
```

`make test-unit` must pass on any Linux with Python 3. `make test-int`
is the only way to claim the inner ring works. A green unit run is not
that claim.

### Run a command in the demo jail

```bash
inner/bwrap-run.sh --workspace /path/to/scratch -- ls /workspace
inner/bwrap-run.sh --print-plan -- ls /opt/grok
```

The driver prints `policy_hash`, `seccomp_hash`, and `decoy_checksum` on
start (I7). The audit log is *not* mounted into the jail.

### Layout

| Path | What |
|---|---|
| [inner/policy.yaml](inner/policy.yaml) | Source of truth |
| [inner/bwrap-run.sh](inner/bwrap-run.sh) | Wrapper (documented flags; no magic fds) |
| [inner/run.py](inner/run.py) | Driver: decoy + seccomp + bwrap + audit |
| [inner/artifacts/syscall-table.txt](inner/artifacts/syscall-table.txt) | Human-readable allow/deny table |
| [inner/TEST_REPORT.md](inner/TEST_REPORT.md) | Honest T1–T10 results |
| [inner/REVIEWER.md](inner/REVIEWER.md) | Second-pair-of-eyes checklist |

## Docs

- [PRD](docs/prd.md) — contract, API sketch, milestones M0–M4
- [Inner-ring agent spec](docs/agent-sandbox-meta-prompt.md) — invariants and tests T1–T10
- [Related work](docs/related-work.md) — Crew, E2B, Kata, what we will not copy
- [Grok Build prompt (M1)](docs/grok-build-m1.md) — site vs local test split
- [Grok Build prompt (M2)](docs/grok-build-m2.md) — one Firecracker world; `NOT RUN` if no `/dev/kvm`

## Next

Paste [docs/grok-build-m2.md](docs/grok-build-m2.md) into a **local** Grok Build sitting on a machine with readable `/dev/kvm`. Do not start Kubernetes, Kata, Tetragon, or snapshot/restore. The inner ring is closed; wrap it.
