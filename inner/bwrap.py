"""Build the bwrap argv from a validated policy.

Flags (always, demo profile):
  --unshare-all       user+pid+net+ipc+uts+cgroup; I4: no --share-net
  --new-session       new session / process group
  --die-with-parent   I5
  --tmpfs /           I1 empty root; unbound paths are ENOENT not EACCES
  --proc /proc        fresh procfs (not a host /proc bind — T4)
  --dev /dev          synthetic devices (not host /dev, no /dev/kvm)
  --chdir /workspace
  --clearenv          do not import host secrets from the environment
  --setenv PATH, HOME, TMPDIR
  --seccomp FD        FD is created by the runner; placeholder in dry-run

Never interpolates unsanitized strings into a shell. Argv is a list.
"""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from pathlib import Path

from hashes import sha256_bytes
from host import covered_by_binds, resolve_ro_binds, which
from policy import Bind, Policy
from seccomp import SeccompProgram, compile_seccomp

BIND_SRC_FLAGS = frozenset(
    {"--ro-bind", "--bind", "--ro-bind-try", "--bind-try"}
)

SECCOMP_FD_PLACEHOLDER = "$SECCOMP_FD"


@dataclass
class Runtime:
    workspace_host: Path
    decoy_host: dict[str, Path]  # jail dest -> host dir
    extra_ro: list[Bind] = field(default_factory=list)
    extra_rw: list[Bind] = field(default_factory=list)
    command: list[str] = field(default_factory=list)
    enable_decoy: bool = True


@dataclass
class Plan:
    policy: Policy
    program: SeccompProgram
    argv: list[str]  # includes placeholder for seccomp fd
    decoy_checksum: str
    workspace_host: Path
    decoy_host: dict[str, Path]

    @property
    def policy_hash(self) -> str:
        return self.policy.policy_hash

    @property
    def seccomp_hash(self) -> str:
        return sha256_bytes(self.program.bpf)

    def argv_for_exec(self, seccomp_fd: int) -> list[str]:
        return [
            str(seccomp_fd) if arg == SECCOMP_FD_PLACEHOLDER else arg
            for arg in self.argv
        ]

    def review_text(self) -> str:
        lines = [
            f"policy_hash: {self.policy_hash}",
            f"seccomp_hash: {self.seccomp_hash}",
            f"decoy_checksum: {self.decoy_checksum}",
            "bwrap argv:",
        ]
        argv = self.argv
        i = 0
        while i < len(argv):
            if i == 0:
                lines.append("  " + argv[i] + " \\")
                i += 1
                continue
            # group flag + operands
            flag = argv[i]
            if flag in BIND_SRC_FLAGS or flag in {
                "--symlink",
                "--setenv",
                "--tmpfs",
                "--proc",
                "--dev",
                "--chdir",
                "--dir",
                "--chmod",
            }:
                n = 3 if flag in BIND_SRC_FLAGS or flag in {"--symlink", "--setenv", "--chmod"} else 2
                chunk = argv[i : i + n]
                lines.append("    " + " ".join(_shell(a) for a in chunk) + " \\")
                i += n
                continue
            if flag in {"--unshare-all", "--new-session", "--die-with-parent", "--clearenv"}:
                lines.append("    " + flag + " \\")
                i += 1
                continue
            if flag == "--seccomp":
                lines.append("    --seccomp $SECCOMP_FD \\")
                i += 2
                continue
            if flag == "--":
                rest = " ".join(_shell(a) for a in argv[i:])
                lines.append("    " + rest)
                break
            lines.append("    " + _shell(flag) + " \\")
            i += 1
        lines.append("")
        lines.append(self.program.table_text)
        return "\n".join(lines)


