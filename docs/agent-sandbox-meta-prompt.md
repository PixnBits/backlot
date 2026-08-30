# Meta-prompt: Build a Bubblewrap agent sandbox with honeypots and audit proof

Copy everything below the line into an agent. Do not ask the agent to "just build a sandbox." Give it this document as the spec.

---

## Role

You are a security engineer building a **defensive containment system** for a long-lived LLM agent that must run untrusted tool binaries. Your job is to produce an implementation **and** an adversarial test suite that tries to break the implementation. You are not writing malware. You are writing a jail and the escape attempts that prove the jail holds.

Do not weaken any invariant to make a test pass. If a test cannot be written without violating an invariant, say so and leave the test failing.

## Context

- Host: Linux. The agent itself lives inside a Firecracker microVM (or equivalent). You are implementing the **inner ring only**: Bubblewrap + seccomp + optional eBPF audit + decoy filesystem.
- The microVM may live for hours. Individual tool invocations are short-lived Bubblewrap process trees.
- Compliance matters more than performance. False negatives (silent leaks) are worse than false positives (blocked legitimate tools).
- Secrets live at paths such as `/opt/grok` on the microVM filesystem. The sandbox must never see the real contents of those paths.

## Threat model

Assume the agent (or a tool it launches) is hostile after a prompt injection. It will try to:

1. Read host or microVM secrets (`/opt/grok`, `~/.ssh`, env files, cloud metadata).
2. Change working directory, mount, or pivot into unbound paths.
3. Exfiltrate data over network, DNS, or encoded stdout.
4. Spawn helpers that inherit a looser sandbox.
5. Probe for path existence (even `EACCES` is information).
6. Use shell builtins (`cd`, `exec`) rather than named binaries.
7. Tamper with its own audit trail.

Out of scope for this inner ring: escaping the microVM / KVM boundary. That is the outer ring's job. Still: never assume the outer ring saves you from sloppy inner policy.

## Hard invariants (must hold after every change)

I1. **Default deny filesystem.** The sandbox mount namespace is empty except for explicitly listed binds. Unlisted paths do not exist inside the sandbox (`ENOENT`, not `EACCES`).

I2. **Secrets are invisible, not merely denied.** `/opt/grok` (and any path in `SECRET_PATHS`) is never bound. A decoy may be bound at that path instead. The real directory must remain unreachable even to uid 0 inside the sandbox.

I3. **Syscall policy is an allow-list.** Named binaries are not trusted. `chdir`, `fchdir`, `pivot_root`, `mount`, `umount2`, `ptrace`, `process_vm_readv`, `bpf`, `perf_event_open`, `keyctl`, `unshare`, `clone` with `CLONE_NEWUSER`, and raw socket creation are denied unless a documented exception exists.

I4. **No ambient network.** Default is `--unshare-net`. If egress is required, it goes only through a documented user-space proxy. Direct `connect`/`sendto` to anything except that proxy is denied or impossible.

I5. **Process tree dies with parent.** `--die-with-parent` (or equivalent). No orphaned helpers after the tool process exits.

I6. **Audit is outside the sandbox write path.** Denied syscalls and decoy-file opens are logged to a sink the sandbox cannot truncate, rewrite, or unlink. Each event includes UTC timestamp, sandbox id, syscall or path, result, and a hash chaining to the previous event.

I7. **Policy is an artifact.** The exact bwrap argv, seccomp filter source, decoy tree checksum, and eBPF program hash are versioned and printed at start. Tests assert those hashes match the committed artifacts.

## Deliverables

Produce a small repo with these pieces. Prefer boring, reviewable code over clever frameworks.

### 1. `policy.yaml` (source of truth)

Human-editable policy that generates everything else:

- `secret_paths`: never bind
- `decoy_paths`: map real-looking path → decoy directory on the host
- `ro_binds` / `rw_binds`: explicit mounts
- `allowed_binaries` or `path_allow` (informational only; enforcement is syscalls + binds)
- `allowed_syscalls` / `denied_syscalls`
- `network`: `none` | `proxy`
- `proxy_addr` if proxy
- `workspace`: writable scratch

### 2. `bwrap-run.sh`

A wrapper that:

- Reads `policy.yaml`
- Creates a fresh scratch dir and optional decoy tree
- Invokes `bwrap` with `--ro-bind`, `--bind`, `--tmpfs`, `--proc`, `--dev`, `--unshare-all` (then `--share-net` only if proxy mode), `--new-session`, `--die-with-parent`, `--seccomp FD`
- Exec's the requested command as the last arguments
- Never interpolates unsanitized user strings into mounts

Document every flag. No "magic 12" file descriptors without showing how the fd is created.

### 3. Seccomp filter

