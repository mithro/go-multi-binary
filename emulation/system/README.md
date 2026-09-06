# System-emulation harness (end-to-end teleport demo)

This directory boots a **full guest** of a chosen architecture under QEMU system
emulation, with a real SSH server, so `go-teleport-self` can install itself onto
a genuinely different machine over SSH — the faithful demonstration of the
project's goal.

## Why this exists (and how it differs from `emulation/user/`)

| | `emulation/user/` (QEMU user-mode) | `emulation/system/` (QEMU system) |
|---|---|---|
| What runs | one foreign binary, host kernel | full guest kernel + userland |
| Speed | fast (seconds) | slow (minutes under TCG) |
| Used for | per-arch execution + reconstruct-law smoke tests (CI matrix) | the end-to-end SSH teleport demo |
| SSH | n/a | real sshd in the guest |

The reconstruct/determinism law is proven cheaply and for **all** arches in
`emulation/user/`. This harness adds the last mile: a real SSH install onto a
separate machine whose CPU is native to the installed binary.

## Spike results — "test the options, use what works best"

The reference host is **arm64 Debian sid** with `/dev/kvm`, passwordless sudo,
QEMU 11.1, and `qemu-user` binfmt registered for all target arches.

| Option | Verdict | Notes |
|--------|---------|-------|
| **Docker + qemu-user-static** | ❌ rejected | Docker daemon socket permission denied (user not in `docker` group); not usable without elevating the account. |
| **QEMU user-mode** | ✅ used for the CI matrix | Every arch's binary runs and reads its own blob; proves the reconstruct law for all 25 host→target pairs. Not an SSH endpoint, so not the *install-onto-a-machine* demo. |
| **QEMU system, arm64 (KVM)** | ✅ works, but same-arch | Fast via KVM, but arm64 == host, so it doesn't show a *cross-architecture* install. Kept as an option in `qemu_system.py`. |
| **QEMU system, amd64 (TCG)** | ✅ **chosen for the live cross-arch demo** | x86_64 under TCG on an arm64 host: mature emulation, stock Debian cloud image, cloud-init SSH. Cross-arch (amd64 ≠ arm64) and reliable. |
| **QEMU system, riscv64 (TCG)** | ⚠️ optional | Config present; needs `qemu-system-misc` + a riscv64 cloud image. Slowest; enable when you want RISC-V system coverage. |

**Chosen default:** amd64 system emulation for the live end-to-end test, backed
by the all-arch reconstruct proof in `emulation/user/`.

## How it works

`qemu_system.py` provides a `guest(arch, workdir)` context manager that:

1. generates a throwaway SSH keypair,
2. builds a cloud-init seed (`cloud-localds`) creating a passwordless `tester`
   user with that key,
3. creates a copy-on-write overlay so the base image stays pristine,
4. boots `qemu-system-<arch>` with user networking and `hostfwd` mapping a free
   local port to the guest's port 22,
5. waits until SSH answers, then yields `{host, port, user, key, ssh_args}`,
6. tears the guest down on exit.

`test/teleport_e2e_test.py` uses it to run the real tool:
`go-teleport-self tester@127.0.0.1 -- <ssh args>`, then SSHes in to run the
freshly installed guest-native binary and checks it reports the target arch with
a matching md5.

## Prerequisites

```bash
sudo apt-get install -y qemu-system-x86 qemu-system-misc qemu-system-arm \
                        cloud-image-utils qemu-utils
# Base image (amd64 example):
mkdir -p emulation/system/images
curl -fsSL -o emulation/system/images/debian-12-amd64.qcow2 \
  https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2
```

Then:

```bash
make e2e          # or: go test -tags e2e ./emulation/system/
```

Images and overlays are git-ignored. The test **skips with a clear reason** when
the emulator or base image is absent, so it never blocks an unprovisioned host.
