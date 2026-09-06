package fatbuild

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/mithro/go-multi-binary/fatblob"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestAssembleSharesIdenticalBlob(t *testing.T) {
	dir := t.TempDir()
	inDir := filepath.Join(dir, "native")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two tiny fake native binaries of different lengths.
	writeFile(t, filepath.Join(inDir, "native.386"), []byte("\x7fELF-386-body"))
	writeFile(t, filepath.Join(inDir, "native.amd64"), []byte("\x7fELF-amd64-longer-body"))

	manifest := filepath.Join(outDir, "MANIFEST.json")
	if _, err := Assemble(inDir, outDir, manifest); err != nil {
		t.Fatalf("assemble: %v", err)
	}

	c386, err := os.ReadFile(filepath.Join(outDir, "go-teleport-self.386"))
	if err != nil {
		t.Fatal(err)
	}
	cAmd, err := os.ReadFile(filepath.Join(outDir, "go-teleport-self.amd64"))
	if err != nil {
		t.Fatal(err)
	}
	// The trailing blob must be byte-identical across arches (they differ only
	// in the leading native prefix). Reconstruct 386 from the amd64 image and
	// compare to the independently written canonical(386).
	got, err := fatblob.Reconstruct(cAmd, "386")
	if err != nil {
		t.Fatalf("reconstruct 386 from amd64: %v", err)
	}
	if !bytes.Equal(got, c386) {
		t.Fatalf("reconstruct(amd64->386) != written canonical(386)")
	}
}

func TestAssembleManifestSHAMatches(t *testing.T) {
	dir := t.TempDir()
	inDir := filepath.Join(dir, "native")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(inDir, "native.amd64"), []byte("\x7fELF-amd64-body"))
	writeFile(t, filepath.Join(inDir, "native.arm64"), []byte("\x7fELF-arm64-body"))

	manifestPath := filepath.Join(outDir, "MANIFEST.json")
	m, err := Assemble(inDir, outDir, manifestPath)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, a := range m.Artifacts {
		if !a.Present {
			continue
		}
		data, err := os.ReadFile(filepath.Join(outDir, a.File))
		if err != nil {
			t.Fatalf("read artifact %s: %v", a.File, err)
		}
		sum := sha256.Sum256(data)
		if a.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("manifest sha mismatch for %s", a.Arch)
		}
	}
}
