import tempfile
import unittest
from pathlib import Path

from bwrap import BIND_SRC_FLAGS, Runtime, bind_sources, build_plan
from policy import load_policy

INNER = Path(__file__).resolve().parents[2]


class ArgvTests(unittest.TestCase):
    def setUp(self):
        self.policy = load_policy(INNER / "policy.yaml")
        self.tmp = Path(tempfile.mkdtemp(prefix="backlot-unit-"))
        ws = self.tmp / "ws"
        decoy = self.tmp / "decoy"
        ws.mkdir()
        decoy.mkdir()
        (decoy / "CANARY.txt").write_text("CANARY\n")
        self.runtime = Runtime(
            workspace_host=ws,
            decoy_host={"/opt/grok": decoy},
            command=["/usr/bin/ls", "/workspace"],
        )
        self.plan = build_plan(self.policy, self.runtime)

    def test_required_flags(self):
        argv = self.plan.argv
        self.assertIn("--unshare-all", argv)
        self.assertIn("--die-with-parent", argv)
        self.assertIn("--new-session", argv)
        self.assertIn("--tmpfs", argv)
        self.assertIn("--seccomp", argv)
        self.assertNotIn("--share-net", argv)

    def test_bind_sources_never_secret(self):
        secrets = set(self.policy.secret_paths)
        for src in bind_sources(self.plan.argv):
            self.assertNotIn(src, secrets)
            for s in secrets:
                self.assertFalse(src == s or src.startswith(s + "/"), src)

    def test_no_ro_bind_of_real_opt_grok(self):
        argv = self.plan.argv
        i = 0
        while i < len(argv):
            if argv[i] in BIND_SRC_FLAGS:
                src, dest = argv[i + 1], argv[i + 2]
                self.assertNotEqual(src, "/opt/grok")
                self.assertNotEqual(src, "/root/.ssh")
                self.assertNotEqual(src, "/home")
                if dest == "/opt/grok":
                    self.assertEqual(src, str(self.runtime.decoy_host["/opt/grok"]))
                i += 3
                continue
            i += 1

    def test_decoy_dest_present(self):
        argv = self.plan.argv
        self.assertIn("/opt/grok", argv)
        # dest only, with decoy host as source
        i = argv.index("--ro-bind")
        # find the decoy pair
        found = False
        for i, flag in enumerate(argv):
            if flag in BIND_SRC_FLAGS and argv[i + 2] == "/opt/grok":
                self.assertTrue(argv[i + 1].endswith("decoy") or "decoy" in argv[i + 1])
                found = True
        self.assertTrue(found)

    def test_no_decoy_omits_opt_grok_bind(self):
        rt = Runtime(
            workspace_host=self.runtime.workspace_host,
            decoy_host={"/opt/grok": self.runtime.decoy_host["/opt/grok"]},
            command=["/usr/bin/true"],
            enable_decoy=False,
        )
        plan = build_plan(self.policy, rt)
        i = 0
        dests = []
        while i < len(plan.argv):
            if plan.argv[i] in BIND_SRC_FLAGS:
                dests.append(plan.argv[i + 2])
                i += 3
                continue
            i += 1
        self.assertNotIn("/opt/grok", dests)
        for src in bind_sources(plan.argv):
            self.assertNotEqual(src, "/opt/grok")

    def test_hashes_printed_fields_exist(self):
        self.assertEqual(len(self.plan.policy_hash), 64)
        self.assertEqual(len(self.plan.seccomp_hash), 64)
        self.assertEqual(len(self.plan.decoy_checksum), 64)
        text = self.plan.review_text()
        self.assertIn("policy_hash:", text)
        self.assertIn("seccomp_hash:", text)
        self.assertIn("decoy_checksum:", text)
        self.assertIn("--die-with-parent", text)
