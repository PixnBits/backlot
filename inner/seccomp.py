"""Generate a classic BPF seccomp filter from policy.

Default action is ERRNO(EPERM). I3 names are never allowed. clone() is
allowed only when flags & CLONE_NS_MASK == 0. clone3 is denied outright
because the flags live in a struct.

The output is a packed array of struct sock_filter, which is what
bwrap --seccomp FD reads. No magic fds: the runner creates a memfd/temp
file and passes that number.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass

from policy import Policy
from syscalls import CLONE_NS_MASK, DENIED_I3_SET, number

# linux/filter.h
BPF_LD = 0x00
BPF_W = 0x00
BPF_ABS = 0x20
BPF_JMP = 0x05
BPF_JEQ = 0x10
BPF_JSET = 0x40
BPF_K = 0x00
BPF_RET = 0x06

SECCOMP_RET_ALLOW = 0x7FFF0000
SECCOMP_RET_ERRNO = 0x00050000
EPERM = 1
RET_EPERM = SECCOMP_RET_ERRNO | EPERM

AUDIT_ARCH_X86_64 = 0xC000003E
OFF_NR = 0
OFF_ARCH = 4
OFF_ARGS0 = 16  # low 32 bits of args[0] on LE


@dataclass(frozen=True)
class SockFilter:
    code: int
    jt: int = 0
    jf: int = 0
    k: int = 0

    def pack(self) -> bytes:
        return struct.pack("<HBBI", self.code, self.jt, self.jf, self.k)


def ld_abs(offset: int) -> SockFilter:
    return SockFilter(BPF_LD | BPF_W | BPF_ABS, k=offset)


def jmp_eq(k: int, jt: int, jf: int) -> SockFilter:
    return SockFilter(BPF_JMP | BPF_JEQ | BPF_K, jt=jt, jf=jf, k=k)


def jmp_set(k: int, jt: int, jf: int) -> SockFilter:
    return SockFilter(BPF_JMP | BPF_JSET | BPF_K, jt=jt, jf=jf, k=k)


def ret(k: int) -> SockFilter:
    return SockFilter(BPF_RET | BPF_K, k=k)


@dataclass
class SeccompProgram:
    insns: list[SockFilter]
    allow: tuple[str, ...]
    deny: tuple[str, ...]
    clone_mask: int

    @property
    def bpf(self) -> bytes:
        return b"".join(i.pack() for i in self.insns)

    @property
    def table_text(self) -> str:
        lines = [
            "# Backlot M1 seccomp table (x86_64)",
            "# default: ERRNO(EPERM)",
            f"# clone NS mask: 0x{self.clone_mask:08x} (any bit → EPERM)",
            "# clone3: denied (cannot filter flags)",
            "",
            "ALLOW:",
        ]
        for name in self.allow:
            note = "  [clone: deny CLONE_NEW*]" if name == "clone" else ""
            lines.append(f"  {number(name):4d}  {name}{note}")
        lines.append("")
        lines.append("DENY (explicit I3 + policy):")
        for name in self.deny:
            nr = number(name)
            lines.append(f"  {nr:4d}  {name}")
        lines.append("")
        lines.append(f"instruction_count: {len(self.insns)}")
        lines.append("return_allow: 0x7fff0000")
        lines.append(f"return_deny:  0x{RET_EPERM:08x}  ERRNO(EPERM)")
        return "\n".join(lines) + "\n"


def compile_seccomp(policy: Policy) -> SeccompProgram:
    allow = tuple(name for name in policy.allowed_syscalls if name not in DENIED_I3_SET)
    deny = tuple(sorted(set(policy.denied_syscalls) | DENIED_I3_SET))
    simple = [name for name in allow if name != "clone"]
    has_clone = "clone" in allow

    insns: list[SockFilter] = [
        ld_abs(OFF_ARCH),
        jmp_eq(AUDIT_ARCH_X86_64, 1, 0),
        ret(RET_EPERM),
        ld_abs(OFF_NR),
    ]

    # clone handler: 4 insns after the JEQ
    #   JEQ clone, 0, 4
    #   LD args0
    #   JSET mask, 0, 1   # bit set → EPERM; clear → ALLOW
    #   RET EPERM
    #   RET ALLOW
    if has_clone:
        insns.append(jmp_eq(number("clone"), 0, 4))
        insns.append(ld_abs(OFF_ARGS0))
        insns.append(jmp_set(CLONE_NS_MASK, 0, 1))
        insns.append(ret(RET_EPERM))
        insns.append(ret(SECCOMP_RET_ALLOW))

    for name in simple:
        insns.append(jmp_eq(number(name), 0, 1))
        insns.append(ret(SECCOMP_RET_ALLOW))

    insns.append(ret(RET_EPERM))
    return SeccompProgram(insns=insns, allow=allow, deny=deny, clone_mask=CLONE_NS_MASK)


def decode(bpf: bytes) -> list[SockFilter]:
    if len(bpf) % 8 != 0:
        raise ValueError("bpf length not a multiple of 8")
    out = []
    for i in range(0, len(bpf), 8):
        code, jt, jf, k = struct.unpack_from("<HBBI", bpf, i)
        out.append(SockFilter(code, jt, jf, k))
    return out


def allowed_numbers(program: SeccompProgram) -> set[int]:
    return {number(name) for name in program.allow}


def denied_numbers(program: SeccompProgram) -> set[int]:
    return {number(name) for name in program.deny}
