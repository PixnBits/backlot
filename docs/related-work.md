# Related work

Backlot is not the first “run an agent in a box.” The compute substrate is crowded. What is thin on the ground is an **open-world lease** (hours, sticky, snapshot clocks) with a **syscall-level inner ring**, **decoy filesystem**, and a **hash-chained audit desk the guest cannot edit**.

This note is so the next implementation pass does not reinvent snapshot APIs or pretend path-name filters are a kernel.

## How to read this

- **Steal** = use the idea, cite the source, do not reimplement the product.
- **Avoid** = known footgun documented by that project.
- **Not us** = adjacent category; do not copy their threat model by accident.

## Agent harnesses (policy + UX, weak outer wall)

### Kiro Crew

Persistent workspace: memory, skills, schedules, ACP harness as a *process* not a library. Security is seven layers, most of them in-process.

| Layer | What they do | Lesson |
|---|---|---|
| 0 OS sandbox | Linux user+mount namespaces (Bubblewrap-class) or macOS Seatbelt. **No microVM.** | Inner ring only. Fail closed if it cannot start. |
| 1 Path gate | Resolved paths, `O_NOFOLLOW`, “keystone” files the agent cannot rewrite | Policy artifacts live outside the jail. |
| 2 Command gate | Denied-bash patterns | Not a shell parser. Loses to `eval` and assembled commands. Do not treat this as enforcement. |
| 3 Validation | MCP schemas, length caps | Keep. Cheap. |
| 4 Redaction | Credentials and exfil-shaped URLs in output | Slow drip still wins; pair with egress proxy. |
| 5 Audit | HMAC-chained SEL log | Continuity desk. Audit-or-deny: if the log cannot commit, the tool does not run. |

Documented holes worth not copying: no default egress lock, no in-guest proof the sandbox engaged, writable `~/.bashrc`, advisory patterns mixed with hard gates.

Sources: [Crew product](https://kiro.dev/crew/), [security deep-dive](https://github.com/kirodotdev/KiroCrew/blob/main/docs/architecture/security-deep-dive.md), [harness as process](https://kiro.dev/blog/one-agent/).

Kiro *Web* task sandboxes still spin up, clone, work, tear down. That is a sandbox. We are a lease.

### Anthropic sandbox-runtime / Claude Code Linux sandbox

Bubblewrap (Linux) or Seatbelt (macOS) around tool exec. Closest inner-ring cousin. No Firecracker, no honeypot tree, no cluster desk.

Steal: per-command wrap, explicit filesystem allow-list, proxy for egress.

## Firecracker / microVM agent clouds (outer wall, thin inner policy)

These already sell “lease me a kernel.” Do not rebuild their VMM orchestration unless we have a reason.

| Project | Isolation | Persistence | Notes |
|---|---|---|
| [E2B](https://e2b.dev) | Firecracker | Pause/resume, snapshot; sessions measured in hours on paid tiers | Mature agent SDK. Hardware wall, usually a root-ish guest. Self-host/BYOC on enterprise. |
| [Vercel Sandbox](https://vercel.com) | Firecracker | Persistent-ish, snapshot | Tight if the agent already lives on Vercel. |
| [Fly Machines / Sprites](https://fly.io) | Firecracker | Checkpoint, idle scale-to-zero | Persistent-first; GPU story exists on Machines. |
| [AWS AgentCore](https://aws.amazon.com) | microVM | Long sessions on AWS | Default if the lot is already an AWS account. |
| [PandaStack](https://www.pandastack.ai) | Firecracker, Apache-2.0 | Snapshot on create, CoW fork | Self-host on `/dev/kvm`. Closest “own the substrate” option. |
| [Novita Agent Sandbox](https://blogs.novita.ai/best-ai-agent-sandboxes-2026/) | Firecracker | Up to 24h, BYOC | Hosted + VPC. |

Steal: warm pool and snapshot/restore are how “90–200 ms” happens, not cold boots. That is our `ttl_pause` / `ttl_store`. Pause still holds RAM; store still costs guest-RAM-sized objects.

Avoid: calling the guest a sandbox because it has a kernel. Most of these give the agent a machine, not a seccomp allow-list or decoys.

## Devboxes and other isolation bets

| Project | Bet | Use if | Not if |
|---|---|---|---|
| [Daytona](https://daytona.io) | Persistent devbox; Docker default, Kata/Sysbox optional; fork/snapshot | Long coding sessions, self-host | You need a KVM wall by default |
| [Modal](https://modal.com) | gVisor + GPU | In-box ML / GPUs | Untrusted multi-tenant + kernel-class bugs |
| Cloudflare Sandbox / Workers | V8 isolate or container | Tiny edge functions | `pip install` and a real Linux userland |
| Docker / Podman / gVisor | Shared or user-space kernel | Trusted internal agents | Hostile prompt-injected tools on a shared node |

## Kubernetes-native

- [Kata Containers](https://katacontainers.io) + `kata-fc` — pod *is* the microVM. Our M2/M3 path.
- [firecracker-containerd](https://github.com/firecracker-microvm/firecracker-containerd) — containerd runtime that boots Firecracker; runc inside the guest.
- [Kubernetes SIG Agent Sandbox](https://agent-sandbox.sigs.k8s.io/) — SandboxClaim / warm pool / router header (`X-Sandbox-ID`). Steal the control-plane nouns; keep our inner ring.

## Inner-ring and telemetry parts

- [Bubblewrap](https://github.com/containers/bubblewrap) — mount/user/pid/net namespaces, no SUID. Flatpak’s engine. M1.
- [libseccomp](https://github.com/seccomp/libseccomp) — generate allow-lists. Binary *name* is not policy.
- [Cilium Tetragon](https://github.com/cilium/tetragon) — host (and optionally guest) eBPF observe/enforce. DaemonSet on the node; do not confuse with the sidecar.
- [Falco](https://falco.org) — broader rules library, userspace eval. Fine for host VMM watch.
- Landlock — process-self filesystem LSM. Complements bwrap; does not replace a microVM.

## Honeypots

Cuckoo-class malware labs popularized decoy filesystems and record/replay. There is no maintained “Cuckoo on Bubblewrap.” Agent clouds almost never ship false fronts. That gap is intentional product space for Backlot **flats**, not a reason to build a malware zoo.

## What we will not copy

1. Per-task tear-down as the identity of the product (Kiro Web sandbox, many “sandbox SDKs”).
2. Command-string deny lists as the security boundary (Crew layer 2).
3. Many tenants on one guest kernel (ordinary Kubernetes nodes without Kata).
4. Nested Firecracker inside a tenant pod (the VMM does not hand guests `/dev/kvm`).
5. A new VMM. Firecracker and Kata exist.

## Suggested reading order for implementers

1. `docs/prd.md` §9 and §15 — our contract.
2. `docs/agent-sandbox-meta-prompt.md` — inner-ring tests.
3. Crew [security-deep-dive](https://github.com/kirodotdev/KiroCrew/blob/main/docs/architecture/security-deep-dive.md) — layers and holes.
4. Firecracker [snapshot API](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) — pause / create / load.
5. SIG Agent Sandbox [Firecracker example](https://agent-sandbox.sigs.k8s.io/docs/use-cases/examples/firecracker-sandbox/).
6. E2B or PandaStack docs — only when M2 starts, to see a complete outer-ring SDK we should *not* clone feature-for-feature.

Last reviewed: 2026-08-30.
