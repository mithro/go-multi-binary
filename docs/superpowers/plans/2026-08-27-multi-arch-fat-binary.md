# Multi-Architecture Fat Binary — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single Go program that carries native binaries for every supported Linux
architecture inside it and can reconstruct, bit-for-bit, the canonical distributable for any
target architecture — enabling `go-teleport-self user@host` to install itself onto a
different-architecture machine over SSH with zero network downloads.

**Architecture:** Each distributable is `native(arch) ++ FATBLOB`, where `native(arch)` is a
reproducible static (`CGO_ENABLED=0 -trimpath`) Go ELF for one arch, and `FATBLOB` is a
byte-identical trailing container holding every arch's native binary plus an index and a
fixed-size EOF trailer. The kernel loads `native(arch)` directly and ignores the appended blob;
the running program reads its own file, recovers `FATBLOB`, and can emit `canonical(T)` for any
target `T`. Determinism follows by construction: same reproducible inputs → identical blob →
identical reconstruction.

**Tech Stack:** Go 1.26 (pinned), pure-Go static binaries, Python 3 (via `uv`) for build/test
orchestration, QEMU (user-mode + system) for cross-arch execution, GitHub Actions for CI.

**Spec:** `docs/research/multi-arch-binary-approaches.md` (the research/design-exploration doc;
§5 defines the container format, §5.2 the determinism property, §5.4 the reproducible-build
recipe, §5.6 the teleport command, §6 emulation, §7 the reserved riscv32 slot).

## Global Constraints

- **Go toolchain pinned:** module targets `go 1.26`; builds use the exact toolchain in CI.
  Reproducibility depends on a fixed compiler version (go.dev/blog/rebuild).
- **Reproducible build flags (every arch, every time):**
  `CGO_ENABLED=0 GOOS=linux GOARCH=<a> [GOARM=6] go build -trimpath -buildvcs=false -ldflags="-s -w -buildid="`
- **Supported arches (fixed sorted order for the blob):** `386, amd64, arm, arm64, riscv64`.
  `riscv32` is a **reserved placeholder slot** — an index entry with zero-length payload and a
  status flag; never a real binary (no Go toolchain can build it; research doc §3/§7).
- **arm variant:** `GOARM=6` (baseline covering ARMv6 Pi 1/Zero and forward-compatible on v7).
- **libc-agnostic:** never enable cgo in the shipped binaries (glibc+musl portability, HR2).
- **Determinism law (HR4):** for all host `H`, target `T`:
  `reconstruct(H → T) == canonical(T)` byte-for-byte (same md5/sha256).
- **No American date formats.** ISO 8601 (YYYY-MM-DD) only, in code and docs.
- **Python for orchestration** (loops/conditionals/subprocess), `uv run` always; Go for the
  binary-format logic (unit-tested).
