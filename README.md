# go-multi-binary

Build a single Go binary that carries **every** supported CPU architecture inside it and can
**install itself onto a machine of a different architecture with zero network access** — the
running binary reconstructs, bit-for-bit, the exact artifact that architecture would have
downloaded.

## Why

The Linux kernel will not run one ELF on multiple architectures (`elf_check_arch` rejects a
mismatched `e_machine`), and Linux has no native multi-arch container like macOS's Mach-O
"universal binaries". So a truly portable, self-installing tool has to carry per-architecture
native binaries and pick/reconstruct the right one itself.

See the design exploration: **[docs/research/multi-arch-binary-approaches.md](docs/research/multi-arch-binary-approaches.md)** —
a comprehensive survey of every approach considered (FatELF, macOS `lipo`, Cosmopolitan/APE,
`binfmt_misc`, OCI multi-arch, shell polyglots, native-stub+fat-blob), with confirmed references
for why the rejected ones cannot meet the hard requirements.

## Supported architectures

| Target | GOARCH | Status |
|--------|--------|--------|
| x86 32-bit | `386` | ✅ |
| x86 64-bit | `amd64` | ✅ |
| arm32 (old RPi) | `arm` (GOARM=6) | ✅ |
| arm64 (RPi 4/5, servers) | `arm64` | ✅ |
| riscv64 (HiFive) | `riscv64` | ✅ |
| riscv32 (VexRISCV/LiteX) | — | ⚠️ Not supported by any Go toolchain today — reserved slot; see research doc §3/§7 |

## Hard requirements

1. Runs on stock Linux (no kernel patches, no root per-machine setup) — RedHat, Debian, Ubuntu,
   Gentoo, Arch, OpenWrt.
2. Works with both glibc and musl (achieved via `CGO_ENABLED=0` static builds).
3. Self-contained: carries every arch; deployment downloads nothing.
4. Bit-for-bit deterministic: `md5(go-binary.riscv64 downloaded)` ==
   `md5(what go-binary.arm64 produces when deploying to a riscv64 machine)`.

## Quickstart

```bash
make build                       # reproducible dist/go-teleport-self.<arch> for all 5 arches
./dist/go-teleport-self.arm64 info
go-teleport-self user@host       # install self onto a different-arch machine over SSH
```

## Documentation

- [docs/architecture.md](docs/architecture.md) — how the fat binary + reconstruct law work
- [docs/usage.md](docs/usage.md) — build, inspect, extract, teleport
- [docs/testing.md](docs/testing.md) — the layered test suite (unit → emulation → e2e)
- [docs/contributing.md](docs/contributing.md) — reproducibility rules, adding an arch
- [docs/research/multi-arch-binary-approaches.md](docs/research/multi-arch-binary-approaches.md) — approaches considered, with references
- [emulation/system/README.md](emulation/system/README.md) — QEMU system-emulation harness

## Status

Core implemented and tested: reproducible builds for all 5 Go-supported arches,
the container format + reconstruct law (proven in Go, under QEMU user-mode, and
via reproducible-build identity), the `go-teleport-self` CLI + SSH teleport, and
QEMU user/system emulation harnesses with CI. riscv32 is a reserved slot.

## License

Apache-2.0 — see [LICENSE](LICENSE).
