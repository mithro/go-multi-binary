// Package qemusystem is a QEMU *system* emulation harness: boot a full guest of
// a chosen architecture with a real SSH server, for the end-to-end teleport demo.
//
// Unlike QEMU user-mode (emulation/user, which runs a single foreign binary with
// a shared host kernel), system emulation boots a complete guest kernel and
// userland. A binary installed into that guest by go-teleport-self is native to
// the guest's architecture and runs there directly — exactly the real-world
// "install onto a different machine" scenario.
//
// Reliability notes (from the spike; see README.md):
//   - amd64 (x86_64) system emulation via TCG is the most reliable cross-arch
//     target from an arm64 host: mature emulation, stock Debian cloud image,
//     cloud-init SSH setup.
//   - arm64 guests can use KVM on an arm64 host (fast) but are same-arch.
//   - Docker + binfmt was NOT usable in the reference environment, so this
//     harness does not depend on Docker.
//
// The SSH client is the project's existing Go SSH stack (teleport.SSHTransport,
// which drives the system `ssh` client). Host keys are handled with
// StrictHostKeyChecking=no and a per-guest UserKnownHostsFile — never
// ssh-keyscan -H or hashed known_hosts.
//
// This replaces emulation/system/qemu_system.py.
package qemusystem

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mithro/go-multi-binary/archdetect"
	"github.com/mithro/go-multi-binary/teleport"
)

// ArchConfig captures the per-arch QEMU invocation details.
type ArchConfig struct {
	QEMU    string   // qemu-system-* binary
	Image   string   // base cloud image filename under Images
	Machine []string // -machine/-cpu flags
	Net     string   // -device network model
	DriveIf string   // drive if=
	UEFI    bool     // needs aarch64 UEFI firmware via -bios
	BIOS    string   // "default" leaves QEMU's built-in BIOS (riscv64)
}

// ArchConfigs is the per-arch configuration table.
var ArchConfigs = map[string]ArchConfig{
	"amd64": {
		QEMU:    "qemu-system-x86_64",
		Image:   "debian-12-amd64.qcow2",
		Machine: []string{"-machine", "q35", "-cpu", "max"},
		Net:     "virtio-net-pci",
		DriveIf: "virtio",
	},
	"arm64": {
		QEMU:    "qemu-system-aarch64",
		Image:   "debian-12-arm64.qcow2",
		Machine: []string{"-machine", "virt", "-cpu", "max"},
		Net:     "virtio-net-pci",
		DriveIf: "virtio",
		UEFI:    true,
	},
	"riscv64": {
		QEMU:    "qemu-system-riscv64",
		Image:   "debian-13-riscv64.qcow2",
		Machine: []string{"-machine", "virt", "-cpu", "max"},
		Net:     "virtio-net-device",
		DriveIf: "virtio",
		BIOS:    "default",
	},
}

// Images is the directory holding base cloud images (emulation/system/images by
// default). Overridable for tests.
var Images = defaultImagesDir()

func defaultImagesDir() string {
	// This file lives in emulation/system; images sit alongside it. Resolve at
	// runtime relative to the working directory's module layout is unreliable,
	// so callers typically set Images explicitly. Default to ./emulation/system/images.
	return filepath.Join("emulation", "system", "images")
}

// aarch64FirmwareCandidates are UEFI firmware paths across distros; the exact
// path varies by distro/package, so we search rather than hardcode one.
var aarch64FirmwareCandidates = []string{
	"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
	"/usr/share/AAVMF/AAVMF_CODE.fd",
	"/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd",
	"/usr/share/edk2/aarch64/QEMU_EFI.fd",
	"/usr/share/edk2/aarch64/QEMU_EFI-silent.fd",
	"/usr/share/qemu/edk2-aarch64-code.fd",
}