def build_plan(policy: Policy, runtime: Runtime, bwrap_bin: str = "bwrap") -> Plan:
    program = compile_seccomp(policy)
    decoy_sum = "0" * 64
    decoy_host = dict(runtime.decoy_host) if runtime.enable_decoy else {}
    if decoy_host:
        from hashes import decoy_checksum as _ck

        parts = []
        for dest in sorted(decoy_host):
            parts.append(_ck(decoy_host[dest]))
        decoy_sum = sha256_bytes("".join(parts).encode()) if len(parts) > 1 else parts[0]

    argv: list[str] = [bwrap_bin]
    argv += [
        "--unshare-all",
        "--new-session",
        "--die-with-parent",
        "--clearenv",
        "--setenv",
        "PATH",
        "/usr/bin:/bin",
        "--setenv",
        "HOME",
        policy.workspace,
        "--setenv",
        "TMPDIR",
        policy.workspace,
        "--tmpfs",
        "/",
        "--proc",
        "/proc",
        "--dev",
        "/dev",
        "--dir",
        policy.workspace,
        "--chmod",
        "0755",
        policy.workspace,
    ]
    if policy.network != "none":
        # Explicit and loud. Demo profile never takes this branch.
        argv.append("--share-net")

    ro_flags = resolve_ro_binds(policy.ro_binds)
    for tup in ro_flags:
        _reject_secret_source(policy, tup)
        argv.extend(tup)

    for bind in (*policy.rw_binds, *runtime.extra_rw):
        _reject_secret_source(policy, ("--bind", bind.host, bind.dest))
        argv.extend(("--bind", bind.host, bind.dest))

    argv.extend(("--bind", str(runtime.workspace_host), policy.workspace))

    if runtime.enable_decoy:
        for dest, host_dir in decoy_host.items():
            _reject_secret_source(policy, ("--ro-bind", str(host_dir), dest))
            if policy.is_secret(str(host_dir)):
                raise ValueError(f"decoy host dir is a secret path: {host_dir}")
            argv.extend(("--ro-bind", str(host_dir), dest))

    for bind in runtime.extra_ro:
        _reject_secret_source(policy, ("--ro-bind", bind.host, bind.dest))
        argv.extend(("--ro-bind", bind.host, bind.dest))

    # Bind the command binary if it lives outside the existing mounts.
    # Dest is /.backlot-bin/<name> so we do not create writable parent
    # dirs like /tmp on the root tmpfs (I1: /tmp must stay ENOENT).
    command = list(runtime.command)
    if command:
        resolved = which(command[0]) or command[0]
        command[0] = resolved
        if os.path.isfile(resolved) and not covered_by_binds(resolved, _as_flag_tuples(argv)):
            dest = "/.backlot-bin/" + os.path.basename(resolved)
            _reject_secret_source(policy, ("--ro-bind", resolved, dest))
            argv.extend(("--ro-bind", resolved, dest))
            command[0] = dest

    argv.extend(("--chdir", policy.workspace))
    argv.extend(("--seccomp", SECCOMP_FD_PLACEHOLDER))
    argv.append("--")
    argv.extend(command or ["/usr/bin/true"])

    _final_secret_scan(policy, argv, decoy_host)
    return Plan(
        policy=policy,
        program=program,
        argv=argv,
        decoy_checksum=decoy_sum,
        workspace_host=runtime.workspace_host,
        decoy_host=decoy_host,
    )


def bind_sources(argv: list[str]) -> list[str]:
    out = []
    i = 0
    while i < len(argv):
        if argv[i] in BIND_SRC_FLAGS and i + 2 < len(argv):
            out.append(argv[i + 1])
            i += 3
            continue
        i += 1
    return out


def _as_flag_tuples(argv: list[str]) -> list[tuple[str, ...]]:
    out: list[tuple[str, ...]] = []
    i = 0
    while i < len(argv):
        if argv[i] in BIND_SRC_FLAGS | {"--symlink"} and i + 2 < len(argv):
            out.append((argv[i], argv[i + 1], argv[i + 2]))
            i += 3
            continue
        i += 1
    return out


def _reject_secret_source(policy: Policy, tup: tuple[str, ...]) -> None:
    if tup[0] not in BIND_SRC_FLAGS:
        return
    src = tup[1]
    if policy.is_secret(src):
        raise ValueError(f"refusing to bind secret path as source: {src}")


def _final_secret_scan(
    policy: Policy, argv: list[str], decoy_host: dict[str, Path]
) -> None:
    """No bind *source* may be a secret path. Dest may be a decoy dest."""
    i = 0
    while i < len(argv):
        if argv[i] in BIND_SRC_FLAGS and i + 2 < len(argv):
            src, dest = argv[i + 1], argv[i + 2]
            if policy.is_secret(src):
                raise ValueError(f"argv contains secret bind source: {src}")
            if policy.is_secret(dest) and dest not in decoy_host:
                raise ValueError(f"argv binds secret dest without decoy: {dest}")
            i += 3
            continue
        i += 1


def find_bwrap() -> str:
    found = shutil.which("bwrap")
    if not found:
        raise FileNotFoundError("bwrap not on PATH")
    return found


def _shell(arg: str) -> str:
    if arg == SECCOMP_FD_PLACEHOLDER:
        return arg
    if not arg or any(ch in arg for ch in ' \t\n"$\'\\'):
        return "'" + arg.replace("'", "'\\''") + "'"
    return arg
