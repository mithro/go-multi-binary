# Multi-Architecture "Fat" Go Binaries on Linux — Approaches Considered

**Status:** Research / design-exploration document
**Date:** 2026-08-27
**Scope:** Linux only (x86 32/64, arm32, arm64, riscv32, riscv64). Windows/macOS/BSD explicitly out of scope.
**Author:** Initial research pass (references verified against live pages on 2026-08-27).

---

## 1. Purpose and hard requirements

We want a single Go program (e.g. `go-claude-teleport`) that can **install itself onto a
machine of a *different* CPU architecture without downloading anything from the internet**.
All architecture variants must be carried *inside* the distributed artifact.

The following are treated as **hard requirements** (HR). An approach that cannot meet a hard
requirement is *rejected*, and this document must cite a **confirmed, quotable reference**
proving the requirement is genuinely impossible for that approach (not merely more work).

| ID | Hard requirement |
|----|------------------|
| **HR1** | Runs on stock Linux — no custom kernel, no root-only per-machine configuration, no patched libc. Must work on current stable + latest RedHat, Debian, Ubuntu, Gentoo, Arch, OpenWrt. |
| **HR2** | Works with **both glibc and musl**, across a wide range of kernel versions. |
| **HR3** | Self-contained: the artifact carries every target architecture; deployment to another arch downloads nothing. |
| **HR4** | **Bit-for-bit deterministic.** The artifact for arch *T* produced by a build machine must be byte-identical (same md5) to the artifact for arch *T* reconstructed by a running binary of *any* arch *H* when it deploys to *T*. |
| **HR5** | Target architectures: x86-32, x86-64, arm32, arm64, **riscv32**, riscv64. |

**Soft goals:** small-ish artifact, no temp-file writes if avoidable, no shell dependency if
avoidable, easy to reason about, testable under emulation in CI and locally.

---

## 2. The two governing facts

Everything below follows from two facts about how Linux runs programs.

### 2.1 The kernel refuses to run an ELF built for a different architecture

When you `execve()` an ELF, the kernel's ELF loader validates the header **before** it will
map and run the file. In `fs/binfmt_elf.c`, `load_elf_binary()` does:

```c
/* First of all, some simple consistency checks */
if (memcmp(elf_ex->e_ident, ELFMAG, SELFMAG) != 0)
        goto out;
if (elf_ex->e_type != ET_EXEC && elf_ex->e_type != ET_DYN)
        goto out;
if (!elf_check_arch(elf_ex))
        goto out;
```
— <https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/fs/binfmt_elf.c>

`elf_check_arch()` is a per-architecture comparison of the ELF header's `e_machine` field
against the host CPU. For example, on arm64:

```c
/* This is used to ensure we don't load something for the wrong architecture. */
#define elf_check_arch(x)  ((x)->e_machine == EM_AARCH64)
```
— <https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/arch/arm64/include/asm/elf.h>

