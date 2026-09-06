// Package fatbuild is the reproducible multi-architecture build orchestrator and
// the FATBLOB assembler, shared by the fatpack and fatbuild commands and by the
// determinism test. It replaces the former build.py + cmd/fatpack split: the
// native binaries are produced by shelling out to `go build` with the Go team's
// reproducible recipe, then the shared FATBLOB and every canonical distributable
// are assembled in-process via the fatblob API (never by re-invoking a tool).
//
// Determinism inputs that MUST be held fixed for bit-identical output across
// machines (see docs/research/multi-arch-binary-approaches.md §5.4):
//   - the exact Go toolchain version,
//   - the build flags in BuildNative,
//   - the injected version string (VERSION env / --version, default git describe).
package fatbuild

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// GitDescribe returns `git describe --tags --always --dirty` run in repoDir, or
// "dev" when git is unavailable or the command fails. This is the default
// version string injected into the build (overridable via VERSION / --version).
func GitDescribe(repoDir string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "dev"
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return "dev"
}

// BuildNative builds the go-teleport-self program for a single supported
// architecture with the reproducible recipe (CGO_ENABLED=0, -trimpath, no VCS
// stamping, stripped, empty build id) and writes it to
// <outDir>/native.<arch>. It shells out to `go build`; the toolchain is Go.
func BuildNative(info archdetect.ArchInfo, repoDir, outDir, version string) (string, error) {
	if !info.Supported {
		return "", fmt.Errorf("fatbuild: arch %q is not buildable (reserved slot)", info.ID)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(outDir, "native."+info.ID)

	env := append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH="+info.GOARCH,
	)
	if info.GOARM != "" {
		env = append(env, "GOARM="+info.GOARM)
	} else {
		env = append(env, "GOARM=") // clear any inherited GOARM
	}

	ldflags := "-s -w -buildid= -X main.version=" + version
	cmd := exec.Command("go", "build",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags", ldflags,
		"-o", outPath,
		"./cmd/go-teleport-self",
	)
	cmd.Dir = repoDir
	cmd.Env = env
	cmd.Stdout = os.Stderr // keep stdout clean for machine-readable callers
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build %s (GOARCH=%s GOARM=%s): %w", info.ID, info.GOARCH, info.GOARM, err)
	}
	return outPath, nil
}

// BuildAll builds every supported architecture into <outRoot>/dist/native and
// then assembles the shared FATBLOB, every canonical distributable, and the
// manifest under <outRoot>/dist. It returns the manifest. Progress is written to
// stderr so a caller can still capture a clean stdout.
//
// Outputs under <outRoot>/dist:
//
//	native/native.<arch>         bare native binaries
//	go-teleport-self.<arch>      canonical distributables (native ++ shared blob)
//	MANIFEST.json                sizes + sha256 + md5
func BuildAll(repoDir, outRoot, version string) (Manifest, error) {
	dist := filepath.Join(outRoot, "dist")
	nativeDir := filepath.Join(dist, "native")
	manifestPath := filepath.Join(dist, "MANIFEST.json")

	for _, info := range archdetect.Table() {
		if !info.Supported {
			continue
		}
		fmt.Fprintf(os.Stderr, "[build] %-8s GOARCH=%s GOARM=%s\n", info.ID, info.GOARCH, orDash(info.GOARM))
		if _, err := BuildNative(info, repoDir, nativeDir, version); err != nil {
			return Manifest{}, err
		}
	}

	fmt.Fprintln(os.Stderr, "[pack] assembling FATBLOB + canonical artifacts")
	return Assemble(nativeDir, dist, manifestPath)
}

// Assemble reads native.<arch> binaries from inDir, builds the shared FATBLOB
// (riscv32 included as a reserved placeholder), writes go-teleport-self.<arch> =
// canonical(arch) for every present arch to outDir, and writes the manifest. It
// is deterministic: identical inputs yield byte-identical outputs.
func Assemble(inDir, outDir, manifestPath string) (Manifest, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Manifest{}, err
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
				fmt.Fprintf(os.Stderr, "fatbuild: skipping %s (no %s)\n", arch, path)
				continue
			}
			return Manifest{}, err
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
		return Manifest{}, err
	}
	blobBytes, err := fatblob.Encode(blob)
	if err != nil {
		return Manifest{}, err
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
			return Manifest{}, err
		}
		file := "go-teleport-self." + s.Arch
		if err := os.WriteFile(filepath.Join(outDir, file), canonical, 0o755); err != nil {
			return Manifest{}, err
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
		return Manifest{}, err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
