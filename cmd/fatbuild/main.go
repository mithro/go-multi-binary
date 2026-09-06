// Command fatbuild is the reproducible multi-architecture build orchestrator.
//
// It builds the go-teleport-self program for every supported architecture with
// the Go team's reproducible recipe (CGO_ENABLED=0, -trimpath, no VCS stamping,
// stripped, empty build id), then assembles the shared FATBLOB and every
// canonical distributable plus a manifest — calling the in-repo fatblob API
// directly rather than re-invoking a separate tool. This replaces build.py.
//
// Usage:
//
//	fatbuild [--out-root DIR] [--version STR] [--repo DIR]
//
// Outputs under <out-root>/dist (default: repo root):
//
//	native/native.<arch>         bare native binaries
//	go-teleport-self.<arch>      canonical distributables (native ++ blob)
//	MANIFEST.json                sizes + sha256 + md5
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mithro/go-multi-binary/internal/fatbuild"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatbuild:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("fatbuild", flag.ExitOnError)
	repo := fs.String("repo", ".", "repository root to build from")
	outRoot := fs.String("out-root", "", "root under which dist/ is written (default: --repo)")
	version := fs.String("version", "", "version string to inject (default: VERSION env or git describe)")
	fs.Parse(os.Args[1:])

	repoDir, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	root := *outRoot
	if root == "" {
		root = repoDir
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}

	ver := *version
	if ver == "" {
		ver = os.Getenv("VERSION")
	}
	if ver == "" {
		ver = fatbuild.GitDescribe(repoDir)
	}

	fmt.Fprintf(os.Stderr, "go: %s\n", goVersion())
	fmt.Fprintf(os.Stderr, "version: %s\n", ver)
	fmt.Fprintf(os.Stderr, "out: %s\n", filepath.Join(root, "dist"))

	m, err := fatbuild.BuildAll(repoDir, root, ver)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "\n=== MANIFEST ===")
	fmt.Fprintf(os.Stderr, "blob: %d bytes  sha256:%s\n", m.BlobSize, truncate(m.BlobSHA256, 16))
	for _, a := range m.Artifacts {
		if a.Present {
			fmt.Fprintf(os.Stderr, "  %-8s %10d bytes  md5:%s  sha256:%s\n",
				a.Arch, a.Size, a.MD5, truncate(a.SHA256, 16))
		} else {
			fmt.Fprintf(os.Stderr, "  %-8s reserved (no binary)\n", a.Arch)
		}
	}
	return nil
}

func goVersion() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
