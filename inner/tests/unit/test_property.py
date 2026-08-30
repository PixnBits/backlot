import random
import tempfile
import unittest
from pathlib import Path

from bwrap import Runtime, bind_sources, build_plan
from policy import load_policy

INNER = Path(__file__).resolve().parents[2]

SENSITIVE = [
    "/opt/grok",
    "/opt/grok/secrets.env",
    "/root/.ssh",
    "/root/.ssh/id_rsa",
    "/home",
    "/home/user/.aws/credentials",
    "/etc/shadow",
    "/etc/ssh/ssh_host_rsa_key",
    "/var/run/secrets/kubernetes.io/serviceaccount/token",
    "/proc/1/root/opt/grok",
    "/proc/self/root/opt/grok",
]

NOISE = [
    "/workspace/file.txt",
    "/usr/bin/ls",
    "/usr/bin/cat",
    "/tmp/foo",
    "/var/log/syslog",
    "/etc/passwd",
    "/dev/null",
    "/opt/other",
    "/run/systemd",
    "/workspace/../opt/grok",
]


class PropertyTests(unittest.TestCase):
    def setUp(self):
        self.policy = load_policy(INNER / "policy.yaml")
        tmp = Path(tempfile.mkdtemp(prefix="backlot-prop-"))
        self.ws = tmp / "ws"
        self.decoy = tmp / "decoy"
        self.ws.mkdir()
        self.decoy.mkdir()
        (self.decoy / "CANARY.txt").write_text("CANARY\n")

    def test_random_paths_never_appear_as_bind_sources(self):
        rng = random.Random(0xB4C10)
        plan = build_plan(
            self.policy,
            Runtime(
                workspace_host=self.ws,
                decoy_host={"/opt/grok": self.decoy},
                command=["/usr/bin/ls"],
            ),
        )
        sources = bind_sources(plan.argv)
        pool = SENSITIVE + NOISE + list(self.policy.secret_paths)
        picks = [rng.choice(pool) for _ in range(50)]
        for path in picks:
            if self.policy.is_secret(path) or path in self.policy.secret_paths:
                for src in sources:
                    self.assertFalse(
                        src == path or src.startswith("/opt/grok"),
                        f"secret {path} leaked as source {src}",
                    )
                    self.assertNotEqual(src, "/opt/grok")
                    self.assertNotEqual(src, "/root/.ssh")
                    self.assertNotEqual(src, "/home")

    def test_mutated_argv_does_not_add_secret_binds(self):
        rng = random.Random(7)
        extras = [
            ["--", "/usr/bin/cat", "../opt/grok/secrets.env"],
            ["/usr/bin/ls", "-l", "/opt/grok"],
            ["/usr/bin/cat", "/workspace/../opt/grok/secrets.env"],
            ["/usr/bin/ls", "-a", "--", "/root/.ssh"],
        ]
        for command in extras:
            plan = build_plan(
                self.policy,
                Runtime(
                    workspace_host=self.ws,
                    decoy_host={"/opt/grok": self.decoy},
                    command=command,
                ),
            )
            for src in bind_sources(plan.argv):
                self.assertFalse(self.policy.is_secret(src), src)
            # command mutation must not inject --bind of secrets
            self.assertNotIn("--share-net", plan.argv)
            _ = rng.random()
