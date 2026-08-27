#!/usr/bin/env python3
"""Reproducible multi-architecture build orchestrator.

Builds the go-teleport-self program for every supported architecture with the
Go team's reproducible recipe (CGO_ENABLED=0, -trimpath, no VCS stamping,
stripped, empty build id), then invokes the `fatpack` tool to assemble the
shared FATBLOB and emit each canonical distributable plus a manifest.

Determinism inputs that MUST be held fixed for bit-identical output across
machines (see docs/research/multi-arch-binary-approaches.md §5.4):
  - the exact Go toolchain version,
  - the build flags below,
  - the injected version string (VERSION env, default: git describe).

Usage:
  python build.py [--out-root DIR] [--version STR]

Outputs under <out-root>/ (default: repo root):
  dist/native/native.<arch>        bare native binaries
  dist/go-teleport-self.<arch>     canonical distributables (native ++ blob)
  dist/MANIFEST.json               sizes + sha256 + md5
"""

import argparse
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent

# (arch id, GOARCH, GOARM). GOARM=6 is the baseline for old ARMv6 Pis.
SUPPORTED = [
    ("386", "386", None),
    ("amd64", "amd64", None),
    ("arm", "arm", "6"),
    ("arm64", "arm64", None),
    ("riscv64", "riscv64", None),
]


def git_describe() -> str:
    try:
        out = subprocess.run(
            ["git", "describe", "--tags", "--always", "--dirty"],
            cwd=REPO,
            capture_output=True,
            text=True,
            check=True,
        )
        return out.stdout.strip() or "dev"
    except subprocess.CalledProcessError:
        return "dev"


def build_native(arch: str, goarch: str, goarm, out_dir: Path, version: str) -> Path:
    out_dir.mkdir(parents=True, exist_ok=True)
    out_path = out_dir / f"native.{arch}"
    env = dict(os.environ)
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = "linux"
    env["GOARCH"] = goarch
    if goarm:
        env["GOARM"] = goarm
    else:
        env.pop("GOARM", None)
    ldflags = f"-s -w -buildid= -X main.version={version}"
    cmd = [
        "go", "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags", ldflags,
        "-o", str(out_path),
        "./cmd/go-teleport-self",
    ]
    print(f"[build] {arch:8} GOARCH={goarch} GOARM={goarm or '-'}", flush=True)
    subprocess.run(cmd, cwd=REPO, env=env, check=True)
    return out_path


def fatpack_assemble(native_dir: Path, out_dir: Path, manifest: Path) -> None:
    cmd = [
        "go", "run", "./cmd/fatpack", "assemble",
        "--in", str(native_dir),
        "--out", str(out_dir),
        "--manifest", str(manifest),
    ]
    print("[pack] assembling FATBLOB + canonical artifacts", flush=True)
    subprocess.run(cmd, cwd=REPO, check=True)


def md5_of(path: Path) -> str:
    return hashlib.md5(path.read_bytes()).hexdigest()


def main() -> int:
    ap = argparse.ArgumentParser(description="Reproducible multi-arch build")
    ap.add_argument("--out-root", default=str(REPO), help="root under which dist/ is written")
    ap.add_argument("--version", default=os.environ.get("VERSION", "") or git_describe())
    args = ap.parse_args()

    out_root = Path(args.out_root).resolve()
    dist = out_root / "dist"
    native_dir = dist / "native"
    manifest = dist / "MANIFEST.json"

    print(f"go: {subprocess.run(['go', 'version'], capture_output=True, text=True).stdout.strip()}")
    print(f"version: {args.version}")
    print(f"out: {dist}")

    for arch, goarch, goarm in SUPPORTED:
        build_native(arch, goarch, goarm, native_dir, args.version)

    fatpack_assemble(native_dir, dist, manifest)

    data = json.loads(manifest.read_text())
    print("\n=== MANIFEST ===")
    print(f"blob: {data['blob_size']} bytes  sha256:{data['blob_sha256'][:16]}")
    for a in data["artifacts"]:
        if a["present"]:
            print(f"  {a['arch']:8} {a['size']:>10} bytes  md5:{a['md5']}  sha256:{a['sha256'][:16]}")
        else:
            print(f"  {a['arch']:8} reserved (no binary)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
