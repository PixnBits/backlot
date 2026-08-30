"""Append-only hash-chained audit log. Lives outside the jail.

event_hash = SHA256(prev_hash || utc || kind || canonical_payload)

The jail never sees this path. Decoy opens are observed with inotify on
the host decoy directory (the bind source), not from inside bwrap.
Denied syscalls are not individually captured without eBPF / USER_NOTIF;
the wrapper logs intent (command, policy hash) instead. Residual risk
is documented in REVIEWER.md.
"""

from __future__ import annotations

import json
import os
import select
import time
from ctypes import CDLL, c_char_p, c_int, c_uint32, get_errno
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from hashes import sha256_text

GENESIS = "0" * 64

# linux/inotify.h
IN_CLOEXEC = 0x80000
IN_NONBLOCK = 0x800
IN_ACCESS = 0x00000001
IN_ATTRIB = 0x00000004
IN_OPEN = 0x00000020
IN_CREATE = 0x00000100
IN_ONLYDIR = 0x01000000
EVENT_HDR = 16  # sizeof(struct inotify_event) without name


class AuditError(RuntimeError):
    pass


class AuditLog:
    def __init__(self, path: Path, sandbox_id: str):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.sandbox_id = sandbox_id
        self.prev = _tail_hash(self.path) or GENESIS
        # O_APPEND so we cannot accidentally rewrite. We never open with O_TRUNC.
        self._fd = os.open(
            self.path,
            os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_CLOEXEC,
            0o600,
        )

    def close(self) -> None:
        if self._fd is not None:
            os.close(self._fd)
            self._fd = None

    def emit(self, kind: str, payload: dict[str, Any]) -> dict[str, Any]:
        utc = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")
        body = json.dumps(payload, sort_keys=True, separators=(",", ":"))
        digest = sha256_text(self.prev + utc + kind + body)
        event = {
            "utc": utc,
            "sandbox_id": self.sandbox_id,
            "kind": kind,
            "payload": payload,
            "prev_hash": self.prev,
            "event_hash": digest,
        }
        line = json.dumps(event, sort_keys=True, separators=(",", ":")) + "\n"
        os.write(self._fd, line.encode("utf-8"))
        os.fsync(self._fd)
        self.prev = digest
        return event


def verify_chain(path: Path) -> tuple[bool, str]:
    prev = GENESIS
    n = 0
    try:
        text = Path(path).read_text(encoding="utf-8")
    except FileNotFoundError:
        return False, "missing log"
    for line in text.splitlines():
        if not line.strip():
            continue
        event = json.loads(line)
        n += 1
        if event.get("prev_hash") != prev:
            return False, f"event {n}: prev_hash mismatch"
        body = json.dumps(event["payload"], sort_keys=True, separators=(",", ":"))
        expect = sha256_text(prev + event["utc"] + event["kind"] + body)
        if event.get("event_hash") != expect:
            return False, f"event {n}: event_hash mismatch"
        prev = event["event_hash"]
    return True, f"{n} events"


def _tail_hash(path: Path) -> str | None:
    if not path.exists() or path.stat().st_size == 0:
        return None
    last = None
    with path.open("r", encoding="utf-8") as fh:
        for line in fh:
            if line.strip():
                last = line
    if not last:
        return None
    return json.loads(last)["event_hash"]


class DecoyWatcher:
    """Host-side inotify on the decoy directory."""

    def __init__(self, decoy_dir: Path):
        self.decoy_dir = Path(decoy_dir)
        libc = CDLL("libc.so.6", use_errno=True)
        libc.inotify_init1.restype = c_int
        libc.inotify_add_watch.argtypes = [c_int, c_char_p, c_uint32]
        libc.inotify_add_watch.restype = c_int
        fd = libc.inotify_init1(IN_CLOEXEC | IN_NONBLOCK)
        if fd < 0:
            raise AuditError(f"inotify_init1 failed errno={get_errno()}")
        mask = IN_OPEN | IN_ACCESS | IN_ATTRIB | IN_CREATE | IN_ONLYDIR
        wd = libc.inotify_add_watch(fd, str(self.decoy_dir).encode(), mask)
        if wd < 0:
            os.close(fd)
            raise AuditError(f"inotify_add_watch failed errno={get_errno()}")
        self.fd = fd
        self.wd = wd
        self._libc = libc

    def close(self) -> None:
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None

    def drain(self, timeout: float = 0.2) -> list[dict[str, Any]]:
        events: list[dict[str, Any]] = []
        deadline = time.monotonic() + timeout
        while True:
            remaining = deadline - time.monotonic()
            if remaining < 0:
                remaining = 0
            ready, _, _ = select.select([self.fd], [], [], remaining)
            if not ready:
                break
            try:
                buf = os.read(self.fd, 4096)
            except BlockingIOError:
                break
            if not buf:
                break
            events.extend(_parse_inotify(buf))
            if remaining == 0:
                break
        return events


def _parse_inotify(buf: bytes) -> list[dict[str, Any]]:
    events = []
    off = 0
    while off + EVENT_HDR <= len(buf):
        wd, mask, cookie, name_len = struct_unpack(buf, off)
        off += EVENT_HDR
        name = ""
        if name_len:
            raw = buf[off : off + name_len]
            off += name_len
            name = raw.split(b"\0", 1)[0].decode("utf-8", "replace")
        events.append({"wd": wd, "mask": mask, "cookie": cookie, "name": name})
    return events


def struct_unpack(buf: bytes, off: int) -> tuple[int, int, int, int]:
    import struct

    return struct.unpack_from("iIII", buf, off)
