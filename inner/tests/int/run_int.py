#!/usr/bin/env python3
"""Adversarial integration tests T1–T10. Requires bwrap + gcc."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

INNER = Path(__file__).resolve().parents[2]
PROGRAMS = Path(__file__).resolve().parent / "programs"
REPO = INNER.parent
REAL_MARKER = "REAL_SECRET_MARKER"
CANARY = "CANARY"

# Sensitive / noise paths for the property test (50 picks, deterministic).
PATH_POOL = [
    "/opt/grok",
    "/opt/grok/secrets.env",
    "/opt/grok/CANARY.txt",
    "/root/.ssh/id_rsa",
    "/root/.ssh",
    "/home",
    "/home/user/.aws/credentials",
    "/etc/shadow",
    "/etc/ssh/ssh_host_rsa_key",
    "/etc/passwd",
    "/var/run/secrets/kubernetes.io/serviceaccount/token",
    "/proc/1/root/opt/grok/secrets.env",
    "/proc/self/root/opt/grok/secrets.env",
    "/proc/1/environ",
    "/workspace/hello.txt",
    "/workspace/../opt/grok/secrets.env",
    "/usr/bin/ls",
    "/tmp/foo",
    "/var/log/syslog",
    "/dev/kmsg",
    "/opt/other",
    "/run/secrets",
    "/root/.bashrc",
    "/etc/hostname",
    "/etc/shadow-",
]


def main() -> int:
    results: list[tuple[str, str, str]] = []
    missing = []
    if shutil.which("bwrap") is None:
        missing.append("bwrap not on PATH")
    if shutil.which("gcc") is None:
        missing.append("gcc not on PATH")
    if missing:
        reason = "; ".join(missing)
        for name in _all_names():
            results.append((name, "NOT RUN", reason))
        _print(results)
        return 2

    tmp = Path(tempfile.mkdtemp(prefix="backlot-int-"))
    bindir = tmp / "bin"
    bindir.mkdir()
    try:
        _compile(bindir)
        ctx = _Ctx(tmp=tmp, bindir=bindir)
        _seed(ctx)
        results.append(_run("demo_ls", lambda: _demo_ls(ctx)))
        results.append(_run("demo_cat", lambda: _demo_cat(ctx)))
        results.append(_run("T1", lambda: _t1(ctx)))
        results.append(_run("T1_no_decoy", lambda: _t1_no_decoy(ctx)))
        results.append(_run("T2", lambda: _t2(ctx)))
        results.append(_run("T3", lambda: _t3(ctx)))
        results.append(_run("T4", lambda: _t4(ctx)))
        results.append(_run("T5", lambda: _t5(ctx)))
        results.append(_run("T6", lambda: _t6(ctx)))
        results.append(_run("T7", lambda: _t7(ctx)))
        results.append(_run("T8", lambda: _t8(ctx)))
        results.append(_run("T9", lambda: _t9(ctx)))
        results.append(_run("T10", lambda: _t10(ctx)))
        results.append(_run("P50_paths", lambda: _p50(ctx)))
        results.append(_run("P_argv_mut", lambda: _p_mut(ctx)))
    except Exception as exc:
        results.append(("runner", "FAIL", repr(exc)))
    _print(results)
    return 0 if all(s == "PASS" for _, s, _ in results) else 1


def _all_names() -> list[str]:
    return [
        "demo_ls",
        "demo_cat",
        "T1",
        "T1_no_decoy",
        "T2",
        "T3",
        "T4",
        "T5",
        "T6",
        "T7",
        "T8",
        "T9",
        "T10",
        "P50_paths",
        "P_argv_mut",
    ]


class _Ctx:
    def __init__(self, tmp: Path, bindir: Path):
        self.tmp = tmp
        self.bindir = bindir
        self.workspace = tmp / "workspace"
        self.audit_dir = tmp / "audit"
        self.real_secret = tmp / "real-opt-grok"
        self.workspace.mkdir()
        self.audit_dir.mkdir()
        self.real_secret.mkdir()


def _seed(ctx: _Ctx) -> None:
    (ctx.workspace / "hello.txt").write_text("hello-from-workspace\n")
    (ctx.real_secret / "secrets.env").write_text(
        f"{REAL_MARKER}=this-must-never-appear-inside-the-jail\n"
    )


def _compile(bindir: Path) -> None:
    mapping = {
        "t1_listdir": "t1_listdir.c",
        "t2_open": "t2_open.c",
        "t3_chdir": "t3_chdir.c",
        "t4_proc": "t4_proc.c",
        "t5_unshare": "t5_unshare.c",
        "t6_network": "t6_network.c",
        "t7_write": "t7_write.c",
        "backlot-t8-orphan": "t8_orphan.c",
        "t9_decoy": "t9_decoy.c",
        "t_open_paths": "t_open_paths.c",
    }
    for out, src in mapping.items():
        _cc(PROGRAMS / src, bindir / out)
    lsdir = bindir / "named"
    lsdir.mkdir()
    _cc(PROGRAMS / "t10_ls.c", lsdir / "ls")


def _cc(src: Path, dest: Path) -> None:
    cmd = ["gcc", "-O2", "-Wall", "-o", str(dest), str(src)]
    subprocess.run(cmd, check=True)


def _run(name, fn) -> tuple[str, str, str]:
    try:
        detail = fn()
        return (name, "PASS", detail or "ok")
    except _Fail as exc:
        return (name, "FAIL", str(exc))
    except subprocess.TimeoutExpired:
        return (name, "FAIL", "timeout")
    except Exception as exc:
        return (name, "FAIL", f"{type(exc).__name__}: {exc}")


class _Fail(AssertionError):
    pass


def _jail(ctx: _Ctx, command: list[str], *, no_decoy: bool = False, ident: str = "t"):
    audit = ctx.audit_dir / f"{ident}.jsonl"
    cmd = [
        sys.executable,
        str(INNER / "run.py"),
        "--policy",
        str(INNER / "policy.yaml"),
        "--workspace",
        str(ctx.workspace),
        "--audit",
        str(audit),
        "--tmp-root",
        str(ctx.tmp / f"rt-{ident}"),
        "--sandbox-id",
        ident,
    ]
    if no_decoy:
        cmd.append("--no-decoy")
    cmd += ["--", *command]
    proc = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        timeout=25,
        check=False,
    )
    blob = (proc.stdout or "") + (proc.stderr or "")
    if REAL_MARKER in blob:
        raise _Fail(f"real secret marker leaked\n{blob}")
    return proc, audit


def _need(cond: bool, msg: str) -> None:
    if not cond:
        raise _Fail(msg)


def _demo_ls(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, ["/usr/bin/ls", "/workspace"], ident="demo_ls")
    _need(proc.returncode == 0, f"ls rc={proc.returncode}\n{proc.stderr}\n{proc.stdout}")
    _need("hello.txt" in proc.stdout, f"ls stdout={proc.stdout!r} err={proc.stderr!r}")
    return "ls /workspace → hello.txt"


def _demo_cat(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, ["/usr/bin/cat", "/workspace/hello.txt"], ident="demo_cat")
    _need(proc.returncode == 0, f"cat rc={proc.returncode}\n{proc.stderr}")
    _need("hello-from-workspace" in proc.stdout, proc.stdout)
    return "cat workspace file"


def _t1(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t1_listdir")], ident="t1")
    _need(proc.returncode == 0, f"rc={proc.returncode} {proc.stderr} {proc.stdout}")
    _need("entry=CANARY.txt" in proc.stdout, proc.stdout)
    _need("entry=secrets.env" in proc.stdout, proc.stdout)
    _need("entry=id_rsa" in proc.stdout, proc.stdout)
    _need(REAL_MARKER not in proc.stdout, "real tree listed")
    return "decoy listing"


def _t1_no_decoy(ctx: _Ctx) -> str:
    proc, _ = _jail(
        ctx, [str(ctx.bindir / "t1_listdir")], no_decoy=True, ident="t1nd"
    )
    _need("opendir_errno=2" in proc.stdout, f"expected ENOENT got {proc.stdout!r} {proc.stderr!r}")
    return "ENOENT without decoy"


def _t2(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t2_open")], no_decoy=True, ident="t2")
    _need("open_errno=2" in proc.stdout, f"expected ENOENT got {proc.stdout!r} {proc.stderr!r}")
    return "open real path without decoy → ENOENT"


def _t3(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t3_chdir")], ident="t3")
    out = proc.stdout
    _need("chdir_slash rc=" in out, out)
    _need("chdir_grok rc=" in out, out)
    _need("chdir_slash rc=0" not in out, f"chdir(/) succeeded\n{out}")
    _need("chdir_grok rc=0" not in out, f"chdir(/opt/grok) succeeded\n{out}")
    _need(
        "errno=1" in out or "errno=2" in out,
        f"expected EPERM or ENOENT\n{out}",
    )
    return out.replace("\n", "; ")


def _t4(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t4_proc")], ident="t4")
    out = proc.stdout
    _need(REAL_MARKER not in out, out)
    # /root and /etc/shadow must not exist (ENOENT) — I1
    _need("open /root/.ssh/id_rsa errno=2" in out, out)
    _need("open /etc/shadow errno=2" in out, out)
    if "open /proc/self/root/opt/grok/secrets.env ok" in out:
        _need(CANARY in out, "proc-root saw non-canary contents")
    return "proc tricks did not leak real secrets"


def _t5(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t5_unshare")], ident="t5")
    out = proc.stdout
    _need("unshare rc=" in out, out)
    _need("unshare rc=0" not in out, f"unshare succeeded\n{out}")
    _need("clone_newuser rc=0" not in out, f"clone NEWUSER succeeded\n{out}")
    _need("clone3 rc=0" not in out, f"clone3 succeeded\n{out}")
    return out.replace("\n", "; ")


def _t6(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t6_network")], ident="t6")
    _need(proc.returncode == 0, f"rc={proc.returncode} {proc.stdout} {proc.stderr}")
    _need("connect rc=0" not in proc.stdout, proc.stdout)
    return proc.stdout.replace("\n", "; ")


def _t7(ctx: _Ctx) -> str:
    proc, _ = _jail(ctx, [str(ctx.bindir / "t7_write")], ident="t7")
    out = proc.stdout
    _need("write /workspace/t7.txt" in out and "n=1" in out, out)
    _need((ctx.workspace / "t7.txt").read_text() == "x", "workspace write missing")
    for path in ("/usr/evil", "/etc/evil", "/tmp/evil", "/root/evil"):
        hit = [ln for ln in out.splitlines() if ln.startswith(f"write {path}")]
        _need(hit, f"missing result for {path}\n{out}")
        _need(all("n=1" not in ln for ln in hit), f"write succeeded: {hit}")
    _need(not Path("/usr/evil").exists(), "host /usr/evil created")
    return "workspace writable; outside not"


def _t8(ctx: _Ctx) -> str:
    binary = ctx.bindir / "backlot-t8-orphan"
    proc, _ = _jail(ctx, [str(binary)], ident="t8")
    _need(proc.returncode == 0, f"rc={proc.returncode} {proc.stdout} {proc.stderr}")
    _need("child_pid=" in proc.stdout, proc.stdout)
    time.sleep(0.3)
    leftovers = []
    for pid in Path("/proc").iterdir():
        if not pid.name.isdigit():
            continue
        try:
            cmd = (pid / "cmdline").read_bytes().replace(b"\0", b" ").decode()
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
        if "backlot-t8-orphan" in cmd:
            leftovers.append((pid.name, cmd))
    _need(not leftovers, f"orphans remain: {leftovers}")
    return "no leftover child"


def _t9(ctx: _Ctx) -> str:
    ident = "t9"
    audit = ctx.audit_dir / f"{ident}.jsonl"
    proc, audit = _jail(
        ctx,
        [str(ctx.bindir / "t9_decoy"), str(audit)],
        ident=ident,
    )
    out = proc.stdout
    _need("decoy_open ok" in out, out)
    _need(CANARY in out, out)
    _need("unlink_audit" in out, out)
    _need("unlink_audit" in out and "rc=0" not in [
        ln for ln in out.splitlines() if ln.startswith("unlink_audit")
    ][0], out)
    _need("truncate_audit rc=-1" in out or "truncate_audit rc=-1" in out.replace("rc=-1", "rc=-1"), out)
    # errno ENOENT (2) or EPERM (1) — not success
    for ln in out.splitlines():
        if ln.startswith("unlink_audit") and "rc=0" in ln:
            raise _Fail(ln)
        if ln.startswith("truncate_audit") and "rc=-1" not in ln and "errno=" not in ln:
            raise _Fail(ln)
        if ln.startswith("truncate_audit") and " rc=-1 " not in f" {ln} " and not ln.endswith("rc=-1"):
            # fd number would be >= 0
            if " rc=-1" not in ln and "errno=" in ln and "rc=-1" not in ln:
                # open returned a non-negative fd
                if "rc=-1" not in ln:
                    parts = ln.split("rc=")
                    if len(parts) > 1 and not parts[1].startswith("-"):
                        fdn = parts[1].split()[0]
                        if fdn.lstrip("-").isdigit() and int(fdn) >= 0:
                            raise _Fail(f"audit writable from jail: {ln}")
    ok, msg = _verify(audit)
    _need(ok, msg)
    events = _load_events(audit)
    kinds = [e["kind"] for e in events]
    _need("start" in kinds, kinds)
    _need(
        "decoy_open" in kinds,
        f"no decoy_open in {kinds}; stdout={out!r} stderr={proc.stderr!r}",
    )
    return f"decoy logged; jail cannot unlink audit ({msg})"


def _t10(ctx: _Ctx) -> str:
    ls = ctx.bindir / "named" / "ls"
    _need(ls.name == "ls", ls.name)
    proc, _ = _jail(ctx, [str(ls)], ident="t10")
    _need("named_ls_chdir" in proc.stdout, proc.stdout + proc.stderr)
    _need("named_ls_chdir rc=0" not in proc.stdout, "chdir succeeded in fake ls")
    _need(proc.returncode == 0, f"rc={proc.returncode} {proc.stdout}")
    return proc.stdout.strip()


def _p50(ctx: _Ctx) -> str:
    # 50 paths: secrets must not yield REAL_MARKER; decoy may yield CANARY.
    paths = (PATH_POOL * 3)[:50]
    proc, _ = _jail(ctx, [str(ctx.bindir / "t_open_paths"), *paths], ident="p50")
    _need(REAL_MARKER not in proc.stdout, proc.stdout)
    readable_secrets = []
    for line in proc.stdout.splitlines():
        if line.startswith("PATH ") and " ok " in line:
            path = line.split(" ", 2)[1]
            if path.startswith("/root") or path.startswith("/home") or path == "/etc/shadow":
                readable_secrets.append(line)
            if path.startswith("/opt/grok/") and CANARY not in line:
                readable_secrets.append(line)
    _need(not readable_secrets, f"unexpected readable secrets: {readable_secrets}")
    return f"50 paths; no real secrets; rc={proc.returncode}"


def _p_mut(ctx: _Ctx) -> str:
    variants = [
        ["/usr/bin/cat", "/workspace/../opt/grok/secrets.env"],
        ["/usr/bin/cat", "--", "/opt/grok/../opt/grok/secrets.env"],
        ["/usr/bin/ls", "-al", "/opt/grok/../../root"],
        ["/usr/bin/ls", "-l", "/proc/1/root/opt/grok"],
    ]
    blobs = []
    for i, cmd in enumerate(variants):
        proc, _ = _jail(ctx, cmd, ident=f"pmut{i}")
        blob = proc.stdout + proc.stderr
        _need(REAL_MARKER not in blob, blob)
        blobs.append(blob[:80])
    return "mutated argv leaked no real secret bytes"


def _verify(path: Path):
    sys.path.insert(0, str(INNER))
    from audit import verify_chain

    return verify_chain(path)


def _load_events(path: Path) -> list[dict]:
    events = []
    for line in path.read_text().splitlines():
        if line.strip():
            events.append(json.loads(line))
    return events


def _print(results: list[tuple[str, str, str]]) -> None:
    width = max(len(n) for n, _, _ in results)
    print("Backlot M1 integration")
    for name, status, detail in results:
        print(f"  {name:<{width}}  {status:<8}  {detail}")
    npass = sum(1 for _, s, _ in results if s == "PASS")
    nfail = sum(1 for _, s, _ in results if s == "FAIL")
    nskip = sum(1 for _, s, _ in results if s == "NOT RUN")
    print(f"summary: {npass} pass / {nfail} fail / {nskip} not-run")


if __name__ == "__main__":
    sys.exit(main())
