# TEST_REPORT — Backlot M1 inner ring

**Date:** 2026-08-30  
**Branch:** `feature/m1-inner-ring`  
**Honesty rule:** do not fake passes. Missing capability → `NOT RUN`.

## Capability matrix (this run)

| Capability | Probe | Result |
|---|---|---|
| `bwrap` on PATH | `command -v bwrap` | **yes** — bubblewrap 0.8.0 (Debian `bubblewrap_0.8.0-2+deb12u1_amd64.deb`; the site image has no apt package index, so `apt install bubblewrap` was not how it got here. Local: `sudo apt install bubblewrap`.) |
| Unprivileged user namespaces | `bwrap --unshare-all … -- /usr/bin/id` | **yes** |
| `libseccomp` / Python `seccomp` | `pkg-config` / `import seccomp` | **no** — generator is shipped classic BPF (`inner/seccomp.py`). Unit tests inspect the bytecode; bwrap loads it via `--seccomp FD`. |
| `/dev/kvm` readable | `test -r /dev/kvm` | **yes** — **not used**. M1 does not implement Firecracker. |
| `firecracker` on PATH | `command -v firecracker` | **no** |
| Python 3 | `python3 --version` | 3.10.21 |
| gcc | `command -v gcc` | **yes** (for T1–T10 C programs) |

## Artifact hashes (I7)

Printed by `inner/run.py --print-plan` and at every jail start:

```
policy_hash:    8c945a1c1aaf9d3cb75d7d208456f987063f24304fbd43db1e6b3536426c2671
seccomp_hash:   bc808d8c38e8404a32fc29369e38bf62a0c40613909a28068b4d334285e6ad06
decoy_checksum: 913f75242d5e41fe57cc3706cd957193f0aecee3872a02ac19bd91e387a8da39
```

These match `inner/policy.yaml` and the packed BPF from `inner/seccomp.py` as of this report. Re-run `make artifacts` if you edit policy.

## `make test-unit`

**PASS** — 33 tests, Python 3 only (no bwrap, no gcc, no libseccomp).

Coverage:

- Policy compiler rejects unknown keys, secret-path binds, I3 names in the allow-list, decoy dest not in `secret_paths`.
- Generated argv always has `--unshare-all --die-with-parent --new-session --tmpfs --seccomp`; never `--share-net`; bind *sources* are never `/opt/grok`, `/root/.ssh`, or `/home`.
- Seccomp table excludes I3; default return is `ERRNO(EPERM)`; `clone` is gated on `CLONE_NS_MASK` including `CLONE_NEWUSER`.
- Audit hash chain verifies; tampering is detected.
- Decoy files are watermarked (`CANARY` / `deadbeef`) and not a real OpenSSH key.

## `make test-int` (T1–T10)

**PASS** — executed as C programs through `inner/run.py` / bwrap, not shell one-liners.

| Test | Result | Detail |
|---|---|---|
| demo_ls | PASS | `ls /workspace` lists `hello.txt` |
| demo_cat | PASS | `cat /workspace/hello.txt` |
| T1 | PASS | `ls /opt/grok` lists decoy (`CANARY.txt`, `secrets.env`, `id_rsa`); not the real tree |
| T1_no_decoy | PASS | without decoy, `opendir("/opt/grok")` → `ENOENT` (errno 2) |
| T2 | PASS | `open("/opt/grok/secrets.env")` without decoy → `ENOENT` |
| T3 | PASS | `chdir("/")` and `chdir("/opt/grok")` → `EPERM` (errno 1) |
| T4 | PASS | `/proc/1/root` and `/proc/self/root` tricks did not leak `REAL_SECRET_MARKER`; `/root/.ssh` and `/etc/shadow` are `ENOENT` |
| T5 | PASS | `unshare(CLONE_NEWUSER)`, `clone(CLONE_NEWUSER)`, `clone3` → `EPERM` |
| T6 | PASS | `socket(AF_INET, SOCK_STREAM)` → `EPERM` (network none) |
| T7 | PASS | write inside `/workspace` ok; `/usr`, `/etc`, `/tmp`, `/root` not writable |
| T8 | PASS | forked sleeper gone after bwrap exit (no leftover `backlot-t8-orphan`) |
| T9 | PASS | decoy open in hash-chained audit log (5 events); jail `unlink`/`O_TRUNC` of the audit path failed |
| T10 | PASS | binary **named** `ls` that calls `chdir("/")` → `EPERM` (name is not policy) |
| P50_paths | PASS | 50 paths; no real secret bytes |
| P_argv_mut | PASS | mutated `cat`/`ls` argv leaked no real secret bytes |

T10 binary is compiled from `inner/tests/int/programs/t10_ls.c` to a file named `ls` and exec'd through the wrapper.

## What this does *not* prove

- Firecracker / KVM / guest kernel isolation (M2).
- eBPF denied-syscall capture. M1 logs wrapper intent + inotify on the decoy host dir.
- That binding all of `/usr` is least privilege. Residual: other binaries under `/usr` are reachable; they still get this seccomp profile.
- Unbreakability. See `inner/REVIEWER.md`.
