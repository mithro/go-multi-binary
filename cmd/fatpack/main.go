// Command fatpack is the build-time tool that assembles the FATBLOB from bare
// per-architecture native binaries and emits each canonical distributable plus
// a manifest of sizes and checksums.
//
// Usage:
//
//	fatpack assemble --in dist/native --out dist --manifest dist/MANIFEST.json
//
// It reads native binaries named `native.<arch>` from --in, builds the shared
// FATBLOB (riscv32 included as a reserved placeholder), writes
// `go-teleport-self.<arch>` = canonical(arch) for every present arch to --out,
// and writes the manifest. Deterministic: identical inputs -> identical outputs.
package main

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mithro/go-multi-binary/archdetect"
	"github.com/mithro/go-multi-binary/fatblob"
)

// ArtifactInfo describes one emitted canonical file (or a reserved slot).
type ArtifactInfo struct {
	Arch    string `json:"arch"`
	File    string `json:"file,omitempty"`
	Present bool   `json:"present"`
	Size    int    `json:"size"`
	SHA256  string `json:"sha256,omitempty"`
	MD5     string `json:"md5,omitempty"`
}

// Manifest is the machine-readable summary written alongside the artifacts.
type Manifest struct {
	Format     string         `json:"format"`
	BlobSize   int            `json:"blob_size"`
	BlobSHA256 string         `json:"blob_sha256"`
	Artifacts  []ArtifactInfo `json:"artifacts"`
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "assemble" {
		fmt.Fprintln(os.Stderr, "usage: fatpack assemble --in DIR --out DIR --manifest FILE")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("assemble", flag.ExitOnError)
	in := fs.String("in", "dist/native", "directory of native.<arch> binaries")
	out := fs.String("out", "dist", "output directory for canonical artifacts")
	manifest := fs.String("manifest", "dist/MANIFEST.json", "manifest output path")
	fs.Parse(os.Args[2:])

	if err := assemble(*in, *out, *manifest); err != nil {
		fmt.Fprintln(os.Stderr, "fatpack:", err)
		os.Exit(1)
	}
}

func assemble(inDir, outDir, manifestPath string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Build the blob in the fixed canonical order.
	var slices []fatblob.Slice
	for _, arch := range fatblob.FixedArchOrder() {
		info, _ := archdetect.Lookup(arch)
		if !info.Supported {
			// Reserved slot (riscv32): no binary embedded.
			slices = append(slices, fatblob.Slice{Arch: arch, Status: fatblob.StatusReserved})
			continue
		}
		path := filepath.Join(inDir, "native."+arch)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Partial build: report and skip (never silently drop).
				fmt.Fprintf(os.Stderr, "fatpack: skipping %s (no %s)\n", arch, path)
				continue
			}
			return err
		}
		slices = append(slices, fatblob.Slice{Arch: arch, Status: fatblob.StatusPresent, Data: data})
	}

	// Compress every present slice ONCE. The resulting blob is the identical
	// trailer appended to every canonical(*); each canonical's head stays the
	// raw, kernel-loadable native (taken from `slices` below), so the images
	// differ only in that uncompressed head.
	rawBlob := fatblob.Blob{Slices: slices}
	blob, err := fatblob.CompressBlob(rawBlob)
	if err != nil {
		return err
	}
	blobBytes, err := fatblob.Encode(blob)
	if err != nil {
		return err
	}
	blobSum := sha256.Sum256(blobBytes)

	m := Manifest{
		Format:     fatblob.Magic,
		BlobSize:   len(blobBytes),
		BlobSHA256: hex.EncodeToString(blobSum[:]),
	}

	for _, s := range slices {
		if s.Status != fatblob.StatusPresent || len(s.Data) == 0 {
			m.Artifacts = append(m.Artifacts, ArtifactInfo{Arch: s.Arch, Present: false})
			continue
		}
		canonical, err := fatblob.BuildCanonical(s.Data, blob)
		if err != nil {
			return err
		}
		file := "go-teleport-self." + s.Arch
		if err := os.WriteFile(filepath.Join(outDir, file), canonical, 0o755); err != nil {
			return err
		}
		sum := sha256.Sum256(canonical)
		msum := md5.Sum(canonical)
		m.Artifacts = append(m.Artifacts, ArtifactInfo{
			Arch:    s.Arch,
			File:    file,
			Present: true,
			Size:    len(canonical),
			SHA256:  hex.EncodeToString(sum[:]),
			MD5:     hex.EncodeToString(msum[:]),
		})
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(manifestPath, raw, 0o644)
}

// reconstructForTest is a thin helper used by tests to reconstruct a target
// canonical image from another arch's canonical image.
func reconstructForTest(image []byte, target string) ([]byte, error) {
	return fatblob.Reconstruct(image, target)
}
