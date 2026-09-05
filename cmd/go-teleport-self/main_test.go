package main

import (
	"testing"

	"github.com/mithro/go-multi-binary/fatblob"
)

// syntheticCanonical builds a canonical image for "amd64" with 5 present arches
// plus the reserved riscv32 slot, for tests that need an attached blob.
func syntheticCanonical(t *testing.T) []byte {
	t.Helper()
	blob := fatblob.Blob{Slices: []fatblob.Slice{
		{Arch: "386", Data: []byte("\x7fELF-386")},
		{Arch: "amd64", Data: []byte("\x7fELF-amd64")},
		{Arch: "arm", Data: []byte("\x7fELF-arm")},
		{Arch: "arm64", Data: []byte("\x7fELF-arm64")},
		{Arch: "riscv64", Data: []byte("\x7fELF-rv64")},
		{Arch: "riscv32", Status: fatblob.StatusReserved, Data: nil},
	}}
	img, err := fatblob.BuildCanonical([]byte("\x7fELF-amd64"), blob)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	return img
}

func TestInventoryCountsPresentArches(t *testing.T) {
	entries, err := inventory(syntheticCanonical(t))
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
	if len(entries) != 6 {
		t.Fatalf("total entries = %d, want 6 (incl. reserved riscv32)", len(entries))
	}
}

func TestInventoryComputesSHA(t *testing.T) {
	entries, err := inventory(syntheticCanonical(t))
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	for _, e := range entries {
		if e.Present && (e.Size == 0 || e.SHA256 == "") {
			t.Fatalf("present arch %q missing size/sha", e.Arch)
		}
		if !e.Present && e.Size != 0 {
			t.Fatalf("reserved arch %q should have size 0", e.Arch)
		}
	}
}

func TestInventoryErrorsWithoutBlob(t *testing.T) {
	if _, err := inventory([]byte("bare native elf, no blob")); err == nil {
		t.Fatalf("expected error: no attached FATBLOB")
	}
}
