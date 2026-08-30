import json
import tempfile
import unittest
from pathlib import Path

from audit import GENESIS, AuditLog, verify_chain


class AuditTests(unittest.TestCase):
    def test_hash_chain(self):
        path = Path(tempfile.mkdtemp()) / "audit.jsonl"
        log = AuditLog(path, "sbox1")
        e1 = log.emit("start", {"n": 1})
        e2 = log.emit("decoy_open", {"name": "secrets.env"})
        e3 = log.emit("exit", {"returncode": 0})
        log.close()
        self.assertEqual(e1["prev_hash"], GENESIS)
        self.assertEqual(e2["prev_hash"], e1["event_hash"])
        self.assertEqual(e3["prev_hash"], e2["event_hash"])
        ok, msg = verify_chain(path)
        self.assertTrue(ok, msg)
        self.assertIn("3 events", msg)

    def test_tamper_detected(self):
        path = Path(tempfile.mkdtemp()) / "audit.jsonl"
        log = AuditLog(path, "sbox1")
        log.emit("start", {"n": 1})
        log.emit("exit", {"returncode": 0})
        log.close()
        lines = path.read_text().splitlines()
        obj = json.loads(lines[1])
        obj["payload"]["returncode"] = 99
        lines[1] = json.dumps(obj)
        path.write_text("\n".join(lines) + "\n")
        ok, msg = verify_chain(path)
        self.assertFalse(ok)
        self.assertIn("mismatch", msg)

    def test_append_only_fd(self):
        path = Path(tempfile.mkdtemp()) / "audit.jsonl"
        log = AuditLog(path, "sbox1")
        log.emit("start", {"n": 1})
        log.close()
        # reopening continues the chain rather than truncating
        log2 = AuditLog(path, "sbox1")
        log2.emit("later", {"n": 2})
        log2.close()
        ok, msg = verify_chain(path)
        self.assertTrue(ok, msg)
        self.assertIn("2 events", msg)
