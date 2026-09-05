package fatblob

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// presentBlob builds a blob whose slices are mini stand-ins for native ELF
// images, plus the reserved riscv32 slot.
func presentBlob() Blob {
	return Blob{Slices: []Slice{
		{Arch: "386", Data: []byte("\x7fELF-386-native")},
		{Arch: "amd64", Data: []byte("\x7fELF-amd64-native-x")},
		{Arch: "arm", Data: []byte("\x7fELF-arm")},
		{Arch: "arm64", Data: []byte("\x7fELF-arm64-native")},
		{Arch: "riscv64", Data: []byte("\x7fELF-rv64")},
		{Arch: "riscv32", Status: StatusReserved, Data: nil},
	}}
}

func canonicalOf(t *testing.T, blob Blob, arch string) []byte {
	t.Helper()
	var native []byte
	for _, s := range blob.Slices {
		if s.Arch == arch {
			native = s.Data
		}
	}
	img, err := BuildCanonical(native, blob)
	if err != nil {
		t.Fatalf("BuildCanonical(%s): %v", arch, err)
	}
	return img
}

// The core HR4 law: for every present host H and target T,
// Reconstruct(canonical(H), T) must equal canonical(T) byte-for-byte.
func TestReconstructLawAllPairs(t *testing.T) {
	blob := presentBlob()
	present := []string{"386", "amd64", "arm", "arm64", "riscv64"}
	for _, h := range present {
		img := canonicalOf(t, blob, h)

		native, gotBlob, err := SplitCanonical(img)
		if err != nil {
			t.Fatalf("SplitCanonical(%s): %v", h, err)
		}
		if len(gotBlob.Slices) != len(blob.Slices) {
			t.Fatalf("blob slice count mismatch after split from %s", h)
		}
		if !bytes.Equal(native, canonicalOf(t, blob, h)[:len(native)]) {
			t.Fatalf("split native prefix mismatch for %s", h)
		}

		for _, target := range present {
			got, err := Reconstruct(img, target)
			if err != nil {
				t.Fatalf("Reconstruct(%s->%s): %v", h, target, err)
			}
			want := canonicalOf(t, blob, target)
			if !bytes.Equal(got, want) {
				t.Fatalf("law broken %s->%s: sha %x != %x",
					h, target, sha256.Sum256(got), sha256.Sum256(want))
			}
		}
	}
}

func TestReconstructReservedFails(t *testing.T) {
	img := canonicalOf(t, presentBlob(), "amd64")
	if _, err := Reconstruct(img, "riscv32"); err == nil {
		t.Fatalf("expected error reconstructing reserved riscv32 slot")
	}
}

func TestReconstructUnknownFails(t *testing.T) {
	img := canonicalOf(t, presentBlob(), "amd64")
	if _, err := Reconstruct(img, "sparc64"); err == nil {
		t.Fatalf("expected error reconstructing unknown arch")
	}
}

func TestSplitRejectsNonCanonical(t *testing.T) {
	if _, _, err := SplitCanonical([]byte("not an image")); err == nil {
		t.Fatalf("expected error splitting bytes with no trailer")
	}
}
