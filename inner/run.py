#!/usr/bin/env python3
"""Inner-ring driver: compile policy → decoy → seccomp → bwrap → audit.

Usage:
  inner/bwrap-run.sh [--policy PATH] [--audit PATH] [--workspace HOSTDIR]
                     [--print-plan] [--no-decoy] -- command [args...]
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile
import uuid
from pathlib import Path

# Allow `python3 inner/run.py` and `cd inner && python3 -m run`.
INNER = Path(__file__).resolve().parent
if str(INNER) not in sys.path:
    sys.path.insert(0, str(INNER))

from audit import AuditLog, DecoyWatcher  # noqa: E402
from bwrap import Runtime, build_plan, find_bwrap  # noqa: E402
from decoy import assert_not_real_secret, materialize  # noqa: E402
from policy import PolicyError, load_policy  # noqa: E402


def main(argv: list[str] | None = None) -> int:
    args = _parse(argv)
    policy_path = Path(args.policy).resolve() if args.policy else INNER / "policy.yaml"
    try:
        policy = load_policy(policy_path)
    except PolicyError as exc:
        print(f"policy error: {exc}", file=sys.stderr)
        return 2

    sandbox_id = args.sandbox_id or uuid.uuid4().hex[:12]
    tmp_root = Path(args.tmp_root) if args.tmp_root else Path(tempfile.mkdtemp(prefix="backlot-m1-"))
    tmp_root.mkdir(parents=True, exist_ok=True)
    workspace_host = Path(args.workspace).resolve() if args.workspace else tmp_root / "workspace"
    workspace_host.mkdir(parents=True, exist_ok=True)

    decoy_host: dict[str, Path] = {}
    if not args.no_decoy:
        for dest, src in policy.decoy_paths.items():
            if src == "runtime":
                host_dir = tmp_root / "decoy" / dest.strip("/").replace("/", "_")
            else:
                host_dir = Path(src)
            host_dir.mkdir(parents=True, exist_ok=True)
            assert_not_real_secret(host_dir)
            materialize(host_dir)
            decoy_host[dest] = host_dir.resolve()

    command = list(args.command)
    if command and command[0] == "--":
        command = command[1:]
    if not command:
        command = ["/usr/bin/true"]

    runtime = Runtime(
        workspace_host=workspace_host,
        decoy_host=decoy_host,
        command=command,
        enable_decoy=not args.no_decoy,
    )
    plan = build_plan(policy, runtime, bwrap_bin=args.bwrap or "bwrap")

    if args.dump_table:
        Path(args.dump_table).parent.mkdir(parents=True, exist_ok=True)
        Path(args.dump_table).write_text(plan.program.table_text, encoding="utf-8")
    if args.dump_bpf:
        Path(args.dump_bpf).write_bytes(plan.program.bpf)

    print(f"policy_hash: {plan.policy_hash}", file=sys.stderr)
    print(f"seccomp_hash: {plan.seccomp_hash}", file=sys.stderr)
    print(f"decoy_checksum: {plan.decoy_checksum}", file=sys.stderr)
    print(f"sandbox_id: {sandbox_id}", file=sys.stderr)

    if args.print_plan:
        sys.stdout.write(plan.review_text())
        return 0

    try:
        bwrap_bin = args.bwrap or find_bwrap()
    except FileNotFoundError as exc:
        print(f"NOT RUN: {exc}", file=sys.stderr)
        return 3

    plan.argv[0] = bwrap_bin

    audit_path = (
        Path(args.audit).resolve()
        if args.audit
        else INNER / "var" / f"audit-{sandbox_id}.jsonl"
    )
    audit = AuditLog(audit_path, sandbox_id)
    watcher = None
    try:
        audit.emit(
            "start",
            {
                "policy_hash": plan.policy_hash,
                "seccomp_hash": plan.seccomp_hash,
                "decoy_checksum": plan.decoy_checksum,
                "command": command,
                "network": policy.network,
            },
        )
        if decoy_host:
            # Watch the first decoy dir (demo has one). Drain after exec.
            first = next(iter(decoy_host.values()))
            watcher = DecoyWatcher(first)
        return _exec(plan, audit, watcher)
    finally:
        if watcher is not None:
            watcher.close()
        audit.close()


def _exec(plan, audit: AuditLog, watcher) -> int:
    bpf = plan.program.bpf
    fd = _seccomp_fd(bpf)
    try:
        argv = plan.argv_for_exec(fd)
        audit.emit("exec", {"argv_head": argv[:12], "seccomp_fd": fd})
        proc = subprocess.run(argv, pass_fds=(fd,), check=False)
        if watcher is not None:
            for ev in watcher.drain(timeout=0.5):
                if ev.get("name"):
                    audit.emit("decoy_open", ev)
        audit.emit("exit", {"returncode": proc.returncode})
        return proc.returncode
    finally:
        os.close(fd)


def _seccomp_fd(bpf: bytes) -> int:
    """Create a readable fd holding struct sock_filter[]. Not a magic number."""
    if hasattr(os, "memfd_create"):
        fd = os.memfd_create("backlot-seccomp", 0)
    else:
        tmp = tempfile.NamedTemporaryFile(prefix="backlot-seccomp-", delete=False)
        tmp.write(bpf)
        tmp.flush()
        fd = os.open(tmp.name, os.O_RDONLY)
        os.unlink(tmp.name)
        tmp.close()
        os.lseek(fd, 0, os.SEEK_SET)
        os.set_inheritable(fd, True)
        return fd
    os.write(fd, bpf)
    os.lseek(fd, 0, os.SEEK_SET)
    os.set_inheritable(fd, True)
    return fd


def _parse(argv: list[str] | None) -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Backlot M1 inner ring (Bubblewrap + seccomp + decoy + audit)"
    )
    p.add_argument("--policy", help="path to policy.yaml")
    p.add_argument("--audit", help="append-only audit jsonl (never mounted into the jail)")
    p.add_argument("--workspace", help="host directory bound at policy workspace dest")
    p.add_argument("--tmp-root", help="scratch parent for decoy + default workspace")
    p.add_argument("--sandbox-id", help="id recorded in the audit log")
    p.add_argument("--bwrap", help="bwrap binary")
    p.add_argument("--print-plan", action="store_true", help="print argv + tables and exit")
    p.add_argument("--dump-table", help="write human-readable syscall table")
    p.add_argument("--dump-bpf", help="write raw sock_filter bytes")
    p.add_argument(
        "--no-decoy",
        action="store_true",
        help="omit decoy binds (secrets still never bound). Used by T2.",
    )
    p.add_argument(
        "command",
        nargs=argparse.REMAINDER,
        help="command to run inside the jail (prefix with --)",
    )
    return p.parse_args(argv)


if __name__ == "__main__":
    sys.exit(main())
