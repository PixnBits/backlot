"""Resolve host bind sources. Replicate Debian /bin -> usr/bin as bwrap --symlink."""

from __future__ import annotations

import os
from pathlib import Path

from policy import Bind


def resolve_ro_binds(requested: tuple[Bind, ...]) -> list[tuple[str, ...]]:
    """Return bwrap flag tuples: ('--ro-bind', src, dst) or ('--symlink', tgt, dst)."""
    flags: list[tuple[str, ...]] = []
    seen_dest = set()
    for bind in requested:
        src = Path(bind.host)
        dest = bind.dest
        if dest in seen_dest:
            continue
        if not src.exists():
            continue
        seen_dest.add(dest)
        if src.is_symlink():
            target = os.readlink(src)
            if not os.path.isabs(target):
                flags.append(("--symlink", target, dest))
                continue
            flags.append(("--ro-bind", str(src.resolve()), dest))
            continue
        flags.append(("--ro-bind", str(src), dest))
    return flags


def which(name: str) -> str | None:
    if os.path.isabs(name) and os.path.isfile(name) and os.access(name, os.X_OK):
        return name
    path = os.environ.get("PATH", "/usr/bin:/bin")
    for d in path.split(":"):
        cand = os.path.join(d, name)
        if os.path.isfile(cand) and os.access(cand, os.X_OK):
            return cand
    return None


def covered_by_binds(path: str, bind_flags: list[tuple[str, ...]]) -> bool:
    path = os.path.realpath(path)
    for flag in bind_flags:
        if flag[0] in ("--ro-bind", "--bind", "--ro-bind-try", "--bind-try"):
            src = os.path.realpath(flag[1])
            dest = flag[2]
            if path == src or path.startswith(src.rstrip("/") + "/"):
                return True
            if path == dest or path.startswith(dest.rstrip("/") + "/"):
                return True
        if flag[0] == "--symlink":
            dest = flag[2]
            if path == dest or path.startswith(dest.rstrip("/") + "/"):
                return True
    return False
