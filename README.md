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

## Status

Early development. Research/design phase complete; implementation in progress.

## License

Apache-2.0 — see [LICENSE](LICENSE).
