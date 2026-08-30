import struct
import unittest

from policy import load_policy
from seccomp import (
    AUDIT_ARCH_X86_64,
    BPF_ABS,
    BPF_JEQ,
    BPF_JMP,
    BPF_JSET,
    BPF_LD,
    BPF_RET,
    BPF_W,
    OFF_ARCH,
    OFF_NR,
    RET_EPERM,
    SECCOMP_RET_ALLOW,
    compile_seccomp,
    decode,
)
from syscalls import CLONE_NS_MASK, DENIED_I3, number
from pathlib import Path

INNER = Path(__file__).resolve().parents[2]


class SeccompTests(unittest.TestCase):
    def setUp(self):
        self.policy = load_policy(INNER / "policy.yaml")
        self.prog = compile_seccomp(self.policy)

    def test_i3_not_in_allow_list(self):
        allow = set(self.prog.allow)
        for name in DENIED_I3:
            self.assertNotIn(name, allow, name)

    def test_denied_table_includes_i3(self):
        deny = set(self.prog.deny)
        for name in DENIED_I3:
            self.assertIn(name, deny, name)

    def test_bpf_default_is_eperm(self):
        insns = self.prog.insns
        self.assertEqual(insns[-1].code, BPF_RET)
        self.assertEqual(insns[-1].k, RET_EPERM)
        # wrong-arch also EPERM
        self.assertEqual(insns[0].code, BPF_LD | BPF_W | BPF_ABS)
        self.assertEqual(insns[0].k, OFF_ARCH)
        self.assertEqual(insns[2].k, RET_EPERM)

    def test_arch_gate(self):
        insns = self.prog.insns
        self.assertEqual(insns[1].code, BPF_JMP | BPF_JEQ)
        self.assertEqual(insns[1].k, AUDIT_ARCH_X86_64)
        self.assertEqual(insns[1].jt, 1)
        self.assertEqual(insns[1].jf, 0)

    def test_chdir_not_allowed_in_bpf(self):
        chdir_nr = number("chdir")
        allow_nrs = set()
        insns = self.prog.insns
        for i, ins in enumerate(insns):
            if ins.code == (BPF_JMP | BPF_JEQ) and i + 1 < len(insns):
                nxt = insns[i + 1]
                if nxt.code == BPF_RET and nxt.k == SECCOMP_RET_ALLOW:
                    allow_nrs.add(ins.k)
        self.assertNotIn(chdir_nr, allow_nrs)
        for name in DENIED_I3:
            self.assertNotIn(number(name), allow_nrs, name)

    def test_clone_has_ns_mask(self):
        insns = self.prog.insns
        clone_nr = number("clone")
        found_mask = False
        for i, ins in enumerate(insns):
            if ins.code == (BPF_JMP | BPF_JEQ) and ins.k == clone_nr:
                # following: LD args0, JSET mask, RET EPERM, RET ALLOW
                jset = insns[i + 2]
                self.assertEqual(jset.code, BPF_JMP | BPF_JSET)
                self.assertEqual(jset.k, CLONE_NS_MASK)
                self.assertTrue(jset.k & 0x10000000)  # CLONE_NEWUSER
                found_mask = True
        self.assertTrue(found_mask)

    def test_bpf_roundtrip_length(self):
        bpf = self.prog.bpf
        self.assertEqual(len(bpf) % 8, 0)
        self.assertEqual(len(decode(bpf)), len(self.prog.insns))
        self.assertGreater(len(self.prog.insns), 10)

    def test_table_is_readable(self):
        text = self.prog.table_text
        self.assertIn("chdir", text)
        self.assertIn("ALLOW:", text)
        self.assertIn("DENY", text)
        self.assertNotRegex(text.split("ALLOW:")[1].split("DENY")[0], r"\bchdir\b")
