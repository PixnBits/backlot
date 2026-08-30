# Backlot — Product Requirements Document

**Status:** Draft v0.2  
**Date:** 2026-08-30  
**Repo:** [PixnBits/backlot](https://github.com/PixnBits/backlot)  
**Codename etymology:** A film backlot is a constructed world that looks lived-in. False fronts. Soundstages with real doors that open onto plywood. Crew on the other side of the wall, logging every take. That is the product.

---

## 1. One-liner

Backlot leases **open worlds** to LLM agents: long-lived, isolated execution environments that look like ordinary Linux, can run real tools, scale as a fleet, and treat hostility as a first-class input — with hardware isolation, userspace confinement, decoy filesystem, and kernel-level telemetry.

## 2. Why “open world” and not “sandbox”

A sandbox, in the sense people ship today, is usually:

- short-lived
- reset between turns
- a shared or thinly isolated kernel
- optimized for “don’t let the demo rm -rf the laptop”

An **open world** is:

- leased for the duration of a job or session (minutes to hours)
- stateful: the agent reads a tool’s output, decides, runs the next binary in the *same* world
- not shared with other tenants
- designed so a compromised agent still cannot leave, and every attempt is evidence

Reset is a *lifecycle policy*, not the product identity. Worlds can be snapshotted, paused, or destroyed. They are not a communal pit that gets hosed down every five minutes.

## 3. Problem

Agentic systems need to run untrusted binaries (the agent’s “tools”) with:

1. **Containment that survives a kernel-class mistake in the guest** — shared-kernel containers are the wrong outer ring for hostile or prompt-injected agents.
2. **Least privilege per tool invocation** — the long-lived agent process should not have the same filesystem or syscall surface as every binary it launches.
3. **Proof, not vibes** — compliance-heavy work needs hashes, boot measurements, denied-syscall logs, and an append-only trail the world cannot rewrite.
4. **Fleet operations** — on-demand start, sticky routing while a session is live, policy that can tighten network as the job progresses, scale-out without a unique snowflake per researcher.
5. **Adversarial visibility** — probes for secrets should hit decoys and ring a bell, not `EACCES` that confirms the treasure is real.

Existing pieces (Firecracker, Kata, Bubblewrap, Tetragon, agent-sandbox SIG) solve slices. Nothing opinionated stitches them into a leasable world with honeypots and a router.

## 4. Goals / non-goals

### Goals (PoC → v0.1)

- Lease a world in seconds (warm pool) or ~125–400 ms + image pull (cold Firecracker path).
- Hardware isolation per world (microVM / Kata-Firecracker RuntimeClass).
- Inner Bubblewrap ring for each tool exec.
- Decoy binds for configured secret paths.
- eBPF telemetry shipped off-world; world cannot truncate the log.
- Session router: sticky mapping `session → world` for the lease TTL.
- Network policy that can be mutated mid-lease (default deny → allowlisted egress → lock down).
- One demo profile: `ls`/`cat` in `/workspace`, decoy at `/opt/grok`, network none unless proxy.

### Non-goals (explicit)

- Multi-cloud control plane, billing, or a public SaaS in v0.1.
- Nested Firecracker inside Firecracker (the VMM does not expose `/dev/kvm` to guests).
- GPU passthrough (Firecracker does not; pick Cloud Hypervisor/QEMU later if needed).
- Replacing Kubernetes. We *use* it as the fleet manager.
- A general malware-analysis lab (no Cuckoo clone).
- Claiming the inner ring is unbreakable. Residual risk is the guest kernel + VMM + operator error.

## 5. Users

| Persona | Need |
|---|---|
| Platform engineer | RuntimeClass, node pool with KVM, Helm-shaped deploy |
| Agent runtime author | HTTP/gRPC: `lease`, `exec`, `files`, `net-policy`, `destroy` |
| Security / compliance | Policy artifacts, hash-chained audit, decoy hits as evidence |
| Red-team / “antagonistic white hat” | Intentional decoys, replay of probe sequences |

## 6. Architecture

### 6.1 Rings (outside → inside)

```
[control plane + session router]
        |
[Kubernetes node with KVM]          ← host eBPF DaemonSet (Tetragon/Falco)
        |
[Firecracker / Kata microVM]        ← hardware virtualization
        |
[guest kernel + world runtime]
   ├─ agent-facing API (exec/files)
   ├─ Bubblewrap tool jails          ← namespaces + seccomp
   ├─ decoy mounts
   └─ guest telemetry sidecar        ← eBPF on the *guest* kernel
        |
        v
[append-only audit sink outside the world]
```

Two eBPF vantage points, on purpose:

- **Host DaemonSet** — watches the VMM, jailer, unexpected host mounts, world-escape *attempts at the KVM boundary*. Privileged, node-scoped.
- **Guest sidecar** — watches Bubblewrap children, decoy `open`/`stat`, denied syscalls. Lives in the same pod/microVM so it shares the guest kernel with the workload. Must not be writable or killable by the agent’s tool processes (separate cgroup, no bind of the sidecar’s sockets into the bwrap mount ns).

A sidecar that only tails container logs is not this. The sidecar is a sensor, not a logger of stdout.

### 6.2 World as a pod

Each leased world is a Kubernetes pod with `runtimeClassName: kata-fc` (or equivalent Firecracker-containerd class):

- **world-runtime** — small API: health, exec-via-bwrap, file ingress/egress through an allow-list, lease heartbeat.
- **tele-sidecar** — loads guest eBPF (or talks to a guest Tetragon-lite), ships events to the cluster sink.
- Optional **egress-proxy** — the only network the inner ring may reach; user-space allow-list + audit.

Pause + two containers share the guest kernel. That is what makes guest eBPF useful. Do not put the telemetry agent *inside* the Bubblewrap jail.

### 6.3 Session router

- Control plane maps `world_id` / `session_id` → pod IP (or internal Service).
- Agent traffic is sticky for the lease. No “new container per tool call” unless the caller asks for a fresh world.
- Warm pool: N pre-booted worlds in `Idle`, claimed in O(milliseconds) + policy apply.
- Heartbeat / any usage slides `ttl_pause`, `ttl_store`, `ttl_prune` (§15.6).
- Mid-lease **network phases** (examples):
  1. `dark` — no egress
  2. `proxy` — only egress-proxy
  3. `allowlist` — named hosts for the current task
  4. `frozen` — no new connections; existing may drain

Implemented as NetworkPolicy / Cilium policy updates plus seccomp/bwrap `--unshare-net` vs `--share-net` for the *next* tool spawn. Already-running processes do not magically lose sockets; document that. Phase changes apply to subsequent execs and, where possible, proxy rules immediately.

### 6.4 Inner ring (Bubblewrap)

Policy file is the source of truth (see `docs/agent-sandbox-meta-prompt.md`):

- Default-deny mount ns.
- Secrets unbound (`ENOENT`, not `EACCES`) unless a decoy is configured at that path.
- Seccomp allow-list generated from policy; deny `chdir`/`pivot_root`/`ptrace`/`unshare`/`CLONE_NEWUSER`/raw sockets unless documented.
- `--die-with-parent`.
- Tool invocations inherit one policy; the long-lived runtime stays outside bwrap (still inside the microVM).

### 6.5 Honeypots

- `decoy_paths` in policy: real-looking tree, watermarked / canary-token contents.
- Every `open`/`stat` under a decoy prefix is an audit event with high severity.
- Never bind real secret + decoy at the same path.
- Decoys are a *feature* of an open world: the set dressing is part of the lot, not an afterthought.

### 6.6 Audit

Each event: `utc`, `world_id`, `session_id`, `policy_hash`, `kind`, `payload`, `prev_hash`, `event_hash`.

Sink lives **outside** the world’s writable mounts (cluster log pipeline, object store with object-lock, or a dedicated audit namespace). The world may *emit*; it may not *edit history*.

Boot artifact: guest kernel hash, rootfs hash, policy hash, seccomp blob hash, eBPF program hash. Printed at lease start. Tests assert they match committed versions.

## 7. API sketch (world-runtime)

```
POST   /v1/worlds                  → lease {profile, ttl_pause, ttl_store, ttl_prune, network_phase}
GET    /v1/worlds/{id}
POST   /v1/worlds/{id}/heartbeat
POST   /v1/worlds/{id}/exec        → {argv, stdin?, timeout} via bwrap
POST   /v1/worlds/{id}/files       → allow-listed ingress
GET    /v1/worlds/{id}/files
PUT    /v1/worlds/{id}/network     → phase change
DELETE /v1/worlds/{id}             → destroy (default) or snapshot
GET    /v1/worlds/{id}/events      → audit cursor (control plane; not from inside bwrap)
```

Router exposes the same surface with `X-Session-Id` / `X-World-Id` so a long agent loop does not hold raw pod IPs.

## 8. Fleet & scale

- Node pool labeled for KVM / Kata (`/dev/kvm` present; nested virt if the pool is VMs).
- RuntimeClass `kata-fc` for worlds; ordinary class for control plane and sinks.
- HPA or a custom controller on `Idle` warm-pool size.
- Density: Firecracker-class overhead (few MiB VMM + guest kernel + runtime), not QEMU-shaped.
- Failure domain: one world = one microVM = one blast radius. Do not pack two tenants in one guest.

## 9. Security invariants

Carried from the inner-ring spec; they apply to the product:

1. Unlisted paths do not exist inside a tool jail.
2. Real secret paths are invisible to tool jails.
3. Syscall policy is an allow-list; binary *name* is not policy.
4. No ambient network from tool jails.
5. Tool trees die with the exec parent.
6. Audit is not in the jail’s write path.
7. Policy is a versioned artifact.

Plus fleet-level:

8. Two tenants never share a guest kernel.
9. Sidecar credentials never appear in the world-runtime env that tools inherit.
10. Phase `frozen`/`dark` is fail-closed if the policy update errors.

## 10. PoC milestones

### M0 — Paper (this PRD)

Repo, naming, rings, API sketch.

### M1 — Inner ring on a laptop VM

- `policy.yaml` + `bwrap-run.sh` + generated seccomp
- Decoy tree + tests T1–T10 from the meta-prompt
- No Kubernetes yet

### M2 — Single microVM world

- Firecracker *or* Kata-fc on a KVM host
- world-runtime + bwrap exec
- Guest sidecar shipping denied syscalls / decoy opens to stdout → file on host

### M3 — Fleet slice

- RuntimeClass + two-node-capable manifest
- Session router + warm pool of 1
- Network phases `dark` and `proxy`
- Host Tetragon DaemonSet (or documented Falco) watching the VMM process

### M4 — Compliance demo

- Hash-chained audit in an external sink
- Replay a planted probe against `/opt/grok` decoy
- One-pager an auditor can read: what was permitted, what was attempted, what was denied

## 11. Risks

| Risk | Mitigation |
|---|---|
| Nested virt I/O tax on VM node pools | Prefer `.metal` / BM for latency-sensitive; document the Oracle-style ~3% CPU vs ~2× random I/O hit |
| Firecracker feature gaps (no virtio-fs, no hotplug) | Block volumes + vsock; phase network via proxy not NIC hotplug |
| Guest eBPF needs privileges in the sidecar | Tight RBAC; sidecar not reachable from bwrap; drop caps after load |
| Warm pool cost | Small idle memory; snapshot/restore later |
| Same-model codegen of policy + tests | Independent reviewer checklist; property tests |
| “Sidecar in the pod” confused with host telemetry | Docs always draw two sensors |

## 12. Name & voice

- Product: **Backlot**
- Instance: a **world** (not a sandbox)
- Inner jail: a **stage** or **pen** if we need a word; otherwise just “tool jail”
- Decoys: **flats** (theatrical scenery) or **false fronts**
- Telemetry: **continuity** (the script supervisor’s book)

If “Backlot” feels too cute in a compliance deck, the sober subtitle is **Open World Runtime**. Repo stays `backlot`.

## 13. Related work (do not reinvent blindly)

- Firecracker, firecracker-containerd, Kata `kata-fc`
- Kubernetes SIG Agent Sandbox (Firecracker example + router header)
- Bubblewrap / Anthropic sandbox-runtime
- Cilium Tetragon, Falco
- The inner-ring meta-prompt in `docs/agent-sandbox-meta-prompt.md`

## 14. Open questions

1. ~~First target runtime~~ → **Kubernetes + Kata-fc / firecracker-containerd** (§15.1).
2. ~~Public vs private~~ → **public** (§15.3). Flip visibility in GitHub settings.
3. Language for world-runtime: Go vs Rust — still open (§15.2).
4. Snapshot/restore: pulled forward into lease clocks (§15.6). M3 should pause; M4 should store/restore.
5. Is `/opt/grok` the canonical decoy path in demos, or do we generalize immediately?
6. Continuity desk storage: object-lock bucket vs append-only log service vs both?

---

*This document is the contract. Implementation that violates §9 is a bug, not a stretch goal.*

# 15. Decisions log (v0.2 — 2026-08-30)

## 15.1 Target runtime: Kubernetes first

Worlds are scheduled as pods with a Firecracker-backed RuntimeClass (`kata-fc` or firecracker-containerd). Control plane, router, continuity sink, and warm-pool controller are ordinary cluster workloads. Nodes that run worlds must expose `/dev/kvm` (bare metal preferred; nested virt is allowed with the I/O tax documented in §11).

Raw Firecracker + jailer remains the *engine*, not the user-facing orchestrator.

## 15.2 Language

Unset. Go has containerd/CRI/Kubernetes gravity. Rust has Firecracker gravity. Decide at M2 when we write world-runtime, not before. Dual implementation is a non-goal.

## 15.3 Visibility

Repo should be **public**. Created private by default; flip in GitHub → Settings → Danger zone → Change visibility. The connector used to create the repo cannot change visibility.

## 15.4 Continuity desk (shared audit sink)

Correct: the append-only log is a **cluster service**, not a disk inside each world.

- Worlds **emit**. The desk **commits**.
- Per-world hash chain (world cannot rewrite its own past even in memory we later snapshot).
- Desk adds a global sequence / Merkle cursor so an auditor can prove ingest order across the lot.
- Auth: the world’s service account / SPIFFE identity. No world credential may `DELETE` or `PUT` over an existing event.
- A world must not read another world’s events by default.
- The sink is not mounted into any Bubblewrap jail. The sidecar may hold a write-only socket or mTLS client, not a bind of the desk’s data volume.

“Common service across worlds” yes. “Lives in the world container’s filesystem” no.

## 15.5 Packing: what a container is allowed to run

The phrase “each container able to run a number of microVMs” is the part to correct.

| Unit | What it is | How many microVMs |
|---|---|---|
| Kubernetes **node** (KVM) | Host kernel + Firecracker processes | Many worlds. This is density. |
| **Lot worker** / node agent | Privileged host process talking to KVM | Manages many worlds on that node |
| Kubernetes **pod** with `kata-fc` | The pod *is* the microVM | **One** world. Sidecar + runtime share that guest kernel. |
| Bubblewrap jail | Process tree inside the guest | Many per world, same tenant |
| Tenant container spawning VMs | Would need `/dev/kvm` in the guest | **No.** Firecracker does not expose nested virt to guests. |

Two tenants never share a guest kernel (§9.8). Multiple tool jails in one world is the intended fan-out. Multiple worlds on one *node* is the intended scale-out. Multiple worlds inside one tenant pod is how you accidentally rebuild a shared-kernel cluster and call it isolation.

If “lot container” meant “a DaemonSet worker on the node that shepherds many Firecracker processes,” that worker is host infrastructure, not a world. Name it the **lot boss**. Keep it off the tenant network path.

## 15.6 Lease clocks (sliding TTL)

Create-world requires three durations, all **sliding on activity** (exec, heartbeat, file ingest, network phase change):

| Clock | When it fires | Effect | Node RAM |
|---|---|---|---|
| `ttl_pause` | Idle this long | Firecracker **Pause** (vCPUs frozen). World still scheduled. | Still held |
| `ttl_store` | Idle this long after pause, or total idle | Snapshot (mem + vmstate + disk) to object storage; VMM process exits; pod may scale to zero | Released |
| `ttl_prune` | Idle this long in storage | Delete snapshot + retire `world_id`. Continuity events are **not** pruned with the world unless a separate retention policy says so. | n/a |

Activity resets all three clocks. That is the “reset each usage” rule.

Wake paths:

- From **paused**: `Resume`. Milliseconds. Sticky router still has the same pod.
- From **stored**: restore snapshot into a new VMM (new pod is fine). Router remaps `world_id`. Seconds, not a full cold boot, if the snapshot is warm in cache.
- From **pruned**: gone. Client must lease a new world.

Hard rules:

- Pause without store still costs the node. Warm pool and paused worlds compete for RAM; cap them.
- Snapshot size ≈ guest RAM + dirty disk. Budget storage or `ttl_store` will surprise you.
- Snapshot restore is not a security boundary. A paused/stored world is still that tenant’s world. Do not resume a snapshot onto a node and then attach a different tenant.
- Continuity desk outlives `ttl_prune` by default (evidence is not ephemeral just because compute is).

Default sketch for the demo profile (not law): `ttl_pause=15m`, `ttl_store=2h`, `ttl_prune=7d`.
