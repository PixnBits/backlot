"""Watermarked decoy (false-front) tree.

Contents are obviously fake to a human reviewer: CANARY tokens, deadbeef
checksums, a non-key in id_rsa. They look real enough to a sloppy probe.
"""

from __future__ import annotations

import os
import stat
from pathlib import Path

from hashes import decoy_checksum

WATERMARK = "CANARY"
REAL_MARKER_NEVER = "REAL_SECRET_MARKER"

FILES: dict[str, str] = {
    "CANARY.txt": """BACKLOT DECOY TREE
This directory is a theatrical flat -- plywood, not a vault.
WATERMARK=CANARY
It is not the real /opt/grok from the host.
""",
    "credentials.json": """{
  "access_key": "CANARY_NOT_A_REAL_KEY",
  "secret": "BACKLOT_DECOY_WATERMARK",
  "checksum": "deadbeefcafebabe",
  "note": "False front. Cannot authenticate to any real system."
}
""",
    ".env": """# decoy -- CANARY
API_KEY=0000000000000000
GROK_TOKEN=CANARY_TOKEN_BACKLOT_FLAT
CANARY=BACKLOT_DECOY_DO_NOT_USE
""",
    "secrets.env": """GROK_SECRET=CANARY_NOT_THE_REAL_SECRET
WATERMARK=BACKLOT_FALSE_FRONT
CHECKSUM=deadbeefcafebabe
""",
    "config.yaml": """# decoy config. api_token is a canary, not a credential.
api_token: CANARY_TOKEN_BACKLOT_FLAT
real: false
watermark: CANARY
""",
    "id_rsa": """-----BEGIN OPENSSH PRIVATE KEY-----
CANARY-THIS-KEY-IS-A-DECOY-NOT-A-REAL-RSA-KEY
checksum: deadbeefcafebabe
This block cannot authenticate anywhere.
-----END OPENSSH PRIVATE KEY-----
""",
}


class DecoyError(RuntimeError):
    pass


def materialize(dest: Path) -> str:
    dest = Path(dest)
    dest.mkdir(parents=True, exist_ok=True)
    for name, body in FILES.items():
        raw = body.encode("utf-8")
        if WATERMARK.encode() not in raw and b"deadbeef" not in raw:
            raise DecoyError(f"{name} missing watermark")
        if REAL_MARKER_NEVER.encode() in raw:
            raise DecoyError(f"{name} contains the real-secret marker")
        path = dest / name
        path.write_bytes(raw)
        mode = stat.S_IRUSR | stat.S_IWUSR
        if name == "id_rsa":
            os.chmod(path, mode)
        else:
            os.chmod(path, mode | stat.S_IRGRP | stat.S_IROTH)
    return decoy_checksum(dest)


def assert_not_real_secret(path: Path) -> None:
    """Refuse to treat a directory as decoy if it looks like a live secret tree."""
    if not path.exists():
        return
    for child in path.rglob("*"):
        if not child.is_file():
            continue
        try:
            sample = child.read_bytes()[:4096]
        except OSError:
            continue
        if REAL_MARKER_NEVER.encode() in sample:
            raise DecoyError(f"refusing to use {child} as decoy: real marker present")
