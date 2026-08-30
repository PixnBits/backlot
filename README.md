# Backlot

Open worlds for LLM agents.

Not a disposable sandbox. A **leased world**: Firecracker (or Kata-fc) as the outer ring, Bubblewrap as the inner ring, eBPF tripwires on host *and* guest, honeypot binds as false fronts. The agent works in one world for the length of the job. Hostility is an input.

**Repo is private.** Product name is the film lot: constructed streets, real doors that open onto plywood, continuity notes on every take.

## Status

M0 — paper. See [docs/prd.md](docs/prd.md).

## Rings

```
router → KVM node (host eBPF) → microVM → world-runtime
                                      → tool jails (bwrap + seccomp)
                                      → decoy flats
                                      → guest telemetry sidecar
                                      → audit sink (off-world)
```

## Docs

- [PRD](docs/prd.md) — contract, API sketch, milestones M0–M4
- [Inner-ring agent spec](docs/agent-sandbox-meta-prompt.md) — invariants and tests T1–T10

## Next

M1 is the inner ring on a laptop: `policy.yaml`, `bwrap-run.sh`, decoys, those tests. Kubernetes waits until that jail is honest.
