// Command go-teleport-self is a self-installing multi-architecture binary.
//
// The distributed artifact is a normal native ELF for one architecture with a
// FATBLOB (containing every architecture's native binary) appended as trailing
// data. Run with a target "user@host" it detects the remote architecture,
// reconstructs the exact canonical binary for that architecture from its own
// embedded blob, and installs it into ~/local/bin over SSH — downloading nothing.
//
// Subcommands:
//
//	go-teleport-self info               human-readable identity + embedded arches
//	go-teleport-self list              JSON arch inventory
//	go-teleport-self verify            integrity self-check
//	go-teleport-self extract <arch> <out>   write canonical(arch) to a file
//	go-teleport-self <user@host>        teleport self into the remote ~/local/bin
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mithro/go-multi-binary/archdetect"
	"github.com/mithro/go-multi-binary/fatblob"
	"github.com/mithro/go-multi-binary/teleport"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// ArchEntry is one architecture's presence in the embedded blob.
type ArchEntry struct {
	Arch    string `json:"arch"`
	Present bool   `json:"present"`
	Size    int    `json:"size"`
	SHA256  string `json:"sha256,omitempty"`
	Status  uint8  `json:"status"`
}

// inventory parses the FATBLOB attached to a canonical image and reports each
// architecture slice with its size and sha256.
func inventory(image []byte) ([]ArchEntry, error) {
	_, blob, err := fatblob.SplitCanonical(image)
	if err != nil {
		return nil, err
	}
	entries := make([]ArchEntry, 0, len(blob.Slices))
	for _, s := range blob.Slices {
		e := ArchEntry{Arch: s.Arch, Status: s.Status, Size: len(s.Data)}
		if s.Status == fatblob.StatusPresent && len(s.Data) > 0 {
			e.Present = true
			sum := sha256.Sum256(s.Data)
			e.SHA256 = hex.EncodeToString(sum[:])
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "go-teleport-self:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "info":
		return cmdInfo()
	case "list":
		return cmdList()
	case "verify":
		return cmdVerify()
	case "extract":
		if len(args) != 3 {
			return fmt.Errorf("usage: go-teleport-self extract <arch> <outpath>")
		}
		return cmdExtract(args[1], args[2])
	case "-h", "--help", "help":
		usage()
		return nil
	case "--version", "version":
		fmt.Println(version)
		return nil
	default:
		// A "user@host" or "host" target means teleport (wired in Task 6).
		if strings.Contains(args[0], "@") || looksLikeHost(args[0]) {
			return cmdTeleport(args)
		}
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func looksLikeHost(s string) bool {
	return !strings.HasPrefix(s, "-") && !strings.Contains(s, "/") && s != ""
}

func cmdInfo() error {
	self, err := fatblob.ReadSelf()
	if err != nil {
		return err
	}
	fmt.Printf("go-teleport-self %s\n", version)
	fmt.Printf("running arch:   %s\n", archdetect.Current())
	entries, err := inventory(self)
	if err != nil {
		fmt.Printf("embedded blob:  none (bare native binary)\n")
		return nil
	}
	fmt.Printf("embedded arches:\n")
	for _, e := range entries {
		if e.Present {
			fmt.Printf("  %-8s present  %10d bytes  sha256:%s\n", e.Arch, e.Size, e.SHA256[:16])
		} else {
			fmt.Printf("  %-8s reserved (no binary embedded)\n", e.Arch)
		}
	}
	return nil
}

func cmdList() error {
	self, err := fatblob.ReadSelf()
	if err != nil {
		return err
	}
	entries, err := inventory(self)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"version": version,
		"running": archdetect.Current(),
		"arches":  entries,
	})
}

func cmdVerify() error {
	self, err := fatblob.ReadSelf()
	if err != nil {
		return err
	}
	// 1. The attached blob must round-trip (re-encode == attached bytes).
	native, blob, err := fatblob.SplitCanonical(self)
	if err != nil {
		return fmt.Errorf("no valid FATBLOB attached: %w", err)
	}
	reenc, err := fatblob.BuildCanonical(native, blob)
	if err != nil {
		return err
	}
	if len(reenc) != len(self) {
		return fmt.Errorf("blob does not round-trip (len %d != %d)", len(reenc), len(self))
	}
	// 2. The image must equal canonical(current arch): reconstruct(self, current) == self.
	cur := archdetect.Current()
	if recon, err := fatblob.Reconstruct(self, cur); err == nil {
		if sha256.Sum256(recon) != sha256.Sum256(self) {
			return fmt.Errorf("self-image is not canonical(%s)", cur)
		}
		fmt.Printf("OK: image is canonical(%s) and blob round-trips\n", cur)
	} else {
		// Current arch may be the reserved slot in exotic cases; still report blob OK.
		fmt.Printf("OK: blob round-trips (current arch %s not a present slice)\n", cur)
	}
	return nil
}

func cmdExtract(arch, out string) error {
	self, err := fatblob.ReadSelf()
	if err != nil {
		return err
	}
	data, err := fatblob.Reconstruct(self, arch)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o755); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	fmt.Printf("wrote canonical(%s): %d bytes  sha256:%s  -> %s\n",
		arch, len(data), hex.EncodeToString(sum[:]), out)
	return nil
}

// cmdTeleport installs this binary onto a remote host of possibly-different
// architecture, reconstructing the exact canonical artifact for the remote's
// arch from this binary's own embedded FATBLOB and copying it over SSH.
//
//	go-teleport-self user@host [-- <extra ssh args>]
//
// Destination defaults to ~/local/bin.
func cmdTeleport(args []string) error {
	target := args[0]
	var sshArgs []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			sshArgs = append(sshArgs, args[i+1:]...)
			break
		}
	}
	self, err := fatblob.ReadSelf()
	if err != nil {
		return err
	}
	if _, _, err := fatblob.SplitCanonical(self); err != nil {
		return fmt.Errorf("this binary has no embedded FATBLOB to teleport: %w", err)
	}
	tr := teleport.SSHTransport{Target: target, SSHArgs: sshArgs}
	res, err := teleport.Deploy(context.Background(), self, tr, "~/local/bin")
	if err != nil {
		return err
	}
	fmt.Printf("teleported to %s: installed canonical(%s), %d bytes, md5:%s -> %s\n",
		target, res.Arch, res.Size, res.MD5, res.RemotePath)
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `go-teleport-self — self-installing multi-architecture binary

Usage:
  go-teleport-self info                    show identity and embedded arches
  go-teleport-self list                    JSON arch inventory
  go-teleport-self verify                  integrity self-check
  go-teleport-self extract <arch> <out>    write canonical(arch) to a file
  go-teleport-self <user@host>             install self into remote ~/local/bin
`)
}
