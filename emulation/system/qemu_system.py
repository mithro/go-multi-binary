#!/usr/bin/env python3
"""QEMU *system* emulation harness: boot a full guest of a chosen architecture
with a real SSH server, for the end-to-end teleport demo.

Unlike QEMU user-mode (emulation/user/run.py, which runs a single foreign binary
with a shared host kernel), system emulation boots a complete guest kernel and
userland. A binary installed into that guest by go-teleport-self is native to
the guest's architecture and runs there directly — exactly the real-world
"install onto a different machine" scenario.

Reliability notes (from the spike; see README.md):
  - amd64 (x86_64) system emulation via TCG is the most reliable cross-arch
    target from an arm64 host: mature emulation, stock Debian cloud image,
    cloud-init SSH setup.
  - arm64 guests can use KVM on an arm64 host (fast) but are same-arch.
  - Docker + binfmt was NOT usable in the reference environment (daemon
    permission denied), so this harness does not depend on Docker.

Requires: qemu-system-<arch>, cloud-image-utils (cloud-localds), a base cloud
image under images/. The e2e test skips (with a reason) when these are absent.
"""

import contextlib
import os
import shutil
import socket
import subprocess
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
IMAGES = HERE / "images"

# Per-arch configuration. `image` is the base cloud image filename under images/.
ARCH_CONFIG = {
    "amd64": {
        "qemu": "qemu-system-x86_64",
        "image": "debian-12-amd64.qcow2",
        "machine": ["-machine", "q35", "-cpu", "max"],
        "accel": "tcg",  # no KVM for x86 on an arm64 host
        "net": "virtio-net-pci",
        "drive_if": "virtio",
    },
    "arm64": {
        "qemu": "qemu-system-aarch64",
        "image": "debian-12-arm64.qcow2",
        "machine": ["-machine", "virt", "-cpu", "max"],
        "accel": "kvm" if os.path.exists("/dev/kvm") else "tcg",
        "net": "virtio-net-pci",
        "drive_if": "virtio",
        "uefi": True,
    },
    "riscv64": {
        "qemu": "qemu-system-riscv64",
        "image": "debian-13-riscv64.qcow2",
        "machine": ["-machine", "virt", "-cpu", "max"],
        "accel": "tcg",
        "net": "virtio-net-device",
        "drive_if": "virtio",
        "bios": "default",
    },
}


def available(arch: str) -> tuple[bool, str]:
    cfg = ARCH_CONFIG.get(arch)
    if not cfg:
        return False, f"no config for arch {arch}"
    if not shutil.which(cfg["qemu"]):
        return False, f"{cfg['qemu']} not installed"
    if not shutil.which("cloud-localds"):
        return False, "cloud-localds (cloud-image-utils) not installed"
    if not (IMAGES / cfg["image"]).exists():
        return False, f"base image {cfg['image']} not present in {IMAGES}"
    return True, ""


def _free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def _wait_ssh(port: int, key: Path, timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=3):
                pass
        except OSError:
            time.sleep(2)
            continue
        # Port open: try an actual ssh command.
        r = subprocess.run(
            ["ssh", "-p", str(port), "-i", str(key),
             "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
             "-o", f"UserKnownHostsFile={key.parent / 'known_hosts'}",
             "-o", "ConnectTimeout=5",
             "tester@127.0.0.1", "true"],
            capture_output=True, text=True,
        )
        if r.returncode == 0:
            return True
        time.sleep(3)
    return False


@contextlib.contextmanager
def guest(arch: str, workdir: Path, boot_timeout: float = 900.0):
    """Context manager that boots a guest and yields a dict with ssh details:
    {host, port, user, key, ssh_args (list for go-teleport-self)}."""
    ok, why = available(arch)
    if not ok:
        raise RuntimeError(why)
    cfg = ARCH_CONFIG[arch]
    workdir.mkdir(parents=True, exist_ok=True)

    # SSH keypair for the guest.
    key = workdir / "id_ed25519"
    subprocess.run(["ssh-keygen", "-t", "ed25519", "-N", "", "-f", str(key), "-q"], check=True)
    pub = (workdir / "id_ed25519.pub").read_text().strip()

    # cloud-init seed: create a passwordless 'tester' with our key.
    (workdir / "user-data").write_text(
        "#cloud-config\n"
        "users:\n"
        "  - name: tester\n"
        "    sudo: ALL=(ALL) NOPASSWD:ALL\n"
        "    shell: /bin/bash\n"
        "    ssh_authorized_keys:\n"
        f"      - {pub}\n"
        "ssh_pwauth: false\n"
    )
    (workdir / "meta-data").write_text("instance-id: teleport-e2e\nlocal-hostname: teleport-e2e\n")
    seed = workdir / "seed.img"
    subprocess.run(["cloud-localds", str(seed), str(workdir / "user-data"), str(workdir / "meta-data")], check=True)

    # Copy-on-write overlay so the base image stays pristine.
    overlay = workdir / "overlay.qcow2"
    base = IMAGES / cfg["image"]
    subprocess.run(["qemu-img", "create", "-f", "qcow2", "-b", str(base), "-F", "qcow2", str(overlay)], check=True)

    port = _free_port()
    console = workdir / "console.log"
    cmd = [
        cfg["qemu"],
        "-m", "1024", "-smp", "2",
        *cfg["machine"],
        "-accel", cfg["accel"],
        "-drive", f"file={overlay},if={cfg['drive_if']},format=qcow2",
        "-drive", f"file={seed},if={cfg['drive_if']},format=raw",
        "-netdev", f"user,id=n0,hostfwd=tcp:127.0.0.1:{port}-:22",
        "-device", f"{cfg['net']},netdev=n0",
        "-nographic",
        "-serial", f"file:{console}",
        "-monitor", "none",
    ]
    if cfg.get("uefi"):
        cmd += ["-bios", "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"]

    proc = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
    try:
        if not _wait_ssh(port, key, boot_timeout):
            tail = console.read_text()[-2000:] if console.exists() else "(no console)"
            raise RuntimeError(f"guest {arch} did not become SSH-ready in {boot_timeout}s\n{tail}")
        yield {
            "host": "127.0.0.1",
            "port": port,
            "user": "tester",
            "key": str(key),
            "ssh_args": [
                "-p", str(port),
                "-i", str(key),
                "-o", "StrictHostKeyChecking=no",
                "-o", f"UserKnownHostsFile={workdir / 'known_hosts'}",
            ],
        }
    finally:
        proc.terminate()
        with contextlib.suppress(subprocess.TimeoutExpired):
            proc.wait(timeout=15)
        if proc.poll() is None:
            proc.kill()
