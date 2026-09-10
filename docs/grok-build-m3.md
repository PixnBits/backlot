# Grok Build prompt — Backlot M2.1 gate, then M3 mini-fleet

Paste everything below the first `---` into a **local** Grok Build / coding agent on the KVM desktop.

Worktrees:

- `/home/pixnbits/projects/backlot`
- `/home/pixnbits/projects/backlot/main`

Repo: https://github.com/PixnBits/backlot

This is a **gated** autonomous run. Later phases are forbidden until earlier phases print their required line. A green site sandbox is not a world. Do not fake a VMM, a cluster, or telemetry.

---

You are continuing **Backlot** toward a local three-world fleet a human can start with Docker Compose (control plane) plus host Firecracker (worlds). You will not jump there in one leap.

## Read first

- `docs/prd.md` — contract. Especially §6 packing, §9 invariants, §15.1–§15.7.
- `docs/related-work.md` — steal patterns, do not clone E2B/Kiro.
- `docs/grok-build-m1.md`, `docs/grok-build-m2.md`
- `inner/` — closed. Do not rewrite T1–T10 C programs or widen seccomp.
- `guest/`, `runtime/` — M2 one-world stack.
- Open PR if present: https://github.com/PixnBits/backlot/pull/4 (`feature/m2.1-jailer`)

## Tags (do not retarget)

| Tag | SHA | Meaning |
|---|---|---|
| `m1` | `adc1be533efba65019a66718d96fae1b99983406` | inner ring |
| `m2` | `93abea1147bf32e22cf4cae672c8d05d484ad335` | one world (Firecracker-as-kvm-user sitting) |

Never point `m1` or `m2` at a later docs or fleet commit. New work is `m2.1` then `m3`.

## Closed forks (do not reopen)

| Fork | Decision |
|---|---|
| Language | Go |
| First-world engine | Raw Firecracker **v1.15.1** + jailer at `/usr/local/firecracker/v1.15.1/` |
| PATH `main` → `v1.16.0-dev` | Unused |
| Product fleet manager | Kubernetes later. M3 local path is **Compose for control plane + host Firecracker for worlds** |
| Two tenants in one guest | Never |
| Guest NIC in demo | Down / absent. Vsock only until a documented `proxy` phase exists |
| Host jsonl into guest | Never. Guest emits on vsock; shepherd or desk `O_APPEND`s |

## The packing rule you will be tempted to break

Docker Compose does **not** run Firecracker worlds as ordinary containers.

| Unit | Allowed |
|---|---|
| Compose service: `router`, `desk`, optional UI | Yes. Ordinary containers. No `/dev/kvm`. |
| Compose service: `lot-boss` | Yes only if privileged / host-network / bind `/dev/kvm` **and** it still starts **one Firecracker process per world on the host**. The container is the shepherd, not the guest. |
| One Compose service that is “three microVMs inside” | **No.** That rebuilds a shared-kernel cluster and calls it isolation. |
| Kata-fc RuntimeClass | Not this prompt. After M3 local fleet is honest. |

If `/dev/kvm` is missing: implement control-plane unit tests, mark fleet tests `NOT RUN`, stop. Do not invent qemu-as-firecracker.

## Capability matrix (print first, every sitting)

Probe and print:

- euid / `sudo -n` / `CAP_SYS_ADMIN`
- `/dev/kvm` readable+writable
- `bwrap`, unprivileged user namespaces
- `firecracker --version` and `jailer --version` of the **v1.15.1 pin**, not the dev symlink
- Go, gcc, Python, Docker, `docker compose`
- Whether PR #4 is merged

## Phase 0 — M2.1 gate (mandatory)

**Stop condition to leave this phase:** `make test-m2` prints `engine=jailer` on this machine, T1–T10 still pass in-guest, tenant exec has no `jail` field.