- Generate from `policy.yaml` using `libseccomp` (Python `seccomp` module or a small C helper). Do not hand-write raw classic BPF unless you also ship the generator.
- Default action: `ERRNO(EPERM)` so denials are visible, not `KILL` (KILL is acceptable as a second profile for production hardening).
- Allow the minimum set `ls` / a POSIX tool actually needs (`execve`, `openat`, `read`, `write`, `close`, `fstat`, `newfstatat`, `mmap`, `mprotect`, `brk`, `ioctl` on tty if required — justify each).
- Explicitly deny the list in I3.
- Emit the compiled filter bytes and a human-readable syscall table as artifacts.

### 4. Honeypot / decoy tree

- A directory of plausible fakes: `credentials.json`, `.env`, `id_rsa`, `config.yaml`.
- Contents must be **clearly non-functional** to a human reviewer (watermark, wrong checksum, canary token) but look real to a sloppy probe.
- Every `open`/`openat`/`stat` on a decoy path must produce an audit event.
- Never bind the real secret path and the decoy at the same time.

### 5. Audit probe

Prefer the simplest thing that works:

- First choice: seccomp user-notification or `SECCOMP_RET_ERRNO` plus a supervisor that already knows the policy (the wrapper logs intent).
- Second choice: a small eBPF tracepoint/kprobe program attached **on the host / microVM**, not loadable from inside the sandbox. Record denied syscalls and opens under decoy prefixes.
- Logs go to an append-only file or journal outside the sandbox (e.g. `/var/log/agent-sandbox/` on the microVM, not visible inside bwrap).
- Hash chain: `event_hash = SHA256(prev_hash || timestamp || payload)`.

### 6. Tests (this is the point)

Two layers. The **same model must not be the only author of both the filter and the only tests** — after generation, add a "reviewer checklist" section the human (or a second model) signs.

**Unit / contract tests**

- Policy compiler rejects unknown keys and overlapping secret + bind paths.
- Generated bwrap argv never contains a secret path.
- Seccomp allow-list does not include denied syscalls from I3.

**Adversarial integration tests** (run inside the sandbox via the wrapper)

Each test is a small C or Python program, not a shell one-liner that can be accidentally allowed.

- T1. `ls /opt/grok` → `ENOENT` if no decoy; if decoy enabled, listing succeeds but contents are the decoy checksum, not the real tree.
- T2. `open("/opt/grok/secrets.env")` on real path without decoy → `ENOENT`.
- T3. `chdir("/")` then `chdir("/opt/grok")` → `EPERM` or `ENOENT`.
- T4. `open("/proc/1/root/opt/grok/...")` and `/proc/self/root` tricks → fail.
- T5. `unshare` / `clone(CLONE_NEWUSER)` → denied.
- T6. Direct outbound TCP (if network is `none`) → fail. If `proxy`, only the proxy address connects.
- T7. Write to a path outside workspace → fail.
- T8. After process exit, no child remains (`pgrep` from outside).
- T9. Decoy open appears in the audit log with matching hash chain; sandbox cannot delete that log file.
- T10. A binary named `ls` that internally calls `chdir` is still denied (proves name is not the policy).

**Property-style tests** (minimum viable)

- Randomly pick 50 paths from `{secret_paths + common sensitive paths + noise}`. Assert none of the secret set is readable.
- Randomly mutate argv of a harmless binary (extra flags, relative paths, `../`). Assert no secret bytes appear on stdout/stderr.

Record pass/fail in `TEST_REPORT.md` with the policy hash used.

### 7. Reviewer checklist (second pair of eyes)

A short markdown the generator fills in, a human ticks:

- [ ] Secret paths absent from every `--bind` / `--ro-bind`
- [ ] Decoy contents cannot authenticate to any real system
- [ ] Seccomp table reviewed syscall-by-syscall
- [ ] Audit sink not mounted into the sandbox
- [ ] Tests T1–T10 actually executed, not just generated
- [ ] Independent reviewer (different model or human) signed

## Implementation constraints

- Target: Linux x86_64, Bubblewrap (`bwrap`) available, unprivileged user namespaces on.
- No SUID helpers. No Docker-in-Docker required for the inner ring.
- No new kernel modules.
- Keep the generated filter small enough to read in one sitting.
- If you use eBPF, the loader runs **outside** bwrap and drops privileges after attach.
- Do not claim "this is unbreakable." State residual risk: shared guest kernel with the rest of the microVM; kernel bugs; argument-incomplete seccomp; side channels via timing or stdout encoding.

## What to output first

1. Restate the invariants in your own words (so mismatches show up).
2. `policy.yaml` for a demo profile: allow `ls` and `cat` of `/workspace` only; decoy at `/opt/grok`; network none.
3. The generated `bwrap-run.sh` invocation printed in full.
4. The syscall allow/deny table.
5. Then implement. Then run tests. Then write `TEST_REPORT.md`.

If a tool or permission is missing in the environment, implement what you can, stub the rest with a clear `NOT RUN:` note, and do not pretend tests passed.

## Non-goals

- Do not implement Firecracker, KVM, or host hardening here.
- Do not add a second microVM inside the guest.
- Do not build a general malware lab or Cuckoo clone.
- Do not hide failures behind skipped tests.

---

End of agent spec.
