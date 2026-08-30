import tempfile
import unittest
from pathlib import Path

from policy import PolicyError, load_policy
from syscalls import DENIED_I3, ALLOWED_DEMO_NAMES

INNER = Path(__file__).resolve().parents[2]
DEMO = (INNER / "policy.yaml").read_text(encoding="utf-8")


def _load(extra: str) -> None:
    text = DEMO + extra
    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as fh:
        fh.write(text)
        path = fh.name
    try:
        return load_policy(path)
    finally:
        Path(path).unlink(missing_ok=True)


def _load_raw(text: str):
    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as fh:
        fh.write(text)
        path = fh.name
    try:
        return load_policy(path)
    finally:
        Path(path).unlink(missing_ok=True)


class PolicyCompilerTests(unittest.TestCase):
    def test_unknown_key_rejected(self):
        with self.assertRaises(PolicyError) as ctx:
            _load("\nfirecracker: true\n")
        self.assertIn("unknown", str(ctx.exception))

    def test_secret_path_as_ro_bind_rejected(self):
        text = DEMO.replace("  - /usr\n", "  - /usr\n  - /opt/grok\n")
        with self.assertRaises(PolicyError):
            _load_raw(text)

    def test_secret_path_as_rw_bind_rejected(self):
        text = DEMO.replace("rw_binds: []\n", "rw_binds:\n  - /root/.ssh\n")
        with self.assertRaises(PolicyError):
            _load_raw(text)

    def test_decoy_without_secret_rejected(self):
        text = DEMO.replace(
            "decoy_paths:\n  /opt/grok: runtime\n",
            "decoy_paths:\n  /var/secret: runtime\n",
        )
        with self.assertRaises(PolicyError):
            _load_raw(text)

    def test_chdir_in_allow_list_rejected(self):
        text = DEMO.replace("  - read\n", "  - chdir\n  - read\n")
        with self.assertRaises(PolicyError) as ctx:
            _load_raw(text)
        self.assertIn("chdir", str(ctx.exception))

    def test_i3_cannot_be_waived(self):
        for name in ("chdir", "pivot_root", "ptrace", "unshare", "bpf"):
            self.assertIn(name, DENIED_I3)

    def test_demo_allow_does_not_include_i3(self):
        overlap = set(ALLOWED_DEMO_NAMES) & set(DENIED_I3)
        self.assertEqual(overlap, set())

    def test_overlapping_secret_and_decoy_ok(self):
        p = load_policy(INNER / "policy.yaml")
        self.assertEqual(p.decoy_paths["/opt/grok"], "runtime")
        self.assertIn("/opt/grok", p.secret_paths)