// FindUEFIFirmware returns the first present aarch64 UEFI firmware path.
func FindUEFIFirmware() (string, bool) {
	for _, p := range aarch64FirmwareCandidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// HostArch reports the host's canonical arch id.
func HostArch() string { return archdetect.Current() }

// SelectAccel picks KVM only when the guest arch matches the host AND /dev/kvm
// is usable; otherwise TCG. KVM cannot virtualize a foreign architecture.
func SelectAccel(arch string) string {
	if arch == HostArch() && kvmUsable("/dev/kvm") {
		return "kvm"
	}
	return "tcg"
}

// kvmUsable reports whether /dev/kvm is readable and writable.
func kvmUsable(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// Available reports whether system emulation for arch can run here, with a
// human-readable reason when it cannot.
func Available(arch string) (bool, string) {
	cfg, ok := ArchConfigs[arch]
	if !ok {
		return false, fmt.Sprintf("no config for arch %s", arch)
	}
	if _, err := exec.LookPath(cfg.QEMU); err != nil {
		return false, fmt.Sprintf("%s not installed", cfg.QEMU)
	}
	if _, err := exec.LookPath("cloud-localds"); err != nil {
		return false, "cloud-localds (cloud-image-utils) not installed"
	}
	if _, err := os.Stat(filepath.Join(Images, cfg.Image)); err != nil {
		return false, fmt.Sprintf("base image %s not present in %s", cfg.Image, Images)
	}
	if cfg.UEFI {
		if _, ok := FindUEFIFirmware(); !ok {
			return false, "aarch64 UEFI firmware not found (install qemu-efi-aarch64)"
		}
	}
	return true, ""
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Guest is a running QEMU-system guest exposing SSH on a forwarded local port.
type Guest struct {
	Arch    string
	Host    string   // 127.0.0.1
	Port    int      // forwarded SSH port
	User    string   // tester
	KeyPath string   // private key path
	SSHArgs []string // extra ssh args for go-teleport-self / SSHTransport

	workdir string
	cmd     *exec.Cmd
	qemuLog string
	console string
	qemuOut *os.File
}

// Target returns the user@host string for SSH.
func (g *Guest) Target() string { return g.User + "@" + g.Host }

// Transport returns a teleport.SSHTransport wired to this guest.
func (g *Guest) Transport() teleport.SSHTransport {
	return teleport.SSHTransport{Target: g.Target(), SSHArgs: g.SSHArgs}
}

// Start boots a guest of arch using workdir for scratch state and returns it
// once SSH is stably ready. The caller MUST call Stop to shut QEMU down.
func Start(ctx context.Context, arch, workdir string, bootTimeout time.Duration) (*Guest, error) {
	if ok, why := Available(arch); !ok {
		return nil, fmt.Errorf("%s", why)
	}
	cfg := ArchConfigs[arch]
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return nil, err
	}

	// SSH keypair for the guest.
	key := filepath.Join(workdir, "id_ed25519")
	if err := run(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", key, "-q"); err != nil {
		return nil, fmt.Errorf("ssh-keygen: %w", err)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return nil, err
	}

	// cloud-init seed: create a passwordless 'tester' with our key.
	userData := "#cloud-config\n" +
		"users:\n" +
		"  - name: tester\n" +
		"    sudo: ALL=(ALL) NOPASSWD:ALL\n" +
		"    shell: /bin/bash\n" +
		"    ssh_authorized_keys:\n" +
		"      - " + string(bytes.TrimSpace(pub)) + "\n" +
		"ssh_pwauth: false\n"
	if err := os.WriteFile(filepath.Join(workdir, "user-data"), []byte(userData), 0o644); err != nil {
		return nil, err
	}
	metaData := "instance-id: teleport-e2e\nlocal-hostname: teleport-e2e\n"
	if err := os.WriteFile(filepath.Join(workdir, "meta-data"), []byte(metaData), 0o644); err != nil {
		return nil, err
	}
	seed := filepath.Join(workdir, "seed.img")
	if err := run(ctx, "cloud-localds", seed, filepath.Join(workdir, "user-data"), filepath.Join(workdir, "meta-data")); err != nil {
		return nil, fmt.Errorf("cloud-localds: %w", err)
	}

	// Copy-on-write overlay so the base image stays pristine.
	overlay := filepath.Join(workdir, "overlay.qcow2")
	base := filepath.Join(Images, cfg.Image)
	if err := run(ctx, "qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", overlay); err != nil {
		return nil, fmt.Errorf("qemu-img create overlay: %w", err)
	}

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	console := filepath.Join(workdir, "console.log")

	argv := []string{
		"-m", "1024", "-smp", "2",
	}
	argv = append(argv, cfg.Machine...)
	argv = append(argv,
		"-accel", SelectAccel(arch),
		"-drive", fmt.Sprintf("file=%s,if=%s,format=qcow2", overlay, cfg.DriveIf),
		"-drive", fmt.Sprintf("file=%s,if=%s,format=raw", seed, cfg.DriveIf),
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-:22", port),
		"-device", cfg.Net+",netdev=n0",
		"-nographic",
		"-serial", "file:"+console,
		"-monitor", "none",
	)
	if cfg.UEFI {
		fw, ok := FindUEFIFirmware()
		if !ok {
			return nil, fmt.Errorf("aarch64 UEFI firmware not found (install qemu-efi-aarch64)")
		}
		argv = append(argv, "-bios", fw)
	}

	// Capture QEMU's own stdout+stderr so startup failures are visible (never
	// discard — that hid the CI failure and wasted the full timeout).
	qemuLog := filepath.Join(workdir, "qemu.log")
	qemuOut, err := os.Create(qemuLog)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.QEMU, argv...)
	cmd.Stdout = qemuOut
	cmd.Stderr = qemuOut
	if err := cmd.Start(); err != nil {
		qemuOut.Close()
		return nil, fmt.Errorf("start %s: %w", cfg.QEMU, err)
	}

	g := &Guest{
		Arch:    arch,
		Host:    "127.0.0.1",
		Port:    port,
		User:    "tester",
		KeyPath: key,
		SSHArgs: []string{
			"-p", strconv.Itoa(port),
			"-i", key,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=" + filepath.Join(workdir, "known_hosts"),
		},
		workdir: workdir,
		cmd:     cmd,
		qemuLog: qemuLog,
		console: console,
		qemuOut: qemuOut,
	}

	if err := g.waitSSH(ctx, bootTimeout); err != nil {
		g.Stop()
		return nil, err
	}
	return g, nil
}

// sshExec runs a remote command over ssh with the guest's args and a connect
// timeout, returning the exit success and captured output.
func (g *Guest) sshExec(ctx context.Context, remoteCmd string, connectTimeout int) error {
	args := []string{
		"-p", strconv.Itoa(g.Port),
		"-i", g.KeyPath,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + filepath.Join(g.workdir, "known_hosts"),
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
		g.Target(), remoteCmd,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	return cmd.Run()
}

// waitSSH returns nil only when the guest is stably ready. Two phases: first
// wait for sshd to answer, then block on `cloud-init status --wait` so we don't
// yield during the window where cloud-init regenerates host keys and restarts
// sshd (which resets connections). Transient resets are retried until cloud-init
// is done or the deadline passes.
func (g *Guest) waitSSH(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Phase 1: basic reachability.
	reachable := false
	for time.Now().Before(deadline) {
		if g.exited() {
			return g.startupFailure()
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", g.Port), 3*time.Second)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		conn.Close()
		if g.sshExec(ctx, "true", 10) == nil {
			reachable = true
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !reachable {
		return g.timeoutFailure(timeout)
	}

	// Phase 2: wait for cloud-init so sshd stops being restarted.
	for time.Now().Before(deadline) {
		if g.exited() {
			return g.startupFailure()
		}
		if g.sshExec(ctx, "cloud-init status --wait", 15) == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return g.timeoutFailure(timeout)
}

func (g *Guest) exited() bool {
	return g.cmd.ProcessState != nil && g.cmd.ProcessState.Exited()
}

func (g *Guest) startupFailure() error {
	log := tail(g.qemuLog, 2000)
	return fmt.Errorf("guest %s: qemu exited early\nqemu output:\n%s", g.Arch, log)
}

func (g *Guest) timeoutFailure(timeout time.Duration) error {
	tailc := tail(g.console, 2000)
	if tailc == "" {
		tailc = "(no console output)"
	}
	return fmt.Errorf("guest %s did not become SSH-ready in %s\n%s", g.Arch, timeout, tailc)
}

// Stop terminates the QEMU process and releases resources.
func (g *Guest) Stop() error {
	if g.cmd == nil || g.cmd.Process == nil {
		return nil
	}
	_ = g.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- g.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = g.cmd.Process.Kill()
		<-done
	}
	if g.qemuOut != nil {
		g.qemuOut.Close()
	}
	return nil
}

// run executes a command, attaching its combined output to any error.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}
	return nil
}

// tail returns up to the last n bytes of a file (empty string if unreadable).
func tail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > n {
		data = data[len(data)-n:]
	}
	return string(data)
}
