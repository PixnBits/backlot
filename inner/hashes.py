"""Stable hashes for I7 artifacts."""

from __future__ import annotations

import hashlib
from pathlib import Path


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_text(text: str) -> str:
    return sha256_bytes(text.encode("utf-8"))


def sha256_file(path: Path) -> str:
    return sha256_bytes(path.read_bytes())


def decoy_checksum(root: Path) -> str:
    """Content-address the decoy tree. Sorted relative paths + file bytes."""
    h = hashlib.sha256()
    if not root.exists():
        return sha256_bytes(b"")
    files = sorted(p for p in root.rglob("*") if p.is_file())
    for path in files:
        rel = path.relative_to(root).as_posix().encode("utf-8")
        body = path.read_bytes()
        h.update(rel)
        h.update(b"\0")
        h.update(str(len(body)).encode("ascii"))
        h.update(b"\0")
        h.update(body)
        h.update(b"\0")
    return h.hexdigest()
