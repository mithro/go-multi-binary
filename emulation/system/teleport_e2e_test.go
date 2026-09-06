//go:build e2e

// End-to-end teleport demo under QEMU *system* emulation. Gated behind the `e2e`
// build tag; it skips (with a reason) when the required emulator or base image
// is absent, so it never fails on an unprovisioned host.
//
// Boots a full guest of a target architecture (different from the host), then
// runs the REAL go-teleport-self tool to install itself into the guest's
// ~/local/bin over real SSH — downloading nothing — and verifies that the
// freshly installed, guest-native binary runs and reports the target
// architecture with a matching md5.
//
// Run: go test -tags e2e ./emulation/system/
// Override target: TELEPORT_E2E_ARCH=arm64 go test -tags e2e ./emulation/system/
//
// This replaces test/teleport_e2e_test.py.
package qemusystem_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	qemusystem "github.com/mithro/go-multi-binary/emulation/system"
	"github.com/mithro/go-multi-binary/internal/fatbuild"
)

func findRepoRoot(t *testing.T) string {
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

func md5File(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func TestTeleportInstallsAndRunsOnForeignGuest(t *testing.T) {
	targetArch := os.Getenv("TELEPORT_E2E_ARCH")
	if targetArch == "" {
		targetArch = "amd64"
	}

	repo := findRepoRoot(t)
	// Point the harness at the repo's images directory.
	qemusystem.Images = filepath.Join(repo, "emulation", "system", "images")

	if ok, why := qemusystem.Available(targetArch); !ok {
		t.Skipf("system emulation for %s unavailable: %s", targetArch, why)
	}

	// Build every arch so we have both the host-native tool and canonical(target).
	outRoot := t.TempDir()
	if _, err := fatbuild.BuildAll(repo, outRoot, "test-e2e"); err != nil {
		t.Fatalf("build: %v", err)
	}
	dist := filepath.Join(outRoot, "dist")

	hostArch := qemusystem.HostArch()
	tool := filepath.Join(dist, "go-teleport-self."+hostArch)
	if _, err := os.Stat(tool); err != nil {
		t.Fatalf("host-native tool missing: %v", err)
	}

	bootTimeout := 900 * time.Second
	if v := os.Getenv("TELEPORT_E2E_BOOT_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			bootTimeout = time.Duration(secs) * time.Second
		}
	}

	ctx := context.Background()
	g, err := qemusystem.Start(ctx, targetArch, filepath.Join(t.TempDir(), "guest"), bootTimeout)
	if err != nil {
		t.Fatalf("boot guest: %v", err)
	}
	defer g.Stop()

	// 1. Teleport: real SSH, reconstruct canonical(target), install to ~/local/bin.
	teleArgs := append([]string{g.Target(), "--"}, g.SSHArgs...)
	out, err := exec.CommandContext(ctx, tool, teleArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("teleport failed: %v\n%s", err, out)
	}
	t.Logf("teleport output: %s", strings.TrimSpace(string(out)))

	// 2. Run the freshly installed, guest-native binary over SSH.
	info, err := exec.CommandContext(ctx, "ssh", append(g.SSHArgs, g.Target(), "~/local/bin/go-teleport-self info")...).CombinedOutput()
	if err != nil {
		t.Fatalf("remote info failed: %v\n%s", err, info)
	}
	t.Logf("remote info:\n%s", info)
	if !strings.Contains(string(info), "running arch:   "+targetArch) {
		t.Fatalf("remote info did not report arch %s:\n%s", targetArch, info)
	}

	// 3. The installed bytes must equal canonical(target) exactly (HR4).
	sumOut, err := exec.CommandContext(ctx, "ssh", append(g.SSHArgs, g.Target(), "md5sum ~/local/bin/go-teleport-self")...).Output()
	if err != nil {
		t.Fatalf("remote md5sum: %v", err)
	}
	remoteMD5 := strings.Fields(string(sumOut))[0]
	want := md5File(t, filepath.Join(dist, "go-teleport-self."+targetArch))
	if remoteMD5 != want {
		t.Fatalf("installed md5 %s != canonical(%s) %s", remoteMD5, targetArch, want)
	}
}
