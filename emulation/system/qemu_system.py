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
        "net": "virtio-net-pci",
        "drive_if": "virtio",
    },
    "arm64": {
        "qemu": "qemu-system-aarch64",
        "image": "debian-12-arm64.qcow2",
        "machine": ["-machine", "virt", "-cpu", "max"],
        "net": "virtio-net-pci",
        "drive_if": "virtio",
        "uefi": True,
    },
    "riscv64": {
        "qemu": "qemu-system-riscv64",
        "image": "debian-13-riscv64.qcow2",
        "machine": ["-machine", "virt", "-cpu", "max"],
        "net": "virtio-net-device",
        "drive_if": "virtio",
        "bios": "default",
    },
}


# Candidate locations for aarch64 UEFI firmware across distros (Debian, Ubuntu,
# Fedora). The exact path varies by distro/package, so we search rather than
# hardcode one (the hardcoded path was why the CI arm64 job failed at startup).
AARCH64_FIRMWARE_CANDIDATES = [
    "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
    "/usr/share/AAVMF/AAVMF_CODE.fd",
    "/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd",
    "/usr/share/edk2/aarch64/QEMU_EFI.fd",
    "/usr/share/edk2/aarch64/QEMU_EFI-silent.fd",
    "/usr/share/qemu/edk2-aarch64-code.fd",
]


def find_uefi_firmware() -> str | None:
    for p in AARCH64_FIRMWARE_CANDIDATES:
        if os.path.exists(p):
            return p
    return None


_HOST_MAP = {
    "x86_64": "amd64", "amd64": "amd64",
    "i686": "386", "i386": "386",
    "armv7l": "arm", "armv6l": "arm",
    "aarch64": "arm64", "arm64": "arm64",
    "riscv64": "riscv64",
}


def host_arch() -> str:
    import platform

    return _HOST_MAP.get(platform.machine().lower(), platform.machine().lower())


def select_accel(arch: str) -> str:
    """Use KVM only when the guest arch matches the host arch AND /dev/kvm is
    actually usable. KVM cannot virtualize a foreign architecture (an aarch64
    guest on an x86 host must use TCG), and /dev/kvm can exist but be
    permission-denied — both were causes of QEMU failing to start.
    """
    if arch == host_arch() and os.access("/dev/kvm", os.R_OK | os.W_OK):
        return "kvm"
    return "tcg"


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
    if cfg.get("uefi") and find_uefi_firmware() is None:
        return False, "aarch64 UEFI firmware not found (install qemu-efi-aarch64)"
    return True, ""


def _free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def _wait_ssh(port: int, key: Path, timeout: float, proc=None) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if proc is not None and proc.poll() is not None:
            return False  # qemu died; caller inspects the log
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
        "-accel", select_accel(arch),
        "-drive", f"file={overlay},if={cfg['drive_if']},format=qcow2",
        "-drive", f"file={seed},if={cfg['drive_if']},format=raw",
        "-netdev", f"user,id=n0,hostfwd=tcp:127.0.0.1:{port}-:22",
        "-device", f"{cfg['net']},netdev=n0",
        "-nographic",
        "-serial", f"file:{console}",
        "-monitor", "none",
    ]
    if cfg.get("uefi"):
        fw = find_uefi_firmware()
        if fw is None:
            raise RuntimeError("aarch64 UEFI firmware not found (install qemu-efi-aarch64)")
        cmd += ["-bios", fw]

    # Capture QEMU's own stdout+stderr so startup failures are visible (never
    # discard to /dev/null — that hid the CI failure and wasted the full timeout).
    qemu_log = workdir / "qemu.log"
    qemu_out = open(qemu_log, "wb")
    proc = subprocess.Popen(cmd, stdout=qemu_out, stderr=subprocess.STDOUT)
    try:
        if not _wait_ssh(port, key, boot_timeout, proc):
            if proc.poll() is not None:
                log = qemu_log.read_text(errors="replace")[-2000:]
                raise RuntimeError(
                    f"guest {arch}: qemu exited early (code {proc.returncode})\n"
                    f"cmd: {' '.join(cmd)}\nqemu output:\n{log}"
                )
            tail = console.read_text()[-2000:] if console.exists() else "(no console output)"
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
        qemu_out.close()
