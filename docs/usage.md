# Usage

## Build

```bash
make build        # or: uv run python build.py
```

This produces, under `dist/`:

```
dist/go-teleport-self.386        canonical (native 386   + shared blob)
dist/go-teleport-self.amd64      canonical (native amd64 + shared blob)
dist/go-teleport-self.arm        canonical (native arm   + shared blob)
dist/go-teleport-self.arm64      canonical (native arm64 + shared blob)
dist/go-teleport-self.riscv64    canonical (native riscv64 + shared blob)
dist/MANIFEST.json               sizes + sha256 + md5 for each
```

Every file is a normal, directly-runnable ELF for its architecture. They all
carry the identical multi-architecture blob, so any one of them can install any
of the others.

## Inspect a binary

```bash
$ ./dist/go-teleport-self.arm64 info
go-teleport-self v0.0-…
running arch:   arm64
embedded arches:
  386      present      2232446 bytes  sha256:…
  amd64    present      2392190 bytes  sha256:…
  arm      present      2293886 bytes  sha256:…
  arm64    present      2359422 bytes  sha256:…
  riscv64  present      2228350 bytes  sha256:…
  riscv32  reserved (no binary embedded)
```

- `list` — the same inventory as JSON.
- `verify` — confirms the attached blob round-trips and that the file *is*
  `canonical(<its own arch>)`.
- `extract <arch> <out>` — write `canonical(<arch>)` to a file. The extracted
  bytes are bit-identical to that architecture's `dist/` artifact.

```bash
# From an arm64 binary, produce the exact riscv64 distributable:
$ ./dist/go-teleport-self.arm64 extract riscv64 /tmp/out.riscv64
$ md5sum /tmp/out.riscv64 dist/go-teleport-self.riscv64   # identical
```

## Teleport onto another machine

```bash
go-teleport-self user@host
```

This will, over SSH and **without downloading anything**:

1. run `uname -m` on the remote and map it to an architecture,
2. reconstruct `canonical(<remote arch>)` from the running binary's own embedded
   blob,
3. create `~/local/bin` on the remote and stream the binary into
   `~/local/bin/go-teleport-self` (via `cat >`, no scp/temp files),
4. `chmod +x` it and verify the remote md5 matches the bytes sent.

Extra SSH arguments (port, key, options) go after `--`:

```bash
go-teleport-self tester@127.0.0.1 -- -p 2222 -i ./key -o StrictHostKeyChecking=accept-new
```

The installed binary is native to the remote machine, so it runs there directly —
and, carrying the same blob, it can teleport onward to yet another architecture.

## Portability

The shipped binaries are static (`CGO_ENABLED=0`), so they run on **glibc and
musl** systems alike (Debian/Ubuntu/RedHat/Arch/Gentoo and musl-based OpenWrt/
Alpine) and on any Linux kernel ≥ 3.2. No shared libraries, no libc, no runtime
dependencies beyond a POSIX shell on the remote for the install commands.
