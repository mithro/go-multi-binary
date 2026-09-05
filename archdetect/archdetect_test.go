package archdetect

import "testing"

func TestFromUname(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "amd64",
		"amd64":   "amd64",
		"i686":    "386",
		"i386":    "386",
		"i586":    "386",
		"armv7l":  "arm",
		"armv6l":  "arm",
		"armhf":   "arm",
		"arm":     "arm",
		"aarch64": "arm64",
		"arm64":   "arm64",
		"riscv64": "riscv64",
		"riscv32": "riscv32",
		"riscv":   "riscv32",
	}
	for in, want := range cases {
		got, err := FromUname(in)
		if err != nil || got != want {
			t.Fatalf("FromUname(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	// uname output often has trailing whitespace/newline.
	if got, err := FromUname("  x86_64\n"); err != nil || got != "amd64" {
		t.Fatalf("FromUname with whitespace = %q,%v want amd64", got, err)
	}
	if _, err := FromUname("sparc64"); err == nil {
		t.Fatalf("expected error for unknown arch")
	}
}

func TestTableIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Table() {
		if seen[a.ID] {
			t.Fatalf("duplicate arch id %q", a.ID)
		}
		seen[a.ID] = true
		if a.EMachine == 0 {
			t.Fatalf("arch %q has zero EMachine", a.ID)
		}
	}
	for _, id := range []string{"386", "amd64", "arm", "arm64", "riscv64", "riscv32"} {
		if !seen[id] {
			t.Fatalf("missing arch %q in table", id)
		}
	}
}

func TestSupportedFlag(t *testing.T) {
	got := map[string]bool{}
	for _, a := range Table() {
		got[a.ID] = a.Supported
	}
	if !got["amd64"] || !got["riscv64"] {
		t.Fatalf("expected amd64/riscv64 supported")
	}
	if got["riscv32"] {
		t.Fatalf("riscv32 must be marked unsupported (reserved slot)")
	}
}

func TestCurrentIsKnown(t *testing.T) {
	cur := Current()
	if _, err := FromUname(cur); err != nil {
		// Current() returns one of our ids, which FromUname must also accept.
		found := false
		for _, a := range Table() {
			if a.ID == cur {
				found = true
			}
		}
		if !found {
			t.Fatalf("Current() = %q not in table", cur)
		}
	}
}
