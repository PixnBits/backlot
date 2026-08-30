"""Load and validate policy.yaml.

Invariants (restated):
  I1 default-deny mounts — only listed binds exist; missing paths are ENOENT.
  I2 real secret paths are never bind sources; a decoy may occupy the dest.
  I3 syscall allow-list; binary name is not policy; I3 denies cannot be waived.
  I4 demo network is none — no --share-net.
  I5 --die-with-parent is mandatory.
  I6 audit sink is not a mount.
  I7 policy/seccomp/decoy hashes are printed at start.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from hashes import sha256_file
from syscalls import ALLOWED_DEMO_NAMES, DENIED_I3_SET, number
from yaml_lite import YamlError, load_path

KNOWN_KEYS = frozenset(
    {
        "profile",
        "workspace",
        "network",
        "proxy_addr",
        "secret_paths",
        "decoy_paths",
        "allowed_binaries",
        "path_allow",
        "ro_binds",
        "rw_binds",
        "allowed_syscalls",
        "denied_syscalls",
    }
)


class PolicyError(ValueError):
    pass


@dataclass(frozen=True)
class Bind:
    host: str
    dest: str
    write: bool = False


@dataclass
class Policy:
    source_path: Path
    profile: str
    workspace: str
    network: str
    proxy_addr: str | None
    secret_paths: tuple[str, ...]
    decoy_paths: dict[str, str]  # jail dest -> "runtime" or host dir
    allowed_binaries: tuple[str, ...]
    ro_binds: tuple[Bind, ...]
    rw_binds: tuple[Bind, ...]
    allowed_syscalls: tuple[str, ...]
    denied_syscalls: tuple[str, ...]
    policy_hash: str
    extras: dict[str, Any] = field(default_factory=dict)

    def is_secret(self, path: str) -> bool:
        path = _norm(path)
        for secret in self.secret_paths:
            if path == secret or path.startswith(secret.rstrip("/") + "/"):
                return True
        return False


def load_policy(path: str | Path) -> Policy:
    path = Path(path).resolve()
    try:
        raw = load_path(str(path))
    except YamlError as exc:
        raise PolicyError(f"{path}: {exc}") from exc
    if not isinstance(raw, dict):
        raise PolicyError("policy root must be a mapping")
    unknown = set(raw) - KNOWN_KEYS
    if unknown:
        raise PolicyError(f"unknown policy keys: {sorted(unknown)}")

    profile = _req_str(raw, "profile")
    workspace = _req_str(raw, "workspace")
    network = raw.get("network", "none")
    if network not in ("none", "proxy"):
        raise PolicyError("network must be none|proxy")
    proxy_addr = raw.get("proxy_addr")
    if proxy_addr is not None:
        proxy_addr = str(proxy_addr)
    if network == "proxy" and not proxy_addr:
        raise PolicyError("proxy_addr required when network=proxy")
    if network == "none" and proxy_addr:
        raise PolicyError("proxy_addr set but network is none")

    secret_paths = tuple(_norm(p) for p in _str_list(raw.get("secret_paths", [])))
    decoy_raw = raw.get("decoy_paths") or {}
    if not isinstance(decoy_raw, dict):
        raise PolicyError("decoy_paths must be a mapping dest -> runtime|hostdir")
    decoy_paths = {_norm(k): str(v) for k, v in decoy_raw.items()}

    allowed_binaries = tuple(_str_list(raw.get("allowed_binaries") or raw.get("path_allow") or []))
    ro_binds = tuple(_parse_binds(raw.get("ro_binds") or [], write=False))
    rw_binds = tuple(_parse_binds(raw.get("rw_binds") or [], write=True))

    if "allowed_syscalls" in raw:
        allowed_syscalls = tuple(_str_list(raw["allowed_syscalls"]))
    else:
        allowed_syscalls = ALLOWED_DEMO_NAMES
    denied_syscalls = tuple(
        dict.fromkeys([*_str_list(raw.get("denied_syscalls") or []), *sorted(DENIED_I3_SET)])
    )

    overlap = set(allowed_syscalls) & set(denied_syscalls)
    if overlap:
        raise PolicyError(
            "allowed_syscalls includes I3/denied names (cannot waive): "
            + ", ".join(sorted(overlap))
        )
    for name in (*allowed_syscalls, *denied_syscalls):
        number(name)  # raises if unknown

    _check_secret_binds(secret_paths, decoy_paths, ro_binds + rw_binds)

    return Policy(
        source_path=path,
        profile=profile,
        workspace=_norm(workspace),
        network=network,
        proxy_addr=proxy_addr,
        secret_paths=secret_paths,
        decoy_paths=decoy_paths,
        allowed_binaries=allowed_binaries,
        ro_binds=ro_binds,
        rw_binds=rw_binds,
        allowed_syscalls=allowed_syscalls,
        denied_syscalls=denied_syscalls,
        policy_hash=sha256_file(path),
    )


def _check_secret_binds(
    secret_paths: tuple[str, ...],
    decoy_paths: dict[str, str],
    binds: tuple[Bind, ...],
) -> None:
    secrets = set(secret_paths)
    for bind in binds:
        host = _norm(bind.host)
        dest = _norm(bind.dest)
        if host in secrets or any(host == s or host.startswith(s + "/") for s in secrets):
            raise PolicyError(f"bind source is a secret path: {bind.host}")
        if dest in secrets and dest not in decoy_paths:
            raise PolicyError(
                f"bind dest {bind.dest} is a secret path without a decoy mapping"
            )
        if dest in decoy_paths:
            raise PolicyError(
                f"bind dest {bind.dest} collides with decoy_paths; "
                "decoy generator owns that dest"
            )
    for dest, src in decoy_paths.items():
        dest_n = _norm(dest)
        if dest_n not in secrets:
            raise PolicyError(f"decoy dest {dest} is not in secret_paths")
        if src != "runtime":
            src_n = _norm(src)
            if src_n in secrets or any(
                src_n == s or src_n.startswith(s + "/") for s in secrets
            ):
                raise PolicyError("decoy host dir must not be a real secret path")


def _parse_binds(items: Any, write: bool) -> list[Bind]:
    if items is None:
        items = []
    if not isinstance(items, list):
        raise PolicyError("ro_binds/rw_binds must be a list")
    out: list[Bind] = []
    for item in items:
        if isinstance(item, str):
            p = _norm(item)
            out.append(Bind(host=p, dest=p, write=write))
        elif isinstance(item, dict) and "host" in item and "dest" in item:
            out.append(
                Bind(host=_norm(str(item["host"])), dest=_norm(str(item["dest"])), write=write)
            )
        else:
            raise PolicyError(f"bind entry must be a path or {{host, dest}}: {item!r}")
    return out


def _str_list(value: Any) -> list[str]:
    if value is None:
        return []
    if not isinstance(value, list):
        raise PolicyError(f"expected list, got {type(value).__name__}")
    out = []
    for item in value:
        if not isinstance(item, (str, int)):
            raise PolicyError(f"expected string list item, got {item!r}")
        out.append(str(item))
    return out


def _req_str(raw: dict, key: str) -> str:
    if key not in raw or not isinstance(raw[key], str) or not raw[key]:
        raise PolicyError(f"{key} must be a non-empty string")
    return raw[key]


def _norm(path: str) -> str:
    path = path.strip()
    if not path:
        raise PolicyError("empty path")
    if not path.startswith("/"):
        raise PolicyError(f"path must be absolute: {path!r}")
    while "//" in path:
        path = path.replace("//", "/")
    if len(path) > 1:
        path = path.rstrip("/")
    return path