Work from `feature/m2.1-jailer` (PR #4) if it is still open.

Required already in that branch (verify, do not regress):

- Tenant `POST /v1/worlds/{id}/exec` always runs `python3 /opt/backlot/inner/run.py`.
- `"jail": false` in JSON is ignored.
- In-guest `run_int.py` uses `POST /v1/internal/bare-exec`, registered only when kernel cmdline has `backlot.bare_exec=1`. Product shepherd does **not** set that arg.
- `m2test` fails unless `engine=jailer`.
- Jailer drops to `SUDO_UID` / `PKEXEC_UID`, not uid 0 inside the jail (uid 0 cannot open KVM on this host).

How to run the gate:

```bash
# same shell
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519   # only if you need git push

cd /home/pixnbits/projects/backlot
git fetch origin
git checkout feature/m2.1-jailer
git pull

# jailer needs privileges
sudo -E make test-unit test-int test-go
sudo -E make test-m2
```

`sudo -E` so `SUDO_UID` is set and the pin paths remain visible.

If `engine=firecracker`: **FAIL the sitting.** Do not implement M3.

If PASS:

1. Update `runtime/TEST_REPORT.md` with euid, jailer version, `engine=jailer`, T1–T10 PASS.
2. Merge PR #4 (human or `gh pr merge` if authorized). Do not move tag `m2`.
3. `git tag m2.1 <merge-sha> && git push origin m2.1`

Only then start Phase 1.

## Phase 1 — control plane on Compose (no extra worlds yet)

New packages, all Go unless noted:

```
lot/                 # lot-boss: host process that shepherds N Firecracker worlds
desk/                # continuity desk: append-only event ingest
router/              # session → world_id sticky map + HTTP facade
deploy/compose/      # docker-compose for router + desk only
```

### Continuity desk

- HTTP or gRPC ingest: worlds **emit**, desk **commits**.
- Per-world hash chain plus a global sequence.
- `O_APPEND` files or sqlite with no UPDATE/DELETE of events. No world credential may overwrite.
- A world cannot read another world’s events by default.
- `GET /v1/events?world_id=&cursor=` for an operator, never from inside bwrap.

### Router

Implement the PRD facade the agent will call:

```
POST   /v1/worlds                  lease {profile, ttl_pause, ttl_store, ttl_prune}
GET    /v1/worlds/{id}
POST   /v1/worlds/{id}/heartbeat
POST   /v1/worlds/{id}/exec
PUT    /v1/worlds/{id}/network     dark | proxy  (proxy may 501 if proxy box not built)
DELETE /v1/worlds/{id}             destroy (no snapshot in M3)
GET    /v1/worlds/{id}/events      proxy to desk
```

Sticky: `X-Session-Id` maps to one `world_id` until destroy or prune.

Lease clocks (sliding on exec/heartbeat):

- Demo defaults: `ttl_pause=15m`, `ttl_store=2h`, `ttl_prune=7d`
- M3 implements **pause** (Firecracker Pause / SIGSTOP-equivalent on vCPU) and **destroy**.
- **Store/restore is M4.** Return 501 for snapshot. Do not fake disk snapshots.

### Lot-boss

- Speaks to existing `runtime/world` Start/Stop/Exec.
- Cap: default max 3 worlds on this node.
- One world = one jailer+Firecracker = one guest kernel.
- Warm pool size 0 or 1. Do not preboot 3 unless RAM is measured and printed.

### Compose file (`deploy/compose/docker-compose.yml`)

Services:

1. `desk` — continuity desk, volume for event store
2. `router` — public `:8080`, talks to desk + lot-boss
3. Optional `lot-boss` profile `kvm` — only with `/dev/kvm` and the v1.15.1 binaries bind-mounted

`docker compose up desk router` must work **without** KVM (they accept lease requests and return `503 world engine unavailable` rather than lying).

Tests:

- `make test-unit test-int test-go` still green
- Desk hash-chain tamper test
- Router session stickiness unit test
- `make test-compose-cp` — compose control plane comes up, `/health` 200, lease without KVM is explicit failure not a fake world

## Phase 2 — three real worlds on the KVM host

Only if Phase 0 printed `engine=jailer` and Phase 1 compose control plane is up.

`make test-m3` (exit 2 without KVM):

| Test | Must hold |
|---|---|
| M3-lease-3 | Router leases three worlds; three `firecracker`/`jailer` processes; three guest CIDs or vsock paths |
| M3-exec | Each world `ls /workspace` through `inner/run.py` |
| M3-isolation | World A cannot `exec` on world B’s id; desk does not show A events when queried as B |
| M3-decoy | Decoy open on one world appears on the **desk**, not only a per-world tempfile |
| M3-scale-down | `DELETE` one world; that VMM is gone; the other two still exec |
| M3-demand | After delete, a new `POST /v1/worlds` starts a replacement; total live ≤ cap |
| M3-no-kvm-guest | No guest has usable `/dev/kvm` |
| M3-orphan | After compose/lot-boss stop: no leftover firecracker for those jail roots |
| M3-tenant-jail | `"jail": false` still cannot skip bwrap on tenant exec |

Write `lot/TEST_REPORT.md`. Honest `NOT RUN` if no sudo/jailer.

Demo script for the human:

```bash
docker compose -f deploy/compose/docker-compose.yml up -d desk router
sudo -E ./bin/lot-boss --max-worlds 3   # or compose profile kvm
curl -sS -X POST localhost:8080/v1/worlds -d '{"profile":"demo"}'
# three times, then exec, then GET events, then DELETE one
```

Document exact curls in `deploy/compose/README.md`.

## Phase 3 — only if Phase 2 is green and time remains

Optional, do not start if Phase 2 leaked:

- Network phase `dark` (already vsock-only) and a **userspace proxy** container as the only future egress. Do not give guests a nic.
- Pause clock: idle world Firecracker Pause; heartbeat Resume.
- A one-page operator README: three worlds, how to read desk events, what a decoy hit looks like.

Still forbidden in Phase 3:

- Kubernetes / k3s / Kata / RuntimeClass
- Tetragon DaemonSet (log a follow-up; host eBPF is M3-cluster in the PRD, not this local compose)
- Snapshot/restore
- Widening M1 seccomp
- GPU, virtio-fs, nested Firecracker
- Packing two tenants in one guest
- Claiming unbreakable

## Invariants (fail the PR if any break)

I1–I7 inside every tool jail.

O1 no `/dev/kvm` in guest.
O2 host/desk log is not a guest disk.
O3 no ambient guest network.
O4 no orphan VMM after stop.
O5 decoy/start/exit visible off-world (now on the desk).
O6 tenant exec cannot disable the inner ring.
O7 two worlds never share a guest kernel.
O8 desk has no DELETE of committed events.

## Honesty

- Site sandbox: generate code + unit tests only. Mark KVM tests `NOT RUN`.
- Missing sudo: you may finish Phase 1. You may **not** claim Phase 2.
- Do not merge M3 over a failed M2.1 gate.
- Residual risk stays: guest kernel + VMM + operator error + `inner/REVIEWER.md`.

## Definition of done for this autonomous run

Minimum shippable (if KVM+sudo available):

1. `m2.1` tagged after `engine=jailer`
2. Compose `desk` + `router` up
3. Three jailed Firecracker worlds via lot-boss
4. Agent-shaped HTTP: lease, exec, events, delete
5. Honest TEST_REPORT files

If sudo is missing: ship Phase 1 + the Phase 2 code, with `make test-m3` exiting 2, and a short note that the human must run the jailer sitting.

Start by printing the capability matrix and which phase you are allowed to enter. Then work. Do not write a second prompt instead of the gate.

---

End prompt.