The `e_machine` values are fixed by the ELF ABI: `EM_386 = 3`, `EM_ARM = 40`,
`EM_X86_64 = 62`, `EM_AARCH64 = 183`, `EM_RISCV = 243`
(<https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/include/uapi/linux/elf-em.h>;
gABI spec <https://www.sco.com/developers/gabi/latest/ch4.eheader.html>). A mismatch returns
`-ENOEXEC` ("Exec format error").

> **Consequence:** There is *no* single ELF file that the Linux kernel loader will run on more
> than one CPU architecture. Any "fat binary" that must be launched by the kernel's ELF loader
> is impossible on stock Linux. This is the root reason the winning design uses a per-arch
> native ELF plus appended data, or a shell polyglot.

### 2.2 The kernel ignores bytes appended *after* an ELF

The loader maps program segments by the offsets in the program header table; it never checks
the file's total length. Arbitrary trailing data is ignored.

- FatELF's author states it plainly: *"The end of the file isn't touched, so you can still do
  things like self-extracting .zip files for multiple architectures…"* — <https://icculus.org/fatelf/>
- Empirically confirmed here: appending 100 KB of random bytes to a working arm64 Go binary
  left it running normally (see §8 proof-of-concept).

> **Consequence:** We can carry a multi-architecture payload as trailing data after a normal,
> natively-loadable ELF. The running program reads *itself* to recover that payload.

macOS is different only because Mach-O has a native multi-arch container (`fat_header` /
`fat_arch`, magic `0xcafebabe`) that the loader understands and `lipo` assembles
(<https://developer.apple.com/documentation/apple-silicon/building-a-universal-macos-binary>,
Apple xnu `EXTERNAL_HEADERS/mach-o/fat.h>`). **Linux ELF has no equivalent.**

---

## 3. The riscv32 problem (HR5 partial-impossibility)

**Producing a riscv32 Linux-userspace binary with Go is not feasible today.** This is a genuine
conflict with HR5, documented here with confirmed references.

- **Standard `gc` Go has no `linux/riscv32` port.** The authoritative platform list contains
  only `riscv64`. Verified locally on this machine (Go 1.26.7):
  `go tool dist list | grep riscv` → `linux/riscv64` (and `openbsd/riscv64`) only. In the Go
  source, `internal/platform/zosarch.go` lists `{"linux", "riscv64"}` and no 32-bit riscv.
  The install docs list only *"`riscv64` The 64-bit RISC-V instruction set."*
  — <https://go.dev/doc/install/source>
- **The feature request was closed unresolved.** golang/go#51067 *"all: support for RISC-V 32
  bit"* is **closed** (timed out in `WaitingForInfo`). Maintainer ALTree:
  *"riscv32 is indeed not supported. Turning this issue into a feature request for it. There's
  is no timeline, it'll start existing when someone does the work : )"*
  — <https://github.com/golang/go/issues/51067>
- **TinyGo cannot target riscv32 Linux userspace.** TinyGo's Linux support is explicitly
  *"both 32-bit and 64-bit, on both x86 and ARM architectures"* — RISC-V is not listed
  (<https://tinygo.org/docs/guides/linux/>). Its riscv32 targets are **bare-metal**
  (`"llvm-target": "riscv32-unknown-none"`,
  <https://github.com/tinygo-org/tinygo/blob/release/targets/riscv32.json>), and generic rv32
  support is still an open issue (<https://github.com/tinygo-org/tinygo/issues/4192>).
  TinyGo also lacks full stdlib/`os/exec`/`recover` needed by a self-installing CLI
  (<https://tinygo.org/docs/reference/lang-support/>).
- **gccgo is the only theoretical path, and it is unproven.** libgo's configure *does* define
  a 32-bit `GOARCH=riscv` (`riscv*-*-*` → `GOARCH=riscv` when `__riscv_xlen != 64`,
  <https://github.com/gcc-mirror/gcc/blob/master/libgo/configure.ac>), but no distro ships a
  `riscv32-linux-gnu` gccgo, and there is no known tested port. Treat as DIY/unproven.

**Recommendation for riscv32:** see §7. In short — ship the design for the 5 Go-supported
arches now; treat riscv32 as a **pluggable non-Go slot** (a C/Rust/Zig helper, or a future
gccgo/Go port) so the container format reserves space for it without blocking the rest.

---

## 4. Options considered

Each option below has a verdict: **Rejected** (cannot meet a hard requirement — with proof),
**Viable** (meets the hard requirements), or **Building block** (a technique used by a viable
option, not a whole solution).

### 4.1 Native Linux "fat ELF" (FatELF) — **Rejected (HR1)**

A real project (Ryan Gordon / icculus, ~2009) that added a multi-arch container to ELF:
*"FatELF is a file format that embeds multiple ELF binaries for different architectures into
one file. This is the Linux equivalent of what Mac OS X calls 'Universal Binaries.'"*
— <https://icculus.org/fatelf/>

It cannot run on stock Linux: *"FatELF requires patches to various pieces of a GNU/Linux
system."* (same page). The kernel patch was maintained out-of-tree and **never merged**
(*"We maintain these patches in the Mercurial repository until they have been merged into the
upstream project."*). The author halted the effort after kernel-maintainer rejection:
*"It looks like the Linux kernel maintainers are frowning on the FatELF patches. … I think
I'll declare FatELF done for now."* — <https://icculus.org/finger/icculus?date=2009-11-03>.
Wikipedia: *"As of 2021, FatELF has not been integrated into the mainline Linux kernel."*
— <https://en.wikipedia.org/wiki/Fat_binary>. Shared-library support additionally needed glibc
patches that were only *"in progress"*.

> **Proof of impossibility for HR1:** running a FatELF binary requires a patched kernel (and,
> for shared libs, patched glibc). Those patches were never merged upstream, so no stock
> RedHat/Debian/Ubuntu/Gentoo/Arch/OpenWrt kernel can execute a FatELF file. Rejected.

### 4.2 macOS-style universal binary via `lipo` — **Rejected (HR1)**

macOS universal binaries work because the **Mach-O** format and loader natively support a
multi-arch container: *"A universal binary … contains executable code for both architectures"*
and *"the system prefers to execute the slice that is native to the current platform"*
(<https://developer.apple.com/documentation/apple-silicon/building-a-universal-macos-binary>).
`lipo` *"create[s] or operate[s] on 'universal' (multi-architecture) files"*
(<https://keith.github.io/xcode-man-pages/lipo.1.html>).

> **Proof of impossibility for HR1:** this relies on the Mach-O `fat_header`/`fat_arch`
> container understood by the *macOS* loader. Linux uses ELF, whose loader has no such
> container (§2.1) and rejects it. `lipo` output is not a Linux executable. Rejected for Linux.

### 4.3 Cosmopolitan libc / APE (Actually Portable Executable) — **Rejected (HR5, and Go-incompat)**

APE is the most impressive shipping polyglot: one file that runs on many OSes by being both a
PE header and a shell script (*"Actually Portable Executable (APE) is an executable file format
that polyglots the Windows Portable Executable (PE) format with a UNIX Sixth Edition style
shell script that doesn't have a shebang."* — <https://github.com/jart/cosmopolitan/blob/master/ape/specification.md>).
It even does `uname -m` fat dispatch internally
(<https://github.com/jart/cosmopolitan/blob/master/tool/build/apelink.c>).

But its architecture support is **AMD64 and ARM64 only** — the APE spec's *"Supported OSes and
Architectures"* section lists exactly those two, with **no i386, arm32, or riscv**. The cosmocc
README: *"This toolchain can be used to compile executables that run on … the x86_64 and
AARCH64 architectures."* (<https://github.com/jart/cosmopolitan/blob/master/tool/cosmocc/README.md>).
Additionally, Cosmopolitan requires **its own libc**; you cannot wrap an arbitrary Go binary in it.

> **Proof of impossibility for HR5:** APE supports only amd64 + arm64 per its own
> specification, so it cannot cover x86-32, arm32, riscv32, or riscv64. And it requires
> compiling against cosmopolitan libc, which is incompatible with the Go toolchain/runtime.
> Rejected. *(The polyglot shell-stub *technique* it pioneered is reused — see §4.6.)*

### 4.4 `binfmt_misc` interpreter registration — **Rejected (HR1, HR3)**

You can register a userspace "interpreter" for foreign binaries via
`/proc/sys/fs/binfmt_misc/register` (this is exactly how QEMU-user auto-runs foreign ELFs).

> **Proof of impossibility for HR1/HR3:** registration is a **root-only, per-machine** kernel
> configuration step, and it presupposes an interpreter (e.g. `qemu-<arch>`) is already
> installed on the target. That is downloading/installing external software and per-machine
> root setup — the opposite of a self-contained, no-download artifact. Rejected as a
> *distribution* mechanism. *(It remains extremely useful for CI/testing — see §6.)*

### 4.5 OCI / container multi-arch images (manifest lists) — **Rejected (HR3)**

Docker/OCI "multi-arch" images use a manifest list that points at per-arch image blobs; the
runtime pulls the right one.

> **Proof of impossibility for HR3:** this is a *pull-from-registry* model. The per-arch blobs
> live in a registry and are downloaded on demand; the client also requires a container runtime
> installed. It fundamentally downloads from the network and needs external tooling, violating
> "install itself … without downloading anything." Rejected.

### 4.6 Shell-polyglot self-extractor (makeself-style `#!/bin/sh` + `uname -m` + appended payloads) — **Viable**

A single file whose leading bytes are a POSIX shell script. The stub reads `uname -m`, seeks to
the matching embedded ELF slice, extracts it (to a temp file or an in-memory `memfd`), and
`exec`s it. This is the makeself pattern (*"a small shell script that generates a
self-extractable compressed tar archive … The resulting file appears as a shell script … can be
launched as is."* — <https://github.com/megastep/makeself>) generalized with per-arch dispatch,
plus the classic "binary payload after a marker line" trick
(<https://www.linuxjournal.com/content/add-binary-payload-your-shell-scripts>).

- **HR1/HR2:** ✅ runs anywhere `/bin/sh` + coreutils exist (essentially every Linux, glibc or
  musl). No kernel/loader changes.
- **HR3:** ✅ all arches embedded.
- **HR4:** ✅ trivially — the file is arch-independent, so *every* arch downloads the **same
  bytes**; determinism is free.
- **HR5:** ✅ no architecture restriction (it's just data slices + `uname -m`).
- **Cons:** depends on `/bin/sh`, `uname`, and a way to write/exec the slice (temp file, or
  `memfd_create`+`execveat` on Linux ≥ 3.17). It is not itself a native ELF — it runs via the
  shell, not directly via the kernel's ELF loader. `uname -m` strings must be mapped carefully
  (e.g. `armv7l`, `armv6l`, `aarch64`, `x86_64`, `i686`, `riscv64`).

### 4.7 Native per-arch stub + appended fat blob (**Recommended** — see §5) — **Viable**

Each distributed file is a **normal native ELF for one arch** (loads directly via the kernel,
no shell needed) with a **fat blob appended as trailing data** (ignored by the loader, §2.2).
The blob contains every arch's native binary plus an index/trailer. The running program reads
itself, parses the appended blob, and can reconstruct the canonical file for *any* target arch.

DataDog's `adipo` is a partial precedent (*"The stub binary enables creating self-extracting fat
binaries"*, <https://github.com/DataDog/adipo>), but it targets *micro-architectures* within one
arch and its stub is single-arch (x86-64/arm64 only). `gozip` shows the self-reading trick
(*"adding zip files behind a binary … distribute one executable that can automatically extract
required files"*, <https://github.com/sanderhahn/gozip>). We combine and generalize these.

- **HR1/HR2:** ✅ the launched file is a native static ELF (`CGO_ENABLED=0`), runnable on any
  stock kernel ≥ 3.2, glibc or musl. No shell required for normal execution.
- **HR3:** ✅ blob carries all arches.
- **HR4:** ✅ by construction: `canonical(T) = native(T) ++ fatblob`, where `fatblob` is
  identical in every distributed file. Any host reconstructs `canonical(T)` byte-for-byte
  (proven for all 25 host→target pairs in §8).
- **HR5:** ✅ arch-agnostic container; riscv32 slot reserved (§3, §7).
- **Cons:** the leading bytes differ per arch (this is the "arch-specific stub" the brief
  anticipated); self-reconstruction logic must be careful and well-tested.

**§4.6 vs §4.7** are not mutually exclusive — the recommended design (§5) makes the artifact a
**native ELF *and* a valid shell script at once** (a true polyglot), getting §4.7's native
execution with §4.6's shell fallback for exotic environments. See §5.3.

---

## 5. Recommended architecture

### 5.1 The container format ("fatblob")

```
canonical(T)  =  native_elf(T)          # a normal, static, reproducible Go binary for arch T
              ++ FATBLOB                 # appended trailing data, identical in every canonical(*)

FATBLOB       =  "FATBLOB\x01"           # magic
              ++ u16 count
              ++ for each arch in FIXED_SORTED_ORDER:
                     name[8] || u64 offset || u64 length      # index entries
              ++ concat(native_elf(arch) for arch in FIXED_SORTED_ORDER)   # payload slices
              ++ TRAILER

TRAILER       =  u64 fatblob_total_length ++ "FATBLOBZ"        # fixed-size footer at EOF
```

- **Self-location without knowing its own ELF size:** the running program opens itself, seeks
  to `EOF-16`, reads the trailer to get `fatblob_total_length`, and computes
  `fatblob_start = filesize - fatblob_total_length`. No dependence on the ELF's internal size.
- **Deterministic order:** slices are stored in a fixed, sorted arch order so the blob is
  canonical regardless of who builds it.

### 5.2 The determinism property (HR4), precisely

Let `native(A)` be the bare reproducible Go binary for arch `A`, and `FATBLOB` the container
built from `{native(A)}` in fixed order. Then:

```
canonical(T)               = native(T) ++ FATBLOB
reconstruct_from(H → T):   read own file  → strip native(H) prefix → recover FATBLOB
                           → extract native(T) from FATBLOB
                           → emit native(T) ++ FATBLOB   ==  canonical(T)
```

Because `FATBLOB` is byte-identical in every `canonical(*)` and `native(T)` is pulled verbatim
from it, `reconstruct_from(H→T) == canonical(T)` for all `H, T`. **This is the md5-identity the
brief requires**, and §8 proves it empirically for all 5×5 pairs.

### 5.3 Polyglot option (native ELF *and* shell script)

To also satisfy the most hostile environments (no matching arch slice runnable, or a shell-only
recovery path), the leading bytes can be crafted so the file is simultaneously a valid ELF and a
valid `/bin/sh` script — the APE technique
(<https://github.com/jart/cosmopolitan/blob/master/ape/specification.md>). This is an
*enhancement*, not required for the core requirements, and adds complexity; it will be evaluated
separately during implementation. The base design (§5.1) is native-ELF + appended blob.

### 5.4 Reproducible builds (HR4 foundation)

Every `native(A)` is built with the Go team's documented reproducible recipe:

> *"For Go programs that don't need cgo, a reproducible build is as simple as compiling with
> `CGO_ENABLED=0 go build -trimpath`."* — <https://go.dev/blog/rebuild>

Concretely: `CGO_ENABLED=0 GOOS=linux GOARCH=<a> go build -trimpath -buildvcs=false
-ldflags="-s -w -buildid="`. Plus:

- **Pin the exact Go toolchain version** — the compiler version is a "relevant input"
  (<https://go.dev/blog/rebuild>). We pin via `go.mod`'s `toolchain` directive / CI.
- `-buildvcs=false` removes embedded VCS revision/time (Go 1.18+ stamps these by default;
  *"This information may be omitted using the flag -buildvcs=false."* — <https://go.dev/doc/go1.18>).
- `-trimpath` removes filesystem paths (*"remove all file system paths from the resulting
  executable, to improve build reproducibility."* — <https://go.dev/doc/go1.13>).
- `SOURCE_DATE_EPOCH` is **not needed**: Go embeds no build timestamp by default (the Go team
  declined to, *"as producing bit-identical binaries for bit-identical inputs is a goal."* —
  golang/go#23175). Verified: `SOURCE_DATE_EPOCH` appears nowhere in the Go source or
  reproducible-builds.org's Go-less docs.

§8 confirms byte-identical output across two independent build passes for all 5 arches.

### 5.5 Portability across distros + libc (HR1/HR2)

`CGO_ENABLED=0` yields a **static** binary with no libc dependency:

- Go links statically by default and, with cgo off, uses its **pure-Go** DNS resolver and
  **pure-Go** `/etc/passwd` parsing instead of libc's `getaddrinfo`/`getpwuid_r`
  (net: *"It can use a pure Go resolver … or … a cgo-based resolver that calls C library
  routines"*, the `netgo` tag *"disables entirely the use of the native (CGO) resolver"* —
  <https://pkg.go.dev/net>; os/user similarly, `osusergo` — <https://pkg.go.dev/os/user>).
- This is why the same static binary runs on glibc **and** musl systems. OpenWrt (musl by
  default — <https://wiki.musl-libc.org/projects-using-musl.html>) confirms it: building with
  `CGO_ENABLED=0` produces *"a binary that does not depend on libc"* and fixes the musl loader
  error (<https://forum.openwrt.org/t/golang-cross-compiled-program-not-works-on-openwrt-x86-64/104638>).
- **Avoid static-glibc-via-cgo**, which is the classic footgun: glibc's NSS *"won't work
  properly without shared libraries"* (<https://sourceware.org/glibc/wiki/FAQ>). Pure-Go static
  sidesteps NSS entirely.
- **Minimum kernel:** Go ≥ 1.24 requires **Linux 3.2+** (<https://go.dev/wiki/MinimumRequirements>).
  Our optional `memfd_create`+`execveat` runtime path needs **3.17+**; a temp-file fallback
  covers 3.2–3.16.

### 5.6 `go-teleport-self` (the demonstration command)

`go-teleport-self user@host`:
1. Opens SSH to `host`, runs `uname -m` (and `/bin/sh` probe) to detect the remote arch.
2. Maps `uname -m` → our arch id (`x86_64→amd64`, `i686/i386→386`, `armv6l/armv7l→arm`,
   `aarch64→arm64`, `riscv64→riscv64`, `riscv32→<reserved>`).
3. Reconstructs `canonical(remote_arch)` from its own appended `FATBLOB` (byte-identical to that
   arch's official download — HR4).
4. `scp`/streams it into `~/local/bin/` on the remote, `chmod +x`, verifies md5.
No network fetch occurs; everything ships inside the running binary.

---

## 6. Testing under emulation (CI + local)

Two complementary emulation layers; the design uses **both**:

- **QEMU user-mode + `binfmt_misc`** (fast, for "does the arch's binary execute + produce the
  right output" checks). Already available on the dev host: handlers registered for
  `qemu-riscv32/riscv64/arm/aarch64/i386/x86_64`. This is how the POC in §8 actually ran all
  five arch binaries on one arm64 machine. Ideal for CI arch-matrix smoke tests.
- **QEMU system emulation** (full guest kernel + userland + SSH) for the end-to-end
  `go-teleport-self` demo the brief calls for. Requires `qemu-system-x86` and
  `qemu-system-misc` (riscv) in addition to the already-installed `qemu-system-arm/aarch64`;
  arm64 guests can use `/dev/kvm` on this host, others use TCG. Installing these system
  emulators is a documented setup step (root/apt), performed in CI via the workflow and locally
  via a documented `make` target.

*(Note: `binfmt_misc` and QEMU are testing/enabling tools here, not part of the distributed
artifact — see the §4.4 rejection of binfmt as a distribution mechanism.)*

---

## 7. The riscv32 decision

HR5 lists riscv32, but §3 proves no Go toolchain can produce a riscv32 Linux userspace binary
today. Options, least-to-most invasive:

| Option | Meets "Go program on riscv32 Linux"? | Notes |
|--------|--------------------------------------|-------|
| **A. Reserve the slot; ship 5 arches now** | N/A (deferred) | Container format includes a `riscv32` index entry that is *empty/placeholder*; everything else works. Cleanest; unblocks the project. **Recommended default.** |
| **B. Non-Go slice for riscv32** | ✅ (not Go) | Compile the *stub/installer* for riscv32 in C/Rust/Zig (all have riscv32-linux support) and embed it as the riscv32 slice; the "program" for riscv32 is that native helper. Keeps HR5 literally satisfied for install/teleport, without a Go runtime on rv32. |
| **C. gccgo riscv32 port** | ✅ (Go, unproven) | libgo has the plumbing (<https://github.com/gcc-mirror/gcc/blob/master/libgo/configure.ac>) but no shipped toolchain; DIY, high risk. |
| **D. Require RV64 soft-core** | ✅ (riscv64) | If the LiteX/VexRISCV target can be built as RV64 with MMU + Linux, the existing `riscv64` slice just works. Sidesteps rv32 entirely. |

**Recommendation:** ship **A** now (reserve the slot, document the impossibility with the §3
references), and design the container so **B** can drop in later without a format change. Flag
**D** to the user as the lowest-effort way to get real RISC-V Linux coverage if their hardware
can run RV64.

---

## 8. Empirical proof-of-concept (run on this host, 2026-08-27)

Host: arm64 Debian sid, Go 1.26.7, QEMU 11.1 user-mode with `binfmt_misc`.

**(a) Reproducible static builds — byte-identical across two independent passes:**

| arch | md5 (pass A) | md5 (pass B) | result |
|------|--------------|--------------|--------|
| 386   | `759b9fcb…caa48` | `759b9fcb…caa48` | identical |
| amd64 | `a4a358fe…31f27` | `a4a358fe…31f27` | identical |
| arm   | `e892c80e…c359a4` | `e892c80e…c359a4` | identical |
| arm64 | `5651326e…82ad45` | `5651326e…82ad45` | identical |
| riscv64 | `f14b7428…10e211` | `f14b7428…10e211` | identical |

Build recipe: `CGO_ENABLED=0 GOOS=linux GOARCH=<a> go build -trimpath -buildvcs=false
-ldflags="-s -w -buildid="`. All five are `statically linked, stripped` per `file`.

**(b) All five actually execute under QEMU-user on one arm64 machine:**
each `hello.<arch>` printed `hello from GOARCH=<arch>` (386, amd64, arm, arm64, riscv64).

**(c) Trailing data does not break execution:** appended 100 KB of random bytes to the arm64
binary → still ran normally (confirms §2.2).

**(d) Determinism of the container model:** a Python model of §5.1 built `canonical(T)` for all
five arches and simulated `reconstruct_from(H→T)` for **all 25 host→target pairs**;
every reconstruction was **bit-identical** to the corresponding `canonical(T)` download.
(arm64→riscv64 example: both md5 `ad09013c0f990c549adad643364b79dd`.)

---

## 9. Summary comparison of viable options

| Criterion | §4.6 Shell polyglot | §4.7 Native stub + fat blob (recommended) |
|-----------|---------------------|-------------------------------------------|
| Runs via kernel ELF loader directly | ❌ (via `/bin/sh`) | ✅ native ELF |
| Depends on `/bin/sh` + coreutils | ✅ required | ❌ not for normal run |
| Determinism (HR4) | ✅ trivial (all arches = same file) | ✅ by construction (proven §8) |
| Distinct bytes per arch | No (one universal file) | Yes ("arch-specific stub") |
| glibc + musl (HR2) | ✅ | ✅ (`CGO_ENABLED=0` static) |
| No-download self-contained (HR3) | ✅ | ✅ |
| riscv32 (HR5) | slot-reservable | slot-reservable |
| Writes payload to run other arch | temp file / `memfd` | only when running a *foreign* slice |
| Complexity | low–medium (shell correctness) | medium (self-read/reconstruct) |
| Matches brief's stated model ("stub + fat part") | partially | ✅ exactly |

**Chosen:** §4.7 native-stub + appended fat blob, built from `CGO_ENABLED=0 -trimpath`
reproducible slices, with §4.6's shell polyglot as an optional robustness enhancement (§5.3),
QEMU (user + system) for testing (§6), and riscv32 handled as a reserved/pluggable slot (§7).

---

## 10. Rejected options at a glance (with impossibility proof)

| Option | Rejected by | One-line proof (see section) | Key reference |
|--------|-------------|------------------------------|---------------|
| FatELF native fat ELF | HR1 | Needs patched kernel (+glibc) never merged upstream | icculus.org/fatelf; finger 2009-11-03 |
| macOS `lipo` universal | HR1 | Mach-O container; Linux ELF loader has no equivalent & rejects it | Apple universal-binary doc; binfmt_elf.c |
| Cosmopolitan / APE | HR5 | Spec supports only amd64+arm64; needs own libc (not Go) | ape/specification.md; cosmocc README |
| `binfmt_misc` register | HR1/HR3 | Root-only per-machine config + presupposes installed interpreter | (QEMU-user uses exactly this) |
| OCI multi-arch images | HR3 | Pulls per-arch blobs from a registry; needs runtime | (manifest-list pull model) |
| **Go for riscv32** | HR5 (partial) | No gc port; TinyGo Linux is x86/ARM only; gccgo unproven | golang/go#51067; tinygo linux guide |

---

## 11. All references (verified live on 2026-08-27)

**Kernel / ELF**
- Linux `fs/binfmt_elf.c` — <https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/fs/binfmt_elf.c>
- arm64 `elf_check_arch` — <https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/arch/arm64/include/asm/elf.h>
- x86 `elf_check_arch` — <https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/arch/x86/include/asm/elf.h>
- `EM_*` constants — <https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/plain/include/uapi/linux/elf-em.h>
- ELF gABI header spec — <https://www.sco.com/developers/gabi/latest/ch4.eheader.html>
- `man 5 elf` — <https://man7.org/linux/man-pages/man5/elf.5.html>

**macOS contrast**
- Apple universal binary — <https://developer.apple.com/documentation/apple-silicon/building-a-universal-macos-binary>
- `man lipo` — <https://keith.github.io/xcode-man-pages/lipo.1.html>
- Mach-O `fat.h` — <https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/EXTERNAL_HEADERS/mach-o/fat.h>

**FatELF**
- Project page — <https://icculus.org/fatelf/>
- Author halt post — <https://icculus.org/finger/icculus?date=2009-11-03>
- LWN coverage — <https://lwn.net/Articles/359070/>
- Wikipedia "Fat binary" — <https://en.wikipedia.org/wiki/Fat_binary>

**Cosmopolitan / APE / self-extractors**
- APE spec — <https://github.com/jart/cosmopolitan/blob/master/ape/specification.md>
- cosmocc README — <https://github.com/jart/cosmopolitan/blob/master/tool/cosmocc/README.md>
- apelink (uname -m dispatch) — <https://github.com/jart/cosmopolitan/blob/master/tool/build/apelink.c>
- makeself — <https://github.com/megastep/makeself>
- Binary payload in shell — <https://www.linuxjournal.com/content/add-binary-payload-your-shell-scripts>
- DataDog adipo — <https://github.com/DataDog/adipo>
- sanderhahn/gozip — <https://github.com/sanderhahn/gozip>

**Go: reproducibility, static linking, platforms, riscv32**
- Reproducible toolchains blog — <https://go.dev/blog/rebuild>
- `-trimpath` (Go 1.13) — <https://go.dev/doc/go1.13>
- VCS stamping / `-buildvcs` (Go 1.18) — <https://go.dev/doc/go1.18>
- No build timestamp (policy) — <https://github.com/golang/go/issues/23175>
- cmd/go, cmd/link, cmd/cgo — <https://pkg.go.dev/cmd/go>, <https://pkg.go.dev/cmd/link>, <https://pkg.go.dev/cmd/cgo>
- net (resolver) — <https://pkg.go.dev/net> ; os/user — <https://pkg.go.dev/os/user>
- Go minimum requirements (kernel 3.2) — <https://go.dev/wiki/MinimumRequirements>
- Go 1.24 / 1.23 release notes — <https://go.dev/doc/go1.24>, <https://go.dev/doc/go1.23>
- Go install/source (riscv64 only) — <https://go.dev/doc/install/source>
- golang/go#51067 (riscv32) — <https://github.com/golang/go/issues/51067>
- TinyGo Linux guide — <https://tinygo.org/docs/guides/linux/>
- TinyGo riscv32 target (bare metal) — <https://github.com/tinygo-org/tinygo/blob/release/targets/riscv32.json>
- TinyGo lang support — <https://tinygo.org/docs/reference/lang-support/>
- gccgo libgo configure (riscv32 plumbing) — <https://github.com/gcc-mirror/gcc/blob/master/libgo/configure.ac>

**Distros / libc**
- musl "projects using musl" (OpenWrt) — <https://wiki.musl-libc.org/projects-using-musl.html>
- OpenWrt Go forum thread — <https://forum.openwrt.org/t/golang-cross-compiled-program-not-works-on-openwrt-x86-64/104638>
- glibc FAQ (static NSS) — <https://sourceware.org/glibc/wiki/FAQ>
