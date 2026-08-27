"""End-to-end teleport demo under QEMU *system* emulation.

Boots a full guest of a target architecture (different from the host), then runs
the REAL go-teleport-self tool to install itself into the guest's ~/local/bin
over real SSH — downloading nothing — and verifies that the freshly installed,
guest-native binary runs and reports the target architecture with a matching md5.

This is the faithful "install onto a machine of a different architecture"
scenario. It is skipped (with a reason) when the required emulator or base image
is not present, so the suite still passes on an unprovisioned host.

Run: uv run --with pytest python -m pytest test/teleport_e2e_test.py -v -s
"""

import hashlib
import importlib.util
import os
import subprocess
import sys
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parent.parent

_spec = importlib.util.spec_from_file_location(
    "qemu_system", REPO / "emulation" / "system" / "qemu_system.py"
)
qsys = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(qsys)

# Target arch to teleport into. Defaults to amd64 (the most reliable cross-arch
# system-emulation target from an arm64 host); override with TELEPORT_E2E_ARCH
# (e.g. arm64 when the CI runner is amd64). See emulation/system/README.md.
TARGET_ARCH = os.environ.get("TELEPORT_E2E_ARCH", "amd64")


def md5_file(p: Path) -> str:
    return hashlib.md5(p.read_bytes()).hexdigest()


def host_native_binary(dist: Path) -> Path:
    import platform

    m = platform.machine().lower()
    mp = {"x86_64": "amd64", "aarch64": "arm64", "armv7l": "arm", "armv6l": "arm",
          "i686": "386", "i386": "386", "riscv64": "riscv64"}
    arch = mp.get(m, m)
    return dist / f"go-teleport-self.{arch}"


@pytest.fixture(scope="module")
def dist(tmp_path_factory):
    out = tmp_path_factory.mktemp("build")
    subprocess.run([sys.executable, str(REPO / "build.py"), "--out-root", str(out)],
                   cwd=REPO, check=True)
    return out / "dist"


def test_teleport_installs_and_runs_on_foreign_guest(dist, tmp_path):
    ok, why = qsys.available(TARGET_ARCH)
    if not ok:
        pytest.skip(f"system emulation for {TARGET_ARCH} unavailable: {why}")

    tool = host_native_binary(dist)
    assert tool.exists(), f"host-native tool missing: {tool}"

    boot_timeout = float(os.environ.get("TELEPORT_E2E_BOOT_TIMEOUT", "900"))
    with qsys.guest(TARGET_ARCH, tmp_path / "guest", boot_timeout=boot_timeout) as g:
        target = f"{g['user']}@{g['host']}"

        # 1. Teleport: real SSH, reconstruct canonical(amd64), install to ~/local/bin.
        res = subprocess.run(
            [str(tool), target, "--", *g["ssh_args"]],
            capture_output=True, text=True,
        )
        assert res.returncode == 0, f"teleport failed:\n{res.stdout}\n{res.stderr}"
        print("teleport output:", res.stdout.strip())

        # 2. Run the freshly installed, guest-native binary over SSH.
        info = subprocess.run(
            ["ssh", *g["ssh_args"], target, "~/local/bin/go-teleport-self info"],
            capture_output=True, text=True,
        )
        assert info.returncode == 0, f"remote info failed:\n{info.stdout}\n{info.stderr}"
        print("remote info:\n", info.stdout)
        assert f"running arch:   {TARGET_ARCH}" in info.stdout

        # 3. The installed bytes must equal canonical(TARGET_ARCH) exactly (HR4).
        remote_md5 = subprocess.run(
            ["ssh", *g["ssh_args"], target, "md5sum ~/local/bin/go-teleport-self"],
            capture_output=True, text=True, check=True,
        ).stdout.split()[0]
        want = md5_file(dist / f"go-teleport-self.{TARGET_ARCH}")
        assert remote_md5 == want, f"installed md5 {remote_md5} != canonical({TARGET_ARCH}) {want}"
