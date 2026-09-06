package fatbuild

import (
	"bytes"
	"crypto/md5"
	"os"
	"path/filepath"
	"testing"

	"github.com/mithro/go-multi-binary/fatblob"
)

// presentArches are the buildable architectures, in canonical order.
var presentArches = []string{"386", "amd64", "arm", "arm64", "riscv64"}

// md5hex is a small helper for byte-identity comparisons.
func md5hex(b []byte) [16]byte { return md5.Sum(b) }

// TestReconstructLawSynthetic exercises the HR4 reconstruction law on synthetic
// canonicals assembled from fake native bytes, WITHOUT any cross-arch go build,
// so it runs in the default fast suite. The heavy "build twice, byte-identical"
// proof over REAL binaries lives in reprobuild_test.go (build tag `reprobuild`).
//
// Law: for every ordered pair (X, Y), Reconstruct(canonical(X), Y) must equal
// the independently assembled canonical(Y) byte-for-byte, because every
// canonical shares the identical trailing blob.
func TestReconstructLawSynthetic(t *testing.T) {
	dir := t.TempDir()
	inDir := filepath.Join(dir, "native")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Distinct, different-length fake native prefixes per arch.
	for i, arch := range presentArches {
		body := append([]byte("\x7fELF-"+arch+"-body"), bytes.Repeat([]byte{byte('a' + i)}, i+1)...)
		writeFile(t, filepath.Join(inDir, "native."+arch), body)
	}

	if _, err := Assemble(inDir, outDir, filepath.Join(outDir, "MANIFEST.json")); err != nil {
		t.Fatalf("assemble: %v", err)
	}

	canon := make(map[string][]byte, len(presentArches))
	for _, arch := range presentArches {
		data, err := os.ReadFile(filepath.Join(outDir, "go-teleport-self."+arch))
		if err != nil {
			t.Fatalf("read canonical(%s): %v", arch, err)
		}
		canon[arch] = data
	}

	for _, x := range presentArches {
		for _, y := range presentArches {
			got, err := fatblob.Reconstruct(canon[x], y)
			if err != nil {
				t.Fatalf("reconstruct(%s->%s): %v", x, y, err)
			}
			if md5hex(got) != md5hex(canon[y]) {
				t.Fatalf("reconstruct(%s->%s) != canonical(%s)", x, y, y)
			}
		}
	}
}
