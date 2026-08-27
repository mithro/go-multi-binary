"""Determinism and container-invariant tests over REAL built binaries.

These drive build.py to produce dist artifacts, then assert:

1. Reproducibility (HR4 foundation): building twice yields byte-identical
   canonical artifacts for every architecture.
2. Shared-blob invariant: every canonical(arch) carries the *identical*
   trailing FATBLOB (they differ only in the leading native prefix).
3. Executed reconstruct law (HR4): the real, native tool reconstructs
   canonical(target) byte-for-byte for every target, matching the downloaded
   canonical(target). Executed natively on the host arch here; other host
   arches are exercised under emulation in the QEMU tests.

Run: uv run python -m pytest test/determinism_test.py -v
"""

import hashlib
import struct
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
PRESENT_ARCHES = ["386", "amd64", "arm", "arm64", "riscv64"]
TRAILER_MAGIC = b"FATBLOBZ"


def md5(path: Path) -> str:
    return hashlib.md5(path.read_bytes()).hexdigest()


def run_build(out_root: Path) -> None:
    out_root.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        [sys.executable, str(REPO / "build.py"), "--out-root", str(out_root)],
        cwd=REPO,
        check=True,
    )


def extract_trailing_blob(path: Path) -> bytes:
    data = path.read_bytes()
    assert data[-8:] == TRAILER_MAGIC, f"{path} missing FATBLOB trailer"
    (blob_len,) = struct.unpack("<Q", data[-16:-8])
    return data[-blob_len:]


def test_reproducible_builds(tmp_path):
    a = tmp_path / "a"
    b = tmp_path / "b"
    run_build(a)
    run_build(b)
    for arch in PRESENT_ARCHES:
        fa = a / "dist" / f"go-teleport-self.{arch}"
        fb = b / "dist" / f"go-teleport-self.{arch}"
        assert fa.exists() and fb.exists(), f"missing artifact for {arch}"
        assert md5(fa) == md5(fb), f"{arch} not reproducible: {md5(fa)} != {md5(fb)}"


def test_shared_blob_identical(tmp_path):
    run_build(tmp_path)
    dist = tmp_path / "dist"
    blobs = {a: extract_trailing_blob(dist / f"go-teleport-self.{a}") for a in PRESENT_ARCHES}
    ref = blobs["amd64"]
    for arch, blob in blobs.items():
        assert blob == ref, f"blob for {arch} differs from amd64's blob"


def test_executed_reconstruct_law_native(tmp_path):
    """The real native tool must reconstruct every target byte-for-byte."""
    run_build(tmp_path)
    dist = tmp_path / "dist"

    # Pick the canonical whose arch matches this host, so we can execute it.
    host_arch = _host_arch()
    if host_arch not in PRESENT_ARCHES:
        import pytest

        pytest.skip(f"host arch {host_arch} not in present arches")

    host_bin = dist / f"go-teleport-self.{host_arch}"
    for target in PRESENT_ARCHES:
        out = tmp_path / f"reconstructed.{target}"
        subprocess.run([str(host_bin), "extract", target, str(out)], check=True)
        want = dist / f"go-teleport-self.{target}"
        assert md5(out) == md5(want), (
            f"executed reconstruct({host_arch}->{target}) md5 {md5(out)} "
            f"!= canonical({target}) md5 {md5(want)}"
        )


def _host_arch() -> str:
    import platform

    m = platform.machine().lower()
    mapping = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "i686": "386",
        "i386": "386",
        "armv7l": "arm",
        "armv6l": "arm",
        "aarch64": "arm64",
        "arm64": "arm64",
        "riscv64": "riscv64",
    }
    return mapping.get(m, m)