- **Temp files:** project-local `./tmp/`, never `/tmp`. Clean up after.
- **Commits:** small and discrete; each task ends committed. Commit trailer:
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` + `Claude-Session:` line.

---

## File Structure

- `internal/fatblob/format.go` — container encode/decode/trailer; pure, no I/O of the process's
  own file. One responsibility: the on-disk format.
- `internal/fatblob/format_test.go` — round-trip, trailer, determinism unit tests.
- `internal/fatblob/selfimage.go` — read the running process's own file, recover the appended
  `FATBLOB`, expose `Reconstruct(target)`. Responsibility: self-image I/O.
- `internal/fatblob/selfimage_test.go`
- `internal/archdetect/archdetect.go` — `uname -m` string → arch id; current arch; the canonical
  arch table (single source of truth for names/order/`e_machine`).
- `internal/archdetect/archdetect_test.go`
- `cmd/go-teleport-self/main.go` — the shipped program: `info`, `list`, `verify`, `extract`,
  and the default teleport action (`go-teleport-self user@host`).
- `internal/teleport/teleport.go` — SSH/scp orchestration: detect remote arch, stream
  `canonical(remote)`, install to `~/local/bin`, verify md5. Transport is an interface (testable
  with a fake).
- `internal/teleport/teleport_test.go`
- `cmd/fatpack/main.go` — build-time tool: assemble `FATBLOB` from bare native binaries and emit
  each `canonical(arch)` + a `MANIFEST` (sha256/md5 per artifact). Thin CLI over `fatblob`.
- `build.py` — orchestrator: build all 5 native arches with the pinned flags, invoke `fatpack`,
  produce `dist/`. Deterministic; prints a manifest.
- `test/determinism_test.py` — build twice; assert identical; assert reconstruct==canonical for
  all pairs (drives real binaries).
- `emulation/user/run.py` — run a canonical binary under QEMU-user/binfmt for a given arch.
- `emulation/system/` — QEMU system-emulation harness (guest images + SSH) for the end-to-end
  teleport demo; contents chosen during Task 11's spike.
- `.github/workflows/ci.yml` — build matrix, determinism gate, qemu-user tests, system-emu job.
- `Makefile` — thin developer entrypoints delegating to `build.py`/`uv`.
- `docs/` — architecture, usage, testing, contributing.

---

## Task 1: fatblob container format — encode/decode round-trip

**Files:**
- Create: `internal/fatblob/format.go`
- Test: `internal/fatblob/format_test.go`

**Interfaces:**
- Produces:
  - `type Slice struct { Arch string; Status uint8; Data []byte }` (Status: 0=present, 1=reserved/empty)
  - `type Blob struct { Slices []Slice }`
  - `func Encode(b Blob) ([]byte, error)` — deterministic bytes: magic `"FATBLOB\x01"`, u16 count,
    fixed-order index (`name[8] || u8 status || u8 pad || u16 pad || u32 pad || u64 offset || u64 length`),
    concatenated payloads, then trailer `u64 totalLen || "FATBLOBZ"`.
  - `func Decode(data []byte) (Blob, error)` — parse the whole blob (data starts at magic).
  - `const Magic = "FATBLOB\x01"`, `const TrailerMagic = "FATBLOBZ"`, `const TrailerLen = 16`.
  - `func FixedArchOrder() []string` → `["386","amd64","arm","arm64","riscv64","riscv32"]`
    (riscv32 last; it is the reserved slot).

- [ ] **Step 1: Write the failing test**

```go
package fatblob

import (
	"bytes"
	"testing"
)

func testBlob() Blob {
	return Blob{Slices: []Slice{
		{Arch: "386", Status: 0, Data: []byte("iii")},
		{Arch: "amd64", Status: 0, Data: []byte("AAAA")},
		{Arch: "arm", Status: 0, Data: []byte("rr")},
		{Arch: "arm64", Status: 0, Data: []byte("bbbbb")},
		{Arch: "riscv64", Status: 0, Data: []byte("V")},
		{Arch: "riscv32", Status: 1, Data: nil}, // reserved placeholder
	}}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	b := testBlob()
	enc, err := Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.HasPrefix(enc, []byte(Magic)) {
		t.Fatalf("missing magic prefix")
	}
	if !bytes.HasSuffix(enc, []byte(TrailerMagic)) {
		t.Fatalf("missing trailer magic")
	}
	got, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.Slices) != len(b.Slices) {
		t.Fatalf("slice count = %d, want %d", len(got.Slices), len(b.Slices))
	}
	for i := range b.Slices {
		if got.Slices[i].Arch != b.Slices[i].Arch ||
			got.Slices[i].Status != b.Slices[i].Status ||
			!bytes.Equal(got.Slices[i].Data, b.Slices[i].Data) {
			t.Fatalf("slice %d mismatch: got %+v want %+v", i, got.Slices[i], b.Slices[i])
		}
	}
}

