#!/usr/bin/env python3
"""Run a canonical go-teleport-self binary under QEMU user-mode emulation.

QEMU user-mode ("qemu-<arch>") runs a single foreign-architecture Linux binary
directly on the host by translating instructions and forwarding syscalls — no
guest kernel, no boot. This is the fast path for verifying that each arch's
binary executes and can read its own appended FATBLOB. (The full SSH teleport
demo uses system emulation instead; see emulation/system/.)

On a host where binfmt_misc is registered for QEMU (e.g. qemu-user-static), a
foreign binary can be exec'd directly and the kernel invokes the interpreter.
We instead invoke the interpreter explicitly, which is more portable across CI
environments and does not depend on binfmt registration.

Usage:
  python emulation/user/run.py <arch> [args...]
"""

import platform
import shutil
import subprocess
import sys

# arch id -> qemu user-mode interpreter binary
QEMU_INTERP = {
    "386": "qemu-i386",
    "amd64": "qemu-x86_64",
    "arm": "qemu-arm",
    "arm64": "qemu-aarch64",
    "riscv64": "qemu-riscv64",
    "riscv32": "qemu-riscv32",
}

_HOST_MAP = {
    "x86_64": "amd64", "amd64": "amd64",
    "i686": "386", "i386": "386",
    "armv7l": "arm", "armv6l": "arm",
    "aarch64": "arm64", "arm64": "arm64",
    "riscv64": "riscv64",
}


def host_arch() -> str:
    return _HOST_MAP.get(platform.machine().lower(), platform.machine().lower())


def interpreter_for(arch: str):
    """Return the argv prefix to run a binary of `arch`, or None if impossible.

    Native arch -> no prefix. Foreign arch -> the qemu interpreter if installed.
    Accepts both `qemu-<arch>` (Debian qemu-user) and `qemu-<arch>-static`
    (Debian/Ubuntu qemu-user-static, common in CI).
    """
    if arch == host_arch():
        return []
    base = QEMU_INTERP.get(arch)
    if not base:
        return None
    for name in (base, base + "-static"):
        if shutil.which(name):
            return [name]
    return None


def can_run(arch: str) -> bool:
    return interpreter_for(arch) is not None


def run(binary: str, arch: str, args, **kwargs) -> subprocess.CompletedProcess:
    prefix = interpreter_for(arch)
    if prefix is None:
        raise RuntimeError(f"cannot run {arch}: no native support and no qemu interpreter")
    return subprocess.run([*prefix, binary, *args], **kwargs)


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: run.py <arch> [args...]", file=sys.stderr)
        return 2
    arch = sys.argv[1]
    binary = f"dist/go-teleport-self.{arch}"
    return run(binary, arch, sys.argv[2:]).returncode


if __name__ == "__main__":
    sys.exit(main())
