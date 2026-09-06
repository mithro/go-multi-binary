//go:build reprobuild

// Heavy determinism proof over REAL cross-arch binaries. Gated behind the
// `reprobuild` build tag because a cold cross-arch build of five architectures
// is slow; the default suite (determinism_test.go) proves the reconstruction
// law on synthetic canonicals instead.
//
// Run all present arches:
//
//	go test -tags reprobuild ./internal/fatbuild/
//
// Limit to a subset (e.g. to demonstrate the mechanism quickly):
//
//	FATBUILD_TEST_ARCHES=amd64,arm64 go test -tags reprobuild ./internal/fatbuild/
package fatbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mithro/go-multi-binary/archdetect"
	"github.com/mithro/go-multi-binary/fatblob"
)

// repoRoot walks up from the package directory to the module root (the dir
// holding go.mod), so `go build ./cmd/go-teleport-self` resolves correctly.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (go.mod)")
		}
		dir = parent
	}
}

// selectedArches returns the arches to build, honoring FATBUILD_TEST_ARCHES.
func selectedArches(t *testing.T) []string {
	if v := strings.TrimSpace(os.Getenv("FATBUILD_TEST_ARCHES")); v != "" {
		return strings.Split(v, ",")
	}
	return presentArches
}

// buildSet builds the given arches into outRoot/dist and assembles the shared
// blob + canonicals. Returns the dist directory.
func buildSet(t *testing.T, repo, outRoot, version string, arches []string) string {
	t.Helper()
	dist := filepath.Join(outRoot, "dist")
	nativeDir := filepath.Join(dist, "native")
	for _, id := range arches {
		info, ok := archdetect.Lookup(id)
		if !ok || !info.Supported {
			t.Fatalf("arch %q is not buildable", id)
		}
		if _, err := BuildNative(info, repo, nativeDir, version); err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
	}
	if _, err := Assemble(nativeDir, dist, filepath.Join(dist, "MANIFEST.json")); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return dist
}

// TestReproducibleBuilds builds the selected arches twice and proves:
//  1. byte-identical canonical(arch) across the two independent builds,
//  2. every canonical carries the identical trailing FATBLOB,
//  3. the HR4 law: Reconstruct(canonical(X), Y) == independently built canonical(Y).
func TestReproducibleBuilds(t *testing.T) {
	repo := repoRoot(t)
	arches := selectedArches(t)
	const version = "test-repro" // fixed so both builds inject the same string

	distA := buildSet(t, repo, filepath.Join(t.TempDir(), "a"), version, arches)
	distB := buildSet(t, repo, filepath.Join(t.TempDir(), "b"), version, arches)

	readCanon := func(dist, arch string) []byte {
		data, err := os.ReadFile(filepath.Join(dist, "go-teleport-self."+arch))
		if err != nil {
			t.Fatalf("read canonical(%s) in %s: %v", arch, dist, err)
		}
		return data
	}

	// 1. Reproducible: build A == build B for every arch.
	canonA := map[string][]byte{}
	for _, arch := range arches {
		a := readCanon(distA, arch)
		b := readCanon(distB, arch)
		if md5hex(a) != md5hex(b) {
			t.Fatalf("%s not reproducible across two builds", arch)
		}
		canonA[arch] = a
	}

	// 2. Shared blob identical across arches within one build.
	ref := arches[0]
	_, refBlob, err := fatblob.SplitCanonical(canonA[ref])
	if err != nil {
		t.Fatalf("split canonical(%s): %v", ref, err)
	}
	refEnc, err := fatblob.Encode(refBlob)
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range arches {
		_, blob, err := fatblob.SplitCanonical(canonA[arch])
		if err != nil {
			t.Fatalf("split canonical(%s): %v", arch, err)
		}
		enc, err := fatblob.Encode(blob)
		if err != nil {
			t.Fatal(err)
		}
		if md5hex(enc) != md5hex(refEnc) {
			t.Fatalf("blob for %s differs from %s's blob", arch, ref)
		}
	}

	// 3. HR4 law over real binaries.
	for _, x := range arches {
		for _, y := range arches {
			got, err := fatblob.Reconstruct(canonA[x], y)
			if err != nil {
				t.Fatalf("reconstruct(%s->%s): %v", x, y, err)
			}
			if md5hex(got) != md5hex(canonA[y]) {
				t.Fatalf("reconstruct(%s->%s) != canonical(%s)", x, y, y)
			}
		}
	}
}