func TestEncodeDeterministic(t *testing.T) {
	a, _ := Encode(testBlob())
	c, _ := Encode(testBlob())
	if !bytes.Equal(a, c) {
		t.Fatalf("Encode not deterministic")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fatblob/ -run TestEncode -v`
Expected: FAIL — `undefined: Encode` etc.

- [ ] **Step 3: Write minimal implementation** in `format.go`

Implement `Slice`, `Blob`, constants, `FixedArchOrder`, `Encode`, `Decode`. Encoding rules:
- Header: `Magic` (8 bytes) + `binary.LittleEndian` u16 count.
- Index: for each slice in the order given, write `name[8]` (arch, NUL-padded, error if >8),
  `status` u8, 7 reserved zero bytes (keep index entries 8-byte aligned and future-proof),
  `offset` u64 (from start of the payload region), `length` u64.
- Payload region: concatenation of each slice's `Data` in the same order.
- Trailer: u64 total blob length (including magic..trailer) + `TrailerMagic`.
- `Decode`: validate magic; read count; read index; slice payloads by offset/length; validate
  trailer length equals `len(data)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fatblob/ -run TestEncode -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/fatblob/format.go internal/fatblob/format_test.go
git commit -m "feat(fatblob): deterministic container encode/decode"
```

---

## Task 2: self-image — recover the appended blob and reconstruct any arch

**Files:**
- Create: `internal/fatblob/selfimage.go`
- Test: `internal/fatblob/selfimage_test.go`

**Interfaces:**
- Consumes: `Encode`, `Decode`, `Blob`, `Slice`, `TrailerLen`, `TrailerMagic`.
- Produces:
  - `func SplitCanonical(image []byte) (native []byte, blob Blob, err error)` — given a full
    `canonical(arch)` byte image (native ELF + appended blob), locate the blob via the EOF
    trailer, return the leading native bytes and the parsed blob.
  - `func BuildCanonical(nativeForTarget []byte, blob Blob) []byte` — `nativeForTarget ++ Encode(blob)`.
  - `func Reconstruct(image []byte, target string) ([]byte, error)` — from any canonical image,
    produce `canonical(target)` = `slice(target).Data ++ Encode(blob)`. Error if target slice is
    reserved/empty or unknown.
  - `func ReadSelf() ([]byte, error)` — read the current executable's own file
    (`os.Executable` + `os.ReadFile`, resolve symlinks).

- [ ] **Step 1: Write the failing test** (`selfimage_test.go`)

```go
package fatblob

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// Build a synthetic blob whose slices ARE mini "native" images, then verify the
// canonical(H) -> reconstruct(T) == canonical(T) law for all present pairs.
func presentBlob() Blob {
	return Blob{Slices: []Slice{
		{Arch: "386", Data: []byte("\x7fELF-386-native")},
		{Arch: "amd64", Data: []byte("\x7fELF-amd64-native-x")},
		{Arch: "arm", Data: []byte("\x7fELF-arm")},
		{Arch: "arm64", Data: []byte("\x7fELF-arm64-native")},
		{Arch: "riscv64", Data: []byte("\x7fELF-rv64")},
		{Arch: "riscv32", Status: 1, Data: nil},
	}}
}

func canonical(t *testing.T, blob Blob, arch string) []byte {
	t.Helper()
	var native []byte
	for _, s := range blob.Slices {
		if s.Arch == arch {
			native = s.Data
		}
	}
	return BuildCanonical(native, blob)
}

func TestReconstructLawAllPairs(t *testing.T) {
	blob := presentBlob()
	present := []string{"386", "amd64", "arm", "arm64", "riscv64"}
	for _, h := range present {
		img := canonical(t, blob, h)
		// sanity: split recovers native + blob
		native, gotBlob, err := SplitCanonical(img)
		if err != nil {
			t.Fatalf("SplitCanonical(%s): %v", h, err)
		}
		if len(gotBlob.Slices) != len(blob.Slices) {
			t.Fatalf("blob slice count mismatch after split from %s", h)
		}
		_ = native
		for _, target := range present {
			got, err := Reconstruct(img, target)
			if err != nil {
				t.Fatalf("Reconstruct(%s->%s): %v", h, target, err)
			}
			want := canonical(t, blob, target)
			if !bytes.Equal(got, want) {
				t.Fatalf("law broken %s->%s: sha %x != %x",
					h, target, sha256.Sum256(got), sha256.Sum256(want))
			}
		}
	}
}

func TestReconstructReservedFails(t *testing.T) {
	img := canonical(t, presentBlob(), "amd64")
	if _, err := Reconstruct(img, "riscv32"); err == nil {
		t.Fatalf("expected error reconstructing reserved riscv32 slot")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fatblob/ -run TestReconstruct -v`
Expected: FAIL — `undefined: BuildCanonical/SplitCanonical/Reconstruct`.

- [ ] **Step 3: Write minimal implementation** (`selfimage.go`)

- `BuildCanonical`: `append(append([]byte{}, native...), Encode(blob)...)` (ignore Encode error
  in this internal helper by having a `mustEncode`, or return error variant — keep it returning
  bytes and panic on encode error, since inputs are already validated; simpler: have
  `BuildCanonical` call `Encode` and drop error via a package-internal check).
- `SplitCanonical`: read last `TrailerLen` bytes; verify `TrailerMagic`; read u64 total; blob =
  `image[len(image)-total:]`; `native = image[:len(image)-total]`; `Decode(blob)`.
- `Reconstruct`: `SplitCanonical` → find target slice → error if not found, or Status!=0/empty →
  `BuildCanonical(target.Data, blob)`.
- `ReadSelf`: `os.Executable()`, `filepath.EvalSymlinks`, `os.ReadFile`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/fatblob/ -v`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
git add internal/fatblob/selfimage.go internal/fatblob/selfimage_test.go
git commit -m "feat(fatblob): self-image split + deterministic reconstruct-any-arch"
```

---

## Task 3: archdetect — uname mapping and the canonical arch table

**Files:**
- Create: `internal/archdetect/archdetect.go`
- Test: `internal/archdetect/archdetect_test.go`

**Interfaces:**
- Produces:
  - `type ArchInfo struct { ID string; GOARCH string; GOARM string; EMachine uint16; UnameAliases []string; Supported bool }`
  - `func Table() []ArchInfo` — single source of truth. `386`(EM_386=3), `amd64`(EM_X86_64=62),
    `arm`(EM_ARM=40, GOARM=6), `arm64`(EM_AARCH64=183), `riscv64`(EM_RISCV=243, Supported=true),
    `riscv32`(EM_RISCV=243, Supported=false — reserved).
  - `func FromUname(m string) (string, error)` — map `uname -m` output → arch id
    (`x86_64→amd64`, `i386/i486/i586/i686→386`, `armv6l/armv7l/armhf/arm→arm`,
    `aarch64/arm64→arm64`, `riscv64→riscv64`, `riscv32/riscv→riscv32`). Unknown → error.
  - `func Current() string` — map Go's `runtime.GOARCH` to our id.

- [ ] **Step 1: Write the failing test**

```go
package archdetect

import "testing"

func TestFromUname(t *testing.T) {
	cases := map[string]string{
		"x86_64": "amd64", "i686": "386", "i386": "386",
		"armv7l": "arm", "armv6l": "arm", "aarch64": "arm64",
		"riscv64": "riscv64", "riscv32": "riscv32",
	}
	for in, want := range cases {
		got, err := FromUname(in)
		if err != nil || got != want {
			t.Fatalf("FromUname(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := FromUname("sparc64"); err == nil {
		t.Fatalf("expected error for unknown arch")
	}
}

func TestTableIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Table() {
		if seen[a.ID] {
			t.Fatalf("duplicate arch id %q", a.ID)
		}
		seen[a.ID] = true
	}
	for _, id := range []string{"386", "amd64", "arm", "arm64", "riscv64", "riscv32"} {
		if !seen[id] {
			t.Fatalf("missing arch %q in table", id)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/archdetect/ -v` → FAIL (undefined).

- [ ] **Step 3: Write minimal implementation** per the interface above.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/archdetect/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/archdetect/
git commit -m "feat(archdetect): uname->arch mapping and canonical arch table"
```

---

## Task 4: the shipped CLI skeleton (`go-teleport-self`)

**Files:**
- Create: `cmd/go-teleport-self/main.go`
- Test: `cmd/go-teleport-self/main_test.go`

**Interfaces:**
- Consumes: `fatblob.ReadSelf/SplitCanonical/Reconstruct`, `archdetect.Current/Table`.
- Produces CLI:
  - `go-teleport-self info` — prints running arch, build id, whether a FATBLOB is attached,
    and the list of embedded arches with sizes + sha256.
  - `go-teleport-self list` — machine-readable (JSON) arch inventory.
  - `go-teleport-self verify` — re-encode the blob and confirm each embedded slice's sha256
    matches its index; exit non-zero on mismatch.
  - `go-teleport-self extract <arch> <outpath>` — write `canonical(arch)` to outpath.
  - `go-teleport-self <user@host>` — teleport (Task 6).
- Factor logic into testable functions (e.g. `func inventory(image []byte) ([]ArchEntry, error)`)
  so tests don't need a real attached blob.

- [ ] **Step 1: Write failing test** for `inventory()` using a synthetic canonical image
  (reuse the `fatblob` builders via an exported test helper or construct bytes directly).

```go
package main

import "testing"

func TestInventoryCountsPresentArches(t *testing.T) {
	img := syntheticCanonical(t) // helper builds native("amd64")++blob with 5 present +1 reserved
	entries, err := inventory(img)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	present := 0
	for _, e := range entries {
		if e.Present {
			present++
		}
	}
	if present != 5 {
		t.Fatalf("present arches = %d, want 5", present)
	}
}
```

- [ ] **Step 2: Run → FAIL.**  `go test ./cmd/go-teleport-self/ -v`
- [ ] **Step 3: Implement** `inventory`, the subcommand dispatch, and `syntheticCanonical` helper.
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat(cli): go-teleport-self info/list/verify/extract skeleton`.

---

## Task 5: `fatpack` build tool

**Files:**
- Create: `cmd/fatpack/main.go`
- Test: `cmd/fatpack/main_test.go`

**Interfaces:**
- `fatpack assemble --in dist/native --out dist --manifest dist/MANIFEST.json` — reads bare
  native binaries named `native.<arch>` from `--in`, builds `FATBLOB` (riscv32 = reserved),
  writes `go-teleport-self.<arch>` = `canonical(arch)` for each present arch to `--out`, and a
  manifest listing size+sha256+md5 per artifact and for the shared blob.
- Consumes: `fatblob.Encode/BuildCanonical`, `archdetect.FixedArchOrder`/`Table`.

- [ ] **Step 1: Write failing test:** assemble from two tiny fake native files in a temp dir;
  assert `canonical(386)` and `canonical(amd64)` differ only by their prefix and share an
  identical trailing blob (compare the last N bytes); assert manifest sha256 matches recomputed.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat(fatpack): assemble canonical artifacts + manifest`.

---

## Task 6: teleport over SSH (with a testable transport)

**Files:**
- Create: `internal/teleport/teleport.go`, `internal/teleport/teleport_test.go`
- Modify: `cmd/go-teleport-self/main.go` (wire the default `user@host` action)

**Interfaces:**
- `type Transport interface { RunUname(ctx) (string, error); Put(ctx, data []byte, remotePath string) error; Run(ctx, cmd string) (string, error) }`
- `type SSHTransport struct { Target string }` implementing `Transport` via `ssh`/`scp`
  (`ssh -o BatchMode=yes`, no host-key hashing per conventions).
- `func Deploy(ctx, image []byte, t Transport, destDir string) (Result, error)` — detect remote
  arch via `RunUname`+`archdetect.FromUname`, `Reconstruct(image, arch)`, `Put` to
  `destDir/go-teleport-self`, `chmod +x`, verify remote md5 == local md5, return `Result{Arch, Md5, RemotePath}`.
- Default `destDir` = `~/local/bin`.

- [ ] **Step 1: Write failing test** with a `fakeTransport` that reports `uname -m` = `riscv64`
  and captures `Put` bytes; assert `Deploy` puts bytes whose md5 == md5 of
  `Reconstruct(image,"riscv64")`, to `~/local/bin/go-teleport-self`.
- [ ] **Step 2: Run → FAIL.**
- [ ] **Step 3: Implement** `Deploy` + `fakeTransport`; wire CLI (real `SSHTransport`).
- [ ] **Step 4: Run → PASS.**
- [ ] **Step 5: Commit** `feat(teleport): SSH self-install with md5 verification`.

---

## Task 7: `build.py` orchestrator + reproducibility gate

**Files:**
- Create: `build.py`, `Makefile`
- Create: `test/determinism_test.py`

**Details:**
- `build.py`:
  - For each supported arch, run the pinned reproducible `go build` into `dist/native/native.<arch>`.
  - Run `fatpack assemble` → `dist/go-teleport-self.<arch>` + `dist/MANIFEST.json`.
  - Print the manifest (arch, size, sha256, md5).
  - Pure stdlib; use `subprocess`; fail hard on any nonzero exit; no `2>/dev/null`.
- `Makefile`: `make build` → `uv run python build.py`; `make test` → `go test ./... && uv run python -m pytest test/`.
- `test/determinism_test.py`:
  - Build twice into two dirs; assert every `dist/*.<arch>` md5 matches across builds.
  - Assert, for all present host/target pairs, that
    `go-teleport-self.<host> extract <target>` md5 == `go-teleport-self.<target>` md5 (HR4 law
    on REAL binaries).

- [ ] **Step 1:** Write `test/determinism_test.py` (it will fail — no build yet).
- [ ] **Step 2:** Run `uv run python -m pytest test/determinism_test.py -v` → FAIL.
- [ ] **Step 3:** Implement `build.py` + `Makefile`.
- [ ] **Step 4:** Run the build, then the test → PASS (byte-identical + law holds).
- [ ] **Step 5:** Commit `feat(build): reproducible multi-arch build + determinism gate`.

---

## Task 8: QEMU-user execution harness + cross-arch smoke test

**Files:**
- Create: `emulation/user/run.py`
- Create: `test/exec_qemu_user_test.py`

**Details:**
- Precondition check: `binfmt_misc` registered handlers exist (or `qemu-<arch>` binaries present).
  Skip with a clear message if unavailable (so `go test`/pytest still pass on a bare host).
- `run.py <arch> [args...]`: run `dist/go-teleport-self.<arch> info` under emulation and return output.
- `test/exec_qemu_user_test.py`: for each present arch, run `... info` under QEMU-user and assert
  it prints the expected running arch id. This is the fast CI arch-matrix check.

- [ ] Steps 1–5 as TDD: failing test → implement `run.py` → passing → commit
  `feat(emulation): qemu-user cross-arch execution smoke test`.

---

## Task 9: QEMU system-emulation end-to-end teleport demo (spike-then-build)

**Files:**
- Create: `emulation/system/` (harness), `test/teleport_e2e_test.py`

**This task begins with a bounded spike** (research doc §6; user directive: "test the options and
use whatever works best"):
- Evaluate, on this arm64 host, the cheapest reliable way to get bootable Linux guests with SSH
  for the target arches. Candidate options to try and record results for:
  1. QEMU system + prebuilt cloud images (e.g. distro cloud images) per arch.
  2. QEMU system + buildroot/OpenWrt minimal rootfs.
  3. Docker + `qemu-user-static` (binfmt) containers as "pseudo-machines" reachable over SSH.
- Pick the option that boots + accepts SSH most reliably for the most arches; document why in
  `emulation/system/README.md`. arm64 guest may use `/dev/kvm`; others use TCG.

**Then build:**
- A harness `emulation/system/up.py <arch>` that boots a guest and exposes SSH on a local port
  with a known key; `down.py` tears it down.
- `test/teleport_e2e_test.py`: bring up a guest of arch T ≠ host, run
  `go-teleport-self <ssh-target>`, then over SSH execute the freshly installed
  `~/local/bin/go-teleport-self info` and assert it reports arch T and its md5 equals
  `dist/go-teleport-self.<T>`'s md5. Mark arches that can't be emulated in this environment as
  skipped-with-reason (never silently dropped).

- [ ] Spike: try options, record findings in `emulation/system/README.md`. Commit the findings.
- [ ] Build harness + e2e test (TDD where feasible). Commit
  `feat(emulation): system-emulation SSH teleport end-to-end demo`.

---

## Task 10: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

**Jobs:**
1. `build-and-determinism`: pin Go 1.26; `go test ./...`; `uv run python build.py`;
   `uv run python -m pytest test/determinism_test.py` (byte-identical + HR4 law).
2. `qemu-user-matrix`: install `qemu-user-static` + binfmt; run `test/exec_qemu_user_test.py`
   for all present arches.
3. `system-emulation` (best-effort, per Task 9 outcome): run the e2e teleport demo for the
   arches that proved reliable; upload logs. Allowed to be a separate, possibly-slow job.

- [ ] Add workflow; push a branch; open a PR; confirm CI is green (adjust until it is).
- [ ] Commit `ci: build+determinism, qemu-user matrix, system-emulation demo`.

---

## Task 11: documentation

**Files:**
- Create: `docs/architecture.md` (format diagram, determinism proof recap, why native-stub),
  `docs/usage.md` (install, `go-teleport-self` commands, teleport walkthrough),
  `docs/testing.md` (qemu-user + system emulation, how to run locally + in CI),
  `docs/contributing.md` (reproducibility rules, adding an arch, the reserved riscv32 slot).
- Modify: `README.md` (link the docs; quickstart).

- [ ] Write docs referencing the research doc; include a real teleport transcript captured from
  Task 9. Commit `docs: architecture, usage, testing, contributing`.

---

## Self-Review (completed by planner)

- **Spec coverage:** HR1 (static native ELF, Tasks 4–7), HR2 (`CGO_ENABLED=0`, Global
  Constraints + build.py), HR3 (fatblob carries all arches, Tasks 1–5), HR4 (determinism law,
  Tasks 2 & 7), HR5 (5 arches built; riscv32 reserved slot, Tasks 1/3/5). Emulation demo (§6,
  Tasks 8–9). `go-teleport-self` (§5.6, Tasks 4/6). ✅
- **Placeholder scan:** exploratory content is confined to Task 9's explicit spike (justified by
  the user's "test the options" directive); every other task has concrete test code and
  interfaces. ✅
- **Type consistency:** `Blob`/`Slice`/`Encode`/`Decode`/`BuildCanonical`/`SplitCanonical`/
  `Reconstruct`/`FromUname`/`Table` names are used identically across Tasks 1–8. ✅
