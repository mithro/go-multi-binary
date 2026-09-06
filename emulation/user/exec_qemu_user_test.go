//go:build qemu

// Cross-architecture execution smoke tests under QEMU user-mode. Gated behind
// the `qemu` build tag; individual arches skip (with a reason) when no emulator
// is installed, so the suite still passes on an unprovisioned host.
//
// For every present architecture we build the real canonical binary and, under
// emulation, assert:
//   - `info` reports the correct running arch and reads its own FATBLOB,
//   - the executed reconstruct law holds under emulation: running
//     canonical(host) `extract <target>` yields bytes byte-identical to
//     canonical(target).
//
// Run: go test -tags qemu ./emulation/user/
//
// This replaces test/exec_qemu_user_test.py.
package qemuuser_test

import (
	"context"
	"crypto/md5"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qemuuser "github.com/mithro/go-multi-binary/emulation/user"
	"github.com/mithro/go-multi-binary/internal/fatbuild"
)

var presentArches = []string{"386", "amd64", "arm", "arm64", "riscv64"}

// distDir is the assembled dist/ directory, built once for the package.
var distDir string

func TestMain(m *testing.M) {
	repo := findRepoRoot()
	outRoot, err := os.MkdirTemp("", "qemu-user-build")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(outRoot)
	if _, err := fatbuild.BuildAll(repo, outRoot, "test-qemu"); err != nil {
		panic("build all arches: " + err.Error())
	}
	distDir = filepath.Join(outRoot, "dist")
	os.Exit(m.Run())
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("could not find module root (go.mod)")
		}
		dir = parent
	}
}

func TestInfoReportsArch(t *testing.T) {
	for _, arch := range presentArches {
		arch := arch
		t.Run(arch, func(t *testing.T) {
			if !qemuuser.CanRun(arch) {
				t.Skipf("no emulator available for %s", arch)
			}
			bin := filepath.Join(distDir, "go-teleport-self."+arch)
			out, err := qemuuser.Output(context.Background(), bin, arch, "info")
			if err != nil {
				t.Fatalf("run info: %v\n%s", err, out)
			}
			s := string(out)
			if !strings.Contains(s, "running arch:   "+arch) {
				t.Fatalf("info did not report arch %s:\n%s", arch, s)
			}
			for _, other := range presentArches {
				if !strings.Contains(s, other) {
					t.Fatalf("info missing arch %s:\n%s", other, s)
				}
			}
		})
	}
}

func TestExecutedReconstructLawEmulated(t *testing.T) {
	for _, host := range presentArches {
		host := host
		t.Run(host, func(t *testing.T) {
			if !qemuuser.CanRun(host) {
				t.Skipf("no emulator available for %s", host)
			}
			hostBin := filepath.Join(distDir, "go-teleport-self."+host)
			for _, target := range presentArches {
				out := filepath.Join(t.TempDir(), host+"-to-"+target)
				if o, err := qemuuser.Output(context.Background(), hostBin, host, "extract", target, out); err != nil {
					t.Fatalf("extract %s->%s: %v\n%s", host, target, err, o)
				}
				got, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				want, err := os.ReadFile(filepath.Join(distDir, "go-teleport-self."+target))
				if err != nil {
					t.Fatal(err)
				}
				if md5.Sum(got) != md5.Sum(want) {
					t.Fatalf("emulated reconstruct(%s->%s) differs from canonical(%s)", host, target, target)
				}
			}
		})
	}
}
