package fatblob

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

// rawNative fabricates a compressible stand-in for a native ELF: the ELF magic
// followed by an arch-specific, highly-repetitive body (so xz actually shrinks
// it and the CodecXZ path is exercised, not the CodecNone fallback).
func rawNative(arch string) []byte {
	body := strings.Repeat("native-"+arch+"-", 4096)
	return append([]byte{0x7f, 'E', 'L', 'F'}, body...)
}

// rawPresentBlob builds an uncompressed blob of fabricated natives plus the
// reserved riscv32 slot.
func rawPresentBlob() Blob {
	present := []string{"386", "amd64", "arm", "arm64", "riscv64"}
	var s []Slice
	for _, a := range present {
		s = append(s, Slice{Arch: a, Status: StatusPresent, Data: rawNative(a)})
	}
	s = append(s, Slice{Arch: "riscv32", Status: StatusReserved})
	return Blob{Slices: s}
}

func rawHead(raw Blob, arch string) []byte {
	for _, s := range raw.Slices {
		if s.Arch == arch {
			return s.Data
		}
	}
	return nil
}

// The core reconstruction law, now with a COMPRESSED shared blob: for every
// present host H and target T, Reconstruct(canonical(H), T) must be byte-exact
// with the independently built canonical(T). The head of the result must be the
// raw, uncompressed native for T, beginning with the ELF magic.
func TestCompressedReconstructLawAllPairs(t *testing.T) {
	raw := rawPresentBlob()
	blob, err := CompressBlob(raw)
	if err != nil {
		t.Fatalf("CompressBlob: %v", err)
	}

	// The compressed blob must actually be smaller than the raw one.
	rawEnc, _ := Encode(raw)
	cmpEnc, _ := Encode(blob)
	if len(cmpEnc) >= len(rawEnc) {
		t.Fatalf("compressed blob %d not smaller than raw %d", len(cmpEnc), len(rawEnc))
	}

	present := []string{"386", "amd64", "arm", "arm64", "riscv64"}

	// Codec/rawLen bookkeeping.
	for _, s := range blob.Slices {
		if s.Arch == "riscv32" {
			if s.Codec != CodecNone || s.RawLen != 0 || len(s.Data) != 0 {
				t.Fatalf("reserved slice must stay codec=none/empty, got %+v", s)
			}
			continue
		}
		if s.Codec != CodecXZ {
			t.Fatalf("present slice %s codec = %d, want CodecXZ", s.Arch, s.Codec)
		}
		if s.RawLen != uint64(len(rawHead(raw, s.Arch))) {
			t.Fatalf("present slice %s rawLen = %d, want %d", s.Arch, s.RawLen, len(rawHead(raw, s.Arch)))
		}
	}

	canonicalOf := func(arch string) []byte {
		img, err := BuildCanonical(rawHead(raw, arch), blob)
		if err != nil {
			t.Fatalf("BuildCanonical(%s): %v", arch, err)
		}
		return img
	}

	for _, h := range present {
		img := canonicalOf(h)
		for _, target := range present {
			got, err := Reconstruct(img, target)
			if err != nil {
				t.Fatalf("Reconstruct(%s->%s): %v", h, target, err)
			}
			want := canonicalOf(target)
			if !bytes.Equal(got, want) {
				t.Fatalf("law broken %s->%s: sha %x != %x", h, target,
					sha256.Sum256(got), sha256.Sum256(want))
			}
			head := rawHead(raw, target)
			if !bytes.Equal(got[:len(head)], head) {
				t.Fatalf("%s->%s: head is not the raw native", h, target)
			}
			if !bytes.HasPrefix(got, []byte{0x7f, 'E', 'L', 'F'}) {
				t.Fatalf("%s->%s: head missing ELF magic", h, target)
			}
		}
	}
}

func TestCompressedEncodeDeterministic(t *testing.T) {
	raw := rawPresentBlob()
	b1, err := CompressBlob(raw)
	if err != nil {
		t.Fatalf("CompressBlob: %v", err)
	}
	b2, err := CompressBlob(raw)
	if err != nil {
		t.Fatalf("CompressBlob: %v", err)
	}
	e1, _ := Encode(b1)
	e2, _ := Encode(b2)
	if !bytes.Equal(e1, e2) {
		t.Fatalf("compressed encode not deterministic")
	}
}

// A corrupted compressed slice must yield a clear error, never a panic.
func TestReconstructCorruptCompressed(t *testing.T) {
	raw := rawPresentBlob()
	blob, err := CompressBlob(raw)
	if err != nil {
		t.Fatalf("CompressBlob: %v", err)
	}
	img, err := BuildCanonical(rawHead(raw, "amd64"), blob)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	// Flip a byte deep inside the appended blob (well past the raw head).
	corrupt := append([]byte(nil), img...)
	corrupt[len(corrupt)-32] ^= 0xFF
	if _, err := Reconstruct(corrupt, "riscv64"); err == nil {
		t.Fatalf("expected error reconstructing from corrupted compressed slice")
	}
}

// A recorded rawLen that disagrees with the true decompressed size must be
// caught by Decompress.
func TestDecompressLengthMismatch(t *testing.T) {
	codec, data, err := Compress(rawNative("amd64"))
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if codec != CodecXZ {
		t.Fatalf("expected CodecXZ, got %d", codec)
	}
	if _, err := Decompress(codec, data, 999999); err == nil {
		t.Fatalf("expected length-mismatch error")
	}
}

// A truncated/short xz stream must error, not panic.
func TestDecompressShortStream(t *testing.T) {
	codec, data, err := Compress(rawNative("arm64"))
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if _, err := Decompress(codec, data[:len(data)/2], uint64(len(rawNative("arm64")))); err == nil {
		t.Fatalf("expected error on truncated xz stream")
	}
}

func TestDecodeRejectsV1Magic(t *testing.T) {
	enc, err := Encode(rawPresentBlob())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	v1 := append([]byte(nil), enc...)
	copy(v1[:magicLen], []byte(MagicV1))
	if _, err := Decode(v1); err == nil {
		t.Fatalf("expected error decoding a \\x01-magic blob with \\x02 reader")
	}
}
