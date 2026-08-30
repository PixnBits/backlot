import tempfile
import unittest
from pathlib import Path

from decoy import FILES, REAL_MARKER_NEVER, WATERMARK, materialize


class DecoyTests(unittest.TestCase):
    def test_every_file_watermarked(self):
        for name, body in FILES.items():
            self.assertTrue(
                WATERMARK in body or "deadbeef" in body,
                name,
            )
            self.assertNotIn(REAL_MARKER_NEVER, body)

    def test_materialize_checksum_stable(self):
        a = Path(tempfile.mkdtemp()) / "d1"
        b = Path(tempfile.mkdtemp()) / "d2"
        ca = materialize(a)
        cb = materialize(b)
        self.assertEqual(ca, cb)
        self.assertEqual(len(ca), 64)
        self.assertTrue((a / "secrets.env").is_file())
        self.assertTrue((a / "id_rsa").is_file())
        self.assertIn(b"CANARY", (a / "credentials.json").read_bytes())

    def test_id_rsa_is_not_a_key(self):
        text = FILES["id_rsa"]
        self.assertIn("CANARY", text)
        self.assertIn("NOT-A-REAL", text)
        self.assertNotIn("-----BEGIN RSA PRIVATE KEY-----", text)
