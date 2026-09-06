// Package qemuuser runs a canonical go-teleport-self binary under QEMU
// user-mode emulation.
//
// QEMU user-mode ("qemu-<arch>") runs a single foreign-architecture Linux binary
// directly on the host by translating instructions and forwarding syscalls — no
// guest kernel, no boot. This is the fast path for verifying that each arch's
// binary executes and can read its own appended FATBLOB. (The full SSH teleport
// demo uses system emulation instead; see emulation/system.)
//
// On a host where binfmt_misc is registered for QEMU (e.g. qemu-user-static), a
// foreign binary can be exec'd directly and the kernel invokes the interpreter.
// We instead invoke the interpreter explicitly, which is more portable across CI
// environments and does not depend on binfmt registration.
//
// This replaces emulation/user/run.py.
package qemuuser

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/mithro/go-multi-binary/archdetect"
)

// qemuInterp maps an arch id to its QEMU user-mode interpreter binary.
var qemuInterp = map[string]string{
	"386":     "qemu-i386",
	"amd64":   "qemu-x86_64",
	"arm":     "qemu-arm",
	"arm64":   "qemu-aarch64",
	"riscv64": "qemu-riscv64",
	"riscv32": "qemu-riscv32",
}

// HostArch reports the running program's canonical arch id.
func HostArch() string { return archdetect.Current() }

// InterpreterFor returns the argv prefix to run a binary of arch and whether it
// is runnable here. A native arch yields an empty prefix (run directly); a
// foreign arch yields the qemu interpreter if installed (either `qemu-<arch>` or
// `qemu-<arch>-static`), otherwise runnable is false.
func InterpreterFor(arch string) (prefix []string, runnable bool) {
	if arch == HostArch() {
		return nil, true
	}
	base, ok := qemuInterp[arch]
	if !ok {
		return nil, false
	}
	for _, name := range []string{base, base + "-static"} {
		if p, err := exec.LookPath(name); err == nil {
			return []string{p}, true
		}
	}
	return nil, false
}

// CanRun reports whether a binary of arch can be executed here (natively or via
// an installed QEMU interpreter).
func CanRun(arch string) bool {
	_, ok := InterpreterFor(arch)
	return ok
}

// Command builds the *exec.Cmd that runs binary (a canonical of arch) with args,
// prefixed by the QEMU interpreter when arch is foreign. The caller wires up
// stdout/stderr and runs it. It errors when arch is not runnable here.
func Command(ctx context.Context, binary, arch string, args ...string) (*exec.Cmd, error) {
	prefix, ok := InterpreterFor(arch)
	if !ok {
		return nil, fmt.Errorf("cannot run %s: no native support and no qemu interpreter", arch)
	}
	argv := append(append([]string{}, prefix...), binary)
	argv = append(argv, args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
}

// Output runs binary (a canonical of arch) with args and returns combined
// stdout. It errors when arch is not runnable or the process fails.
func Output(ctx context.Context, binary, arch string, args ...string) ([]byte, error) {
	cmd, err := Command(ctx, binary, arch, args...)
	if err != nil {
		return nil, err
	}
	return cmd.Output()
}
