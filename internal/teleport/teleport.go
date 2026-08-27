// Package teleport installs a canonical multi-architecture binary onto a remote
// host over SSH, reconstructing the exact artifact for the remote's architecture
// from the running binary's own embedded FATBLOB. Nothing is downloaded.
package teleport

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mithro/go-multi-binary/internal/archdetect"
	"github.com/mithro/go-multi-binary/internal/fatblob"
)

// Transport abstracts remote command execution and file upload so Deploy can be
// unit-tested with a fake and driven by real SSH in production.
type Transport interface {
	RunUname(ctx context.Context) (string, error)
	Put(ctx context.Context, data []byte, remotePath string) error
	Run(ctx context.Context, cmd string) (string, error)
}

// Result summarizes a successful deployment.
type Result struct {
	Arch       string
	MD5        string
	RemotePath string
	Size       int
}

// Deploy detects the remote architecture, reconstructs canonical(remoteArch)
// from image, installs it into destDir as `go-teleport-self`, makes it
// executable, and verifies the remote md5 matches the bytes sent.
func Deploy(ctx context.Context, image []byte, t Transport, destDir string) (Result, error) {
	uname, err := t.RunUname(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("detect remote arch: %w", err)
	}
	arch, err := archdetect.FromUname(uname)
	if err != nil {
		return Result{}, fmt.Errorf("remote arch %q: %w", strings.TrimSpace(uname), err)
	}

	data, err := fatblob.Reconstruct(image, arch)
	if err != nil {
		return Result{}, fmt.Errorf("reconstruct canonical(%s): %w", arch, err)
	}
	localSum := md5.Sum(data)
	localMD5 := hex.EncodeToString(localSum[:])

	remotePath := strings.TrimRight(destDir, "/") + "/go-teleport-self"

	if _, err := t.Run(ctx, "mkdir -p "+shellQuoteAllowTilde(destDir)); err != nil {
		return Result{}, fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	if err := t.Put(ctx, data, remotePath); err != nil {
		return Result{}, fmt.Errorf("upload: %w", err)
	}
	if _, err := t.Run(ctx, "chmod +x "+shellQuoteAllowTilde(remotePath)); err != nil {
		return Result{}, fmt.Errorf("chmod: %w", err)
	}

	out, err := t.Run(ctx, "md5sum "+remotePath)
	if err != nil {
		return Result{}, fmt.Errorf("remote md5sum: %w", err)
	}
	remoteMD5 := firstField(out)
	if remoteMD5 != localMD5 {
		return Result{}, fmt.Errorf("md5 mismatch: local %s != remote %q", localMD5, remoteMD5)
	}

	return Result{Arch: arch, MD5: localMD5, RemotePath: remotePath, Size: len(data)}, nil
}

func firstField(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// shellQuoteAllowTilde single-quotes a path for the remote shell but leaves a
// leading ~ unquoted so the remote shell still expands it to $HOME.
func shellQuoteAllowTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		return "~/" + shellQuote(p[2:])
	}
	if p == "~" {
		return "~"
	}
	return shellQuote(p)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SSHTransport implements Transport using the system `ssh` client. Data is
// streamed over `cat >` (no scp, no temp files). Host-key handling follows the
// project convention: never hash known_hosts.
type SSHTransport struct {
	Target  string   // user@host
	SSHArgs []string // extra ssh args (e.g. -p 2222, -i key, -o options)
}

func (s SSHTransport) ssh(ctx context.Context, stdin []byte, remoteCmd string) (string, error) {
	args := append([]string{"-o", "BatchMode=yes"}, s.SSHArgs...)
	args = append(args, s.Target, remoteCmd)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("ssh %s: %w: %s", remoteCmd, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// RunUname returns the remote `uname -m`.
func (s SSHTransport) RunUname(ctx context.Context) (string, error) {
	return s.ssh(ctx, nil, "uname -m")
}

// Run executes a remote shell command.
func (s SSHTransport) Run(ctx context.Context, remoteCmd string) (string, error) {
	return s.ssh(ctx, nil, remoteCmd)
}

// Put streams data into remotePath via `cat >`.
func (s SSHTransport) Put(ctx context.Context, data []byte, remotePath string) error {
	_, err := s.ssh(ctx, data, "cat > "+shellQuoteAllowTilde(remotePath))
	return err
}
