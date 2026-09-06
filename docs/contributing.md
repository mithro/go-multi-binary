# Contributing

## Reproducibility rules (do not break these)

Bit-for-bit determinism is the project's core guarantee. Every shipped binary is
built with:

```
CGO_ENABLED=0 GOOS=linux GOARCH=<arch> [GOARM=6] \
  go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=<v>"
```

- **Never enable cgo** in the shipped binaries. cgo pulls in the host C toolchain
  and libc paths, breaking both reproducibility and glibc/musl portability.
- **Pin the Go toolchain.** The compiler version is an input to the output bytes;
  `.github/workflows/ci.yml` sets `GO_VERSION`. Changing it changes every md5.
- **Keep the version string an explicit input.** `cmd/fatbuild` injects
  `main.version` (default: `git describe`); the same input on any machine yields
  the same bytes.
- `SOURCE_DATE_EPOCH` is irrelevant — Go embeds no build timestamp.

Run `make determinism` before sending a change that touches the build or the
container format; it fails if two builds differ or the reconstruct law breaks.

## The reserved riscv32 slot

`riscv32` (VexRISCV/KIAN-V under LiteX) is listed in the arch table but marked
`Supported: false`. **No Go toolchain can build a riscv32 Linux userspace binary
today** (standard Go has no `linux/riscv32`; TinyGo's Linux support is x86/ARM
only; gccgo has plumbing but no shipped port — see the research doc §3). The blob
carries a reserved, empty index entry for it so the format is ready without a
change if that ever becomes possible, or if a non-Go (C/Rust/Zig) riscv32 slice
is dropped in. Do **not** mark it supported until a real slice can be produced.

## Adding an architecture

1. Add an entry to `archdetect.Table()` (id, `GOARCH`/`GOARM`, ELF
   `e_machine`, `uname -m` aliases, `Supported: true`) **and** to
   `fatblob.FixedArchOrder()` — the order is part of the canonical format, so
   append; do not reorder existing arches.
2. No separate build list to update — `cmd/fatbuild` builds every
   `archdetect.Table()` entry with `Supported: true`.
3. Add the arch to the `presentArches` lists in the Go tests and to
   `emulation/user/qemuuser.go`'s `qemuInterp` map.
4. Run `make unit determinism qemu-user`.

Reordering or changing the format constants (`Magic`, index layout, trailer)
changes every binary's bytes and breaks compatibility with already-distributed
artifacts — treat the format as append-only and versioned by `Magic`.

## Conventions

- **Go for everything**: the build orchestrator (`cmd/fatbuild`), the emulation
  harnesses (`emulation/user`, `emulation/system`), and the binary-format logic
  are all Go, unit-tested. There is no scripting-language runtime dependency and
  no external test-runner wiring — the Go toolchain is the only requirement.
- Temp files go in project-local `./tmp/` (git-ignored), never `/tmp`.
- Small, discrete commits; each ends with tests passing.
- ISO 8601 or day-first dates only.
- SSH tooling must keep `known_hosts` human-readable — never enable hostname
  hashing in SSH config or key tooling.

## Test before you push

```bash
make unit         # required
make determinism  # required for build/format changes
make qemu-user    # recommended (needs qemu-user)
```
