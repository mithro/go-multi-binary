package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	if err := assemble(inDir, outDir, manifest); err != nil {
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
	// in the leading native prefix). Compare the shared suffix.
	minLen := len(c386)
	if len(cAmd) < minLen {
		minLen = len(cAmd)
	}
	// Find the blob by locating the shared trailer: the blobs are equal, so the
	// last K bytes of both must match for K = min blob length. Simplest robust
	// check: reconstruct 386 from amd64 image and compare to the written 386.
	got, err := reconstructForTest(cAmd, "386")
	if err != nil {
		t.Fatalf("reconstruct 386 from amd64: %v", err)
	}
	if !bytes.Equal(got, c386) {
		t.Fatalf("reconstruct(amd64->386) != written canonical(386)")
	}
	_ = minLen
}

func TestAssembleManifestSHAMatches(t *testing.T) {
	dir := t.TempDir()
	inDir := filepath.Join(dir, "native")
	outDir := filepath.Join(dir, "out")
	os.MkdirAll(inDir, 0o755)
	writeFile(t, filepath.Join(inDir, "native.amd64"), []byte("\x7fELF-amd64-body"))
	writeFile(t, filepath.Join(inDir, "native.arm64"), []byte("\x7fELF-arm64-body"))

	manifestPath := filepath.Join(outDir, "MANIFEST.json")
	if err := assemble(inDir, outDir, manifestPath); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest json: %v", err)
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
