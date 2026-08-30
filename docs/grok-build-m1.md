# Grok Build prompt — Backlot M1

Paste everything below the line into Grok Build. Prefer a **local** Grok Build / coding session on a Linux machine where you can `sudo apt install bubblewrap` and run the jail tests. Use the **site** sandbox only to generate code and unit tests; do not treat a green site run as “the inner ring works.”

---

You are implementing **Backlot M1** (inner ring only) in the existing repo.

## Repo and docs (read first)

- Repo: https://github.com/PixnBits/backlot
- Contract: `docs/prd.md` (especially §9 invariants and §15 packing / TTL — TTL is *not* M1)
- Inner-ring spec: `docs/agent-sandbox-meta-prompt.md` — this is the implementation spec. Follow it.

M1 is **not** Kubernetes, not Firecracker, not a fleet. It is Bubblewrap + seccomp + decoy binds + an audit log the jail cannot rewrite + tests T1–T10.

## Where this may run

Detect the environment at the start of work and print a capability matrix:

| Capability | How to probe |
|---|---|
| `bwrap` on PATH | `command -v bwrap` |
| Unprivileged user namespaces | `bwrap --unshare-user --unshare-pid echo ok` |
| `libseccomp` / Python `seccomp` | import or `pkg-config libseccomp` |
| `/dev/kvm` readable | `test -r /dev/kvm` |
| `firecracker` on PATH | `command -v firecracker` |

Rules:

- **Site / CI sandbox without bwrap:** implement all code, run unit/contract tests, mark T1–T10 as `NOT RUN` with the missing capability. Do not fake passes. Add a `make test-unit` vs `make test-int` split.
- **Linux laptop/VM with bwrap:** run T1–T10 for real. This is the only way M1 is done.
- **Do not implement or “test” Firecracker/microVMs in this pass** unless `/dev/kvm` is readable *and* the user explicitly expanded scope. Grok Build site sandboxes often show `/dev/kvm` as root-only; that is not a usable VMM. Nested virt tests on the site will lie or hang.

If you are on the site and bwrap cannot be installed, still write the full M1 tree so a local `make test-int` works after `apt install bubblewrap`.

## Scope (do this)

Create an M1 package under `inner/` (or `stage/` if you prefer the Backlot voice). Deliver:

1. `inner/policy.yaml` — demo profile: `ls`/`cat` of `/workspace` only; decoy at `/opt/grok`; network `none`; secret path `/opt/grok` never bound as the real tree.
2. `inner/bwrap-run.sh` (or a small Python/Go driver that execs bwrap) — documented flags, no magic fds, `--die-with-parent`, `--new-session`, default-deny mounts, `--unshare-all`, no `--share-net` in the demo profile.
3. Seccomp generator from policy (`libseccomp`). Default `ERRNO(EPERM)` on deny. Allow-list only what the demo tools need. Explicitly deny the I3 list in the meta-prompt (`chdir`, `fchdir`, `pivot_root`, `mount`, `ptrace`, `unshare`, `CLONE_NEWUSER`, raw sockets, `bpf`, …).
4. Decoy tree generator — watermarked fakes (`CANARY`, wrong checksum). Never bind real secrets + decoy at the same path.
5. Audit writer **outside** the jail: append-only file with `prev_hash` / `event_hash`. Jail must not be able to unlink it (do not mount the log path into bwrap).
6. Tests:
   - Unit: policy compiler rejects secret-path binds; generated argv never contains the real secret path; seccomp table excludes I3 denies.
   - Integration T1–T10 from the meta-prompt as real programs (C or Python), not shell one-liners.
   - T10 must be a binary *named* `ls` that calls `chdir` and is still denied.
7. `inner/TEST_REPORT.md` filled with actual results or `NOT RUN: <reason>`.
8. `inner/REVIEWER.md` checklist from the spec, unchecked except what you personally verified.
9. Root `README.md` gets an M1 section: how to install bwrap, `make test-unit`, `make test-int`.

## Scope (do not do)

- No Kubernetes manifests, no RuntimeClass, no session router.
- No Firecracker jailer, no guest kernel, no snapshot/TTL clocks.
- No host Tetragon DaemonSet.
- No claim of “unbreakable.”
- Do not weaken an invariant to make a test pass. Leave it failing and explain.

## Invariants you must not violate

I1 default-deny mounts (`ENOENT` for unbound paths, not `EACCES`).
I2 real `/opt/grok` invisible; decoy optional and watermarked.
I3 syscall allow-list; binary name is not policy.
I4 no ambient network in the demo profile.
I5 `--die-with-parent`.
I6 audit sink not writable from inside the jail.
I7 print policy hash, seccomp hash, decoy checksum at start.

## Definition of done

- `make test-unit` passes in any Linux environment with Python 3.
- `make test-int` passes on a machine with working `bwrap`.
- A reviewer can read the generated bwrap argv and the syscall table in one sitting.
- `TEST_REPORT.md` is honest.

Start by restating the seven invariants in your own words, then implement, then run whatever tests the environment allows.

---

End prompt.
