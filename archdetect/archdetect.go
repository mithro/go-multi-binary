// Package archdetect is the single source of truth for the architectures this
// project targets: their canonical ids, Go build settings, ELF e_machine values,
// and the mapping from `uname -m` strings to our ids.
package archdetect

import (
	"fmt"
	"runtime"
	"strings"
)

// ELF e_machine values (see include/uapi/linux/elf-em.h and the ELF gABI).
const (
	EM_386     uint16 = 3
	EM_ARM     uint16 = 40
	EM_X86_64  uint16 = 62
	EM_AARCH64 uint16 = 183
	EM_RISCV   uint16 = 243
)

// ArchInfo describes one target architecture.
type ArchInfo struct {
	ID           string   // our canonical id, e.g. "amd64"
	GOARCH       string   // Go GOARCH (empty for unsupported)
	GOARM        string   // Go GOARM (only for "arm")
	EMachine     uint16   // ELF e_machine
	UnameAliases []string // `uname -m` strings that map to this arch
	Supported    bool     // can any Go toolchain build a Linux userspace binary?
}

// Table returns the canonical architecture table. Order matches
// fatblob.FixedArchOrder (riscv32 last as the reserved slot).
func Table() []ArchInfo {
	return []ArchInfo{
		{ID: "386", GOARCH: "386", EMachine: EM_386,
			UnameAliases: []string{"i386", "i486", "i586", "i686", "x86", "ia32"}, Supported: true},
		{ID: "amd64", GOARCH: "amd64", EMachine: EM_X86_64,
			UnameAliases: []string{"x86_64", "amd64", "x64"}, Supported: true},
		{ID: "arm", GOARCH: "arm", GOARM: "6", EMachine: EM_ARM,
			UnameAliases: []string{"arm", "armv6l", "armv7l", "armhf", "armel", "armv5l"}, Supported: true},
		{ID: "arm64", GOARCH: "arm64", EMachine: EM_AARCH64,
			UnameAliases: []string{"aarch64", "arm64", "armv8l"}, Supported: true},
		{ID: "riscv64", GOARCH: "riscv64", EMachine: EM_RISCV,
			UnameAliases: []string{"riscv64"}, Supported: true},
		// Reserved: no Go toolchain can build riscv32 Linux userspace today
		// (docs/research/multi-arch-binary-approaches.md §3/§7).
		{ID: "riscv32", GOARCH: "", EMachine: EM_RISCV,
			UnameAliases: []string{"riscv32", "riscv", "riscv32gc"}, Supported: false},
	}
}

// FromUname maps a `uname -m` string (leading/trailing space tolerated) to our
// canonical arch id, or returns an error for an unrecognized machine.
func FromUname(m string) (string, error) {
	m = strings.ToLower(strings.TrimSpace(m))
	for _, a := range Table() {
		if a.ID == m {
			return a.ID, nil
		}
		for _, alias := range a.UnameAliases {
			if alias == m {
				return a.ID, nil
			}
		}
	}
	return "", fmt.Errorf("archdetect: unrecognized machine %q", m)
}

// Current maps the running binary's runtime.GOARCH to our canonical id.
func Current() string {
	for _, a := range Table() {
		if a.GOARCH != "" && a.GOARCH == runtime.GOARCH {
			return a.ID
		}
	}
	// Fall back to GOARCH verbatim (keeps output honest on unexpected ports).
	return runtime.GOARCH
}

// Lookup returns the ArchInfo for an id, or ok=false.
func Lookup(id string) (ArchInfo, bool) {
	for _, a := range Table() {
		if a.ID == id {
			return a, true
		}
	}
	return ArchInfo{}, false
}
