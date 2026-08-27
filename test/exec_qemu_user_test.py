"""Cross-architecture execution smoke tests under QEMU user-mode.

For every present architecture we run the real canonical binary under emulation
and assert:
  - `info` reports the correct running arch and reads its own FATBLOB,
  - the executed reconstruct law holds under emulation: running
    canonical(host) `extract <target>` yields bytes byte-identical to
    canonical(target).

Architectures with no available emulator are skipped with a clear reason
(never silently dropped).

Run: uv run --with pytest python -m pytest test/exec_qemu_user_test.py -v
"""

import hashlib
import importlib.util
import subprocess
import sys
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parent.parent
PRESENT_ARCHES = ["386", "amd64", "arm", "arm64", "riscv64"]

# Load emulation/user/run.py as a module.
_spec = importlib.util.spec_from_file_location("qemu_user_run", REPO / "emulation" / "user" / "run.py")
qemu = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(qemu)


def md5_bytes(b: bytes) -> str:
    return hashlib.md5(b).hexdigest()


@pytest.fixture(scope="module")
def dist_dir(tmp_path_factory):
    out_root = tmp_path_factory.mktemp("build")
    subprocess.run([sys.executable, str(REPO / "build.py"), "--out-root", str(out_root)],
                   cwd=REPO, check=True)
    return out_root / "dist"


@pytest.mark.parametrize("arch", PRESENT_ARCHES)
def test_info_reports_arch(dist_dir, arch):
    if not qemu.can_run(arch):
        pytest.skip(f"no emulator available for {arch}")
    binary = str(dist_dir / f"go-teleport-self.{arch}")
    res = qemu.run(binary, arch, ["info"], capture_output=True, text=True, check=True)
    assert f"running arch:   {arch}" in res.stdout, res.stdout
    # Reads its own blob: all present arches listed.
    for other in PRESENT_ARCHES:
        assert f"{other:8}" in res.stdout or other in res.stdout


@pytest.mark.parametrize("host", PRESENT_ARCHES)
def test_executed_reconstruct_law_emulated(dist_dir, host, tmp_path):
    """Running canonical(host) under emulation must reconstruct every target
    byte-for-byte (HR4, executed on an emulated CPU)."""
    if not qemu.can_run(host):
        pytest.skip(f"no emulator available for {host}")
    host_bin = str(dist_dir / f"go-teleport-self.{host}")
    for target in PRESENT_ARCHES:
        out = tmp_path / f"{host}-to-{target}"
        qemu.run(host_bin, host, ["extract", target, str(out)], check=True)
        want = (dist_dir / f"go-teleport-self.{target}").read_bytes()
        got = out.read_bytes()
        assert md5_bytes(got) == md5_bytes(want), (
            f"emulated reconstruct({host}->{target}) differs from canonical({target})"
        )
