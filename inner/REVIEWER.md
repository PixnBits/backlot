# Reviewer checklist — Backlot M1 inner ring

The same model must not be the only author of both the filter and the
only tests. Tick these after reading the generated argv and syscall table.

Invariants (restated):

1. **I1** Unlisted paths do not exist in the jail (`ENOENT`, not `EACCES`).
   Root is a fresh tmpfs; only explicit `--ro-bind` / `--bind` / `--symlink`
   appear.
2. **I2** Real secret paths (`/opt/grok`, `/root/.ssh`, `/home`) are never
   bind *sources*. A watermarked decoy may be bound at `/opt/grok`.
3. **I3** Syscalls are an allow-list. A binary named `ls` that calls `chdir`
   is still denied. `clone` with any `CLONE_NEW*` bit is denied; `clone3`
   is denied outright.
4. **I4** Demo profile has no ambient network: `--unshare-all` and no
   `--share-net`. `socket`/`connect` are not in the allow-list.
5. **I5** `--die-with-parent` plus a private pid ns.
6. **I6** The audit jsonl is not mounted into the jail. The jail cannot
   unlink or truncate it.
7. **I7** `policy_hash`, `seccomp_hash`, and `decoy_checksum` print on
   every start.

## Checklist

- [x] Secret paths absent from every `--bind` / `--ro-bind` *source*
- [x] Decoy contents cannot authenticate to any real system
- [x] Seccomp table reviewed syscall-by-syscall (`inner/artifacts/syscall-table.txt`)
- [x] Audit sink not mounted into the sandbox
- [x] Tests T1–T10 actually executed, not just generated
- [ ] Independent reviewer (different model or human) signed

## Residual risk (do not claim unbreakable)

- Shared guest kernel with the rest of the microVM (outer ring’s job).
- Binding `/usr` makes other binaries under `/usr` reachable; they still
  get this seccomp profile (name is not policy).
- `ioctl`, `mmap`, `prctl` are allowed without argument filters.
- `clone3` is denied entirely — glibc `pthread_create` will fail. Fine for
  `ls`/`cat`; not a general-purpose POSIX jail.
- Denied syscalls are not individually logged without eBPF or
  `SECCOMP_RET_USER_NOTIF`. The wrapper logs intent; inotify logs decoy
  opens.
- Side channels: timing, stdout encoding, argument-incomplete seccomp.
- Kernel bugs, VMM bugs, operator error.

## How to review in one sitting

```
make artifacts          # writes inner/artifacts/plan.txt + syscall-table.txt
less inner/artifacts/plan.txt
less inner/artifacts/syscall-table.txt
```
