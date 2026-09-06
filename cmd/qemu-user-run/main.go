// Command qemu-user-run runs a canonical go-teleport-self binary under QEMU
// user-mode emulation for a given arch. It replaces emulation/user/run.py.
//
// Usage:
//
//	qemu-user-run <arch> [args...]
//
// It runs dist/go-teleport-self.<arch> (natively when arch is the host arch, or
// via the qemu-<arch>[-static] interpreter otherwise) and forwards stdio.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	qemuuser "github.com/mithro/go-multi-binary/emulation/user"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: qemu-user-run <arch> [args...]")
		os.Exit(2)
	}
	arch := os.Args[1]
	binary := fmt.Sprintf("dist/go-teleport-self.%s", arch)

	cmd, err := qemuuser.Command(context.Background(), binary, arch, os.Args[2:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qemu-user-run:", err)
		os.Exit(1)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "qemu-user-run:", err)
		os.Exit(1)
	}
}
