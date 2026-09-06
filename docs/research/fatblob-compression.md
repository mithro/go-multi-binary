# FATBLOB slice compression (format \x02)

Date: 2026-09-06

## Goal

Minimise the on-disk size of the released fat binary by compressing each
embedded native binary inside the FATBLOB. Optimise for the **smallest output**;
compression time is irrelevant (runs once at build). Decompression happens only
when installing onto a new machine (`fatblob.Reconstruct`) and **must be pure Go
and link into a `CGO_ENABLED=0` static binary**.

## Codec chosen: xz / LZMA2 (encoded by reference `xz`, decoded by pure-Go `ulikunitz/xz`)

Compression is applied once when the blob is built. The packer prefers the
**system `xz -9 -e -T1`** encoder (the reference LZMA2 match-finder) and falls
back to the pure-Go `ulikunitz/xz` encoder only when `xz` is absent. Either way
the stored bytes are a standard `.xz` stream that the **pure-Go
`ulikunitz/xz` decoder** reads in-binary under `CGO_ENABLED=0`.

`-T1` forces single-threaded encoding: multi-threaded `xz` splits the input into
independently-compressed blocks, which both hurts the ratio and makes the byte
output depend on the thread count.

### Ratio benchmark (real stripped Go ELFs, `CGO_ENABLED=0 -ldflags "-s -w"`)

claude-teleport natives, compressed size / ratio:

| input (raw)          | xz -9e (system) | zstd --ultra -22 | zstd -19 | gzip -9 | ulikunitz xz (pure-Go enc) | klauspost zstd best |
|----------------------|-----------------|------------------|----------|---------|----------------------------|---------------------|
| amd64  8,675,488 B   | 2,709,512 (31.2%) | 2,938,036 (33.9%) | 2,938,458 (33.9%) | 3,459,668 (39.9%) | 3,248,380 (37.4%) | 3,277,345 (37.8%) |
| arm64  7,995,552 B   | 2,304,020 (28.8%) | 2,614,098 (32.7%) | 2,614,340 (32.7%) | 3,110,463 (38.9%) | 2,783,852 (34.8%) | 2,926,583 (36.6%) |

Key findings:

- Reference **`xz -9e` wins the ratio decisively** (~31% vs zstd's ~34%, gzip's
  ~40%).
- The **pure-Go encoders are much weaker** (xz 37.4%, zstd best 37.8%) than the
  reference C encoders, because their match-finders are weaker — *not* the
  dictionary size. This is why we shell out to system `xz` at pack time and only
  fall back to the pure-Go encoder.
- **BCJ filters** (`xz --x86`) improve ratio further (29.8%) but the pure-Go
  `ulikunitz/xz` decoder rejects them (`xz: unsupported filter count`), so BCJ is
  **not** used. Plain LZMA2 only.
- `zstd -19` vs `-22` is a rounding error on these inputs, and both trail `xz`.

### In-binary decoder link-size cost

Tiny `CGO_ENABLED=0 -ldflags "-s -w"` program importing only the decoder:

| binary                          | size       | delta vs base |
|---------------------------------|------------|---------------|
| baseline (no codec)             | 1,593,504  | —             |
| + `ulikunitz/xz` decoder        | 2,007,200  | **+413,696 (~404 KB)** |
| + `klauspost/compress/zstd` dec | 2,175,136  | +581,632 (~568 KB)     |

**xz wins on both axes**: better ratio *and* a smaller in-binary decoder. It is
the single codec linked (one decoder), so there is no multi-decoder cost.

The build-time encoder (`os/exec` + the `ulikunitz/xz` writer) is unreachable
from the distributed native (`go-teleport-self` only calls `Reconstruct` ->
`Decompress`) and is **dropped by the linker**: verified with `go tool nm` — 0
xz writer-side symbols, 25 reader-side symbols in the native.

## Before / after size

Measured end-to-end with `fatpack assemble` over five real stripped
claude-teleport natives (this build ~8 MB each; sizes scale linearly to the
~14 MB natives cited in the task).

Raw natives total: **41,345,824 B (~39.4 MB)**.

| quantity                                  | before (\x01, stored) | after (\x02, xz) |
|-------------------------------------------|-----------------------|------------------|
| shared blob (5 slices)                    | ~41.35 MB             | **12.59 MB (30.4%)** |
| one canonical (amd64) = raw head + blob   | ~50.0 MB              | **21.26 MB**     |
| sum of all 5 canonicals                   | ~248 MB               | **104.3 MB**     |

Projected for the task's 14 MB natives (~70 MB uncompressed blob): compressed
blob ~21 MB (30%); a full canonical drops from ~84 MB (14 MB head + 70 MB blob)
to **~35 MB** (uncompressed 14 MB head + ~21 MB compressed blob).

The head native stays uncompressed and kernel-loadable; only the appended blob
is compressed, and it is byte-identical across every `canonical(*)`.

## Round-trip proof

`Reconstruct(canonical(H), T)` was checked against the independently assembled
`canonical(T)` for all 25 host/target pairs of the five real natives: every pair
is **SHA-256 byte-identical** and its reconstructed head begins with the ELF
magic `\x7fELF`. Unit tests in `fatblob/compress_test.go` cover the same law on
synthetic ELF-prefixed inputs plus determinism, corruption, length-mismatch,
truncated-stream, and \x01-magic-rejection cases.

## Format change (\x01 -> \x02)

Index entry grew 32 -> 40 bytes: `name[8] status[1] codec[1] rsvd[6] offset[8]
length[8] rawLen[8]`. `length` is the stored (compressed) length; `rawLen` is the
uncompressed native length (0 for reserved/none). Reserved slices keep
`codec=0, length=0, rawLen=0`. Magic bumped to `FATBLOB\x02`; a `\x01` blob is
rejected with a clear error (no compressed \x01 releases exist in the wild).
