# Architecture

This document explains how `go-multi-binary` carries every architecture inside one
file and reconstructs any target byte-for-byte. For the full survey of approaches
considered (and why the alternatives were rejected), see
[research/multi-arch-binary-approaches.md](research/multi-arch-binary-approaches.md).

## The two facts everything rests on

1. **The Linux kernel refuses to run an ELF built for another architecture.**
   `load_elf_binary()` validates the ELF header and calls `elf_check_arch()`,
   which compares the header's `e_machine` field to the host CPU; a mismatch
   returns `ENOEXEC`. There is *no* single ELF the kernel will run on multiple
   architectures, and Linux has no native multi-arch container like macOS's
   Mach-O "universal binaries".

2. **The kernel ignores bytes appended after an ELF.** Program segments are
   mapped by offsets in the program header table; total file length is never
   checked. So we can staple a multi-architecture payload onto the end of a
   perfectly normal, natively-loadable ELF.

From these: the distributable for architecture *A* is a **native ELF for *A***
(loads directly, no shell, no loader tricks) with a **shared blob appended** that
contains every architecture's native binary.

## The canonical image

```
canonical(A)  =  native(A)   ++   FATBLOB
                 └─ runs ─┘        └─ trailing data the kernel ignores ─┘
```

- `native(A)` — the `go-teleport-self` program compiled for *A*, static
  (`CGO_ENABLED=0`), reproducible (`-trimpath`, no VCS stamp, empty build id).
- `FATBLOB` — identical in **every** `canonical(*)`. It holds all architectures'
  native binaries plus an index and a fixed-size EOF trailer.

```
FATBLOB = "FATBLOB\x01"                      magic
          u16 count
          index[count]:                       (32 bytes each)
              name[8]  status  _rsvd[7]  offset:u64  length:u64
          payload = native(386) ++ native(amd64) ++ … ++ native(riscv64)
          trailer = u64 total_length ++ "FATBLOBZ"     (16 bytes, at EOF)
```

The **fixed-size EOF trailer** is what lets a running program find its own
appended blob without knowing the size of the ELF in front of it: seek to
`EOF-16`, read `total_length`, and the blob begins at `filesize - total_length`.

## Reconstruction and the determinism law

A running `canonical(H)` reads its own bytes (`/proc/self/exe`), splits off the
`FATBLOB`, and can emit `canonical(T)` for any embedded target *T*:

```
Reconstruct(H → T) = native(T) ++ FATBLOB = canonical(T)
```

Because `FATBLOB` is byte-identical everywhere and `native(T)` is copied verbatim
out of it, the result is **bit-identical** to the `canonical(T)` a build machine
would produce. This is the project's core guarantee (HR4):

> `md5(go-teleport-self.riscv64` downloaded`)` **==**
> `md5(` what `go-teleport-self.arm64` produces when deploying to a riscv64 machine `)`

It is proven three ways: a Go unit test over all 25 host→target pairs
(`internal/fatblob`), an executed test on real binaries under QEMU user-mode
(`test/exec_qemu_user_test.py`), and the reproducible-build check
(`test/determinism_test.py`).

## Why this shape (vs. the alternatives)

- **A shell polyglot** (one file that is both `#!/bin/sh` and payloads, dispatched
  by `uname -m`) also works and makes all downloads identical, but it runs via the
  shell, not the kernel's ELF loader, and depends on `/bin/sh` + coreutils. We
  keep it as a possible enhancement, not the base design.
- **FatELF** (a real multi-arch ELF container) needs kernel + glibc patches that
  were never merged upstream — it cannot run on a stock kernel.
- **Cosmopolitan/APE** is amd64+arm64 only and requires its own libc, so it can't
  wrap arbitrary Go binaries or cover 386/arm/riscv.

See the research document for the confirmed references behind each rejection.

## Components

| Package / file | Responsibility |
|----------------|----------------|
| `internal/fatblob` | The container format: encode/decode, self-image split, reconstruct. Pure, heavily unit-tested. |
| `internal/archdetect` | Single source of truth for arches: ids, GOARCH/GOARM, ELF `e_machine`, `uname -m` mapping. |
| `internal/teleport` | SSH install: detect remote arch, reconstruct, upload, verify md5. Transport is an interface (fake in tests, `ssh` in production). |
| `cmd/go-teleport-self` | The shipped program: `info`/`list`/`verify`/`extract` and the `user@host` teleport action. |
| `cmd/fatpack` | Build-time assembler: bare natives → canonical artifacts + manifest. |
| `build.py` | Reproducible multi-arch build orchestrator. |
| `emulation/user`, `emulation/system` | QEMU user-mode (fast, per-arch) and system (full-guest SSH) test harnesses. |
