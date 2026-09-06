# Testing

The suite is layered so that fast checks run everywhere and the slow, faithful
end-to-end demo runs only where the environment supports it.

## Layers

| Layer | Command | What it proves | Speed |
|-------|---------|----------------|-------|
| Go unit tests | `go test ./...` | Container format, self-image reconstruct (all 25 pairs, synthetic), arch mapping, teleport logic (fake SSH transport) | ms |
| Reproducibility + law | `make determinism` | Two builds are byte-identical; the *real native* binary reconstructs every target byte-for-byte (HR4) | ~20–40 s |
| QEMU user-mode matrix | `make qemu-user` | Every arch's real binary runs under emulation, reads its own blob, and satisfies the reconstruct law on an emulated CPU | ~1–2 min |
| QEMU system e2e | `make e2e` | Real SSH teleport installs onto a full foreign-arch guest; the installed guest-native binary runs and matches md5 | minutes |

## Prerequisites

- **Go** (pinned version — see `.github/workflows/ci.yml` `GO_VERSION`) and
  **uv** for the Python orchestration/tests.
- **QEMU user-mode** for the matrix: `qemu-user` (Debian) or `qemu-user-static`
  (Ubuntu/CI). The harness accepts both `qemu-<arch>` and `qemu-<arch>-static`.
- **QEMU system** for the e2e demo: `qemu-system-x86` / `qemu-system-misc` /
  `qemu-system-arm`, plus `cloud-image-utils` and a base cloud image. See
  [../emulation/system/README.md](../emulation/system/README.md).

Every emulation test **skips with a clear reason** when its emulator or image is
missing — the suite never silently drops coverage and never fails on an
unprovisioned host.

## Running everything locally

```bash
make unit            # go vet + go test ./...
make determinism     # reproducible build + executed reconstruct law
make qemu-user       # cross-arch execution matrix
make e2e             # full SSH teleport under system emulation (needs setup)
```

## In CI

`.github/workflows/ci.yml` runs four jobs:

1. **unit** — `go vet` + `go test`.
2. **determinism** — build all arches, prove byte-identical + reconstruct law,
   upload `dist/` as an artifact.
3. **qemu-user** — install `qemu-user-static`, run the cross-arch matrix.
4. **e2e-system** *(best-effort, non-blocking)* — boot a foreign-arch guest
   (arm64 on the amd64 runner) and run the SSH teleport demo. Marked
   `continue-on-error` and gated to `main`/manual runs because TCG boots are slow.

## Why the reconstruct law is checked three times

It is the core guarantee, so it is verified at three levels of realism:

1. **Synthetic, in-process** (`fatblob`): fast, exhaustive over all
   host→target pairs, proves the algorithm.
2. **Real binaries, executed under emulation** (`emulation/user/exec_qemu_user_test.go`):
   proves the actual compiled tool does it, on every emulated CPU.
3. **Reproducible-build identity** (`internal/fatbuild/reprobuild_test.go`): proves the bytes
   a build machine emits equal the bytes a running binary reconstructs.
