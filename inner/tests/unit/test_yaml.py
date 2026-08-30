import unittest
from pathlib import Path

import yaml_lite
from policy import load_policy

INNER = Path(__file__).resolve().parents[2]


class YamlTests(unittest.TestCase):
    def test_policy_yaml_loads(self):
        raw = yaml_lite.load_path(str(INNER / "policy.yaml"))
        self.assertEqual(raw["profile"], "demo")
        self.assertEqual(raw["network"], "none")
        self.assertEqual(raw["decoy_paths"]["/opt/grok"], "runtime")
        self.assertEqual(raw["rw_binds"], [])
        self.assertIn("ls", raw["allowed_binaries"])
        self.assertIn("chdir", raw["denied_syscalls"])
        self.assertNotIn("chdir", raw["allowed_syscalls"])

    def test_unknown_comment_and_slash_keys(self):
        text = """
# hi
root: /workspace
nested:
  /opt/grok: runtime
items:
  - a
  - b
empty: []
flag: true
none: null
"""
        data = yaml_lite.loads(text)
        self.assertEqual(data["nested"]["/opt/grok"], "runtime")
        self.assertEqual(data["items"], ["a", "b"])
        self.assertEqual(data["empty"], [])
        self.assertIs(data["flag"], True)
        self.assertIsNone(data["none"])

    def test_demo_policy_object(self):
        p = load_policy(INNER / "policy.yaml")
        self.assertEqual(p.profile, "demo")
        self.assertTrue(p.is_secret("/opt/grok"))
        self.assertTrue(p.is_secret("/opt/grok/secrets.env"))
        self.assertFalse(p.is_secret("/workspace"))
