// Command fatpack is the build-time tool that assembles the FATBLOB from bare
// per-architecture native binaries and emits each canonical distributable plus
// a manifest of sizes and checksums.
//
// Usage:
//
//	fatpack assemble --in dist/native --out dist --manifest dist/MANIFEST.json
//
// It reads native binaries named `native.<arch>` from --in, builds the shared
// FATBLOB (riscv32 included as a reserved placeholder), writes
// `go-teleport-self.<arch>` = canonical(arch) for every present arch to --out,
// and writes the manifest. Deterministic: identical inputs -> identical outputs.
// The assembly logic lives in internal/fatbuild so the fatbuild command and the
// determinism test can reuse it without shelling out to this tool.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mithro/go-multi-binary/internal/fatbuild"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "assemble" {
		fmt.Fprintln(os.Stderr, "usage: fatpack assemble --in DIR --out DIR --manifest FILE")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("assemble", flag.ExitOnError)
	in := fs.String("in", "dist/native", "directory of native.<arch> binaries")
	out := fs.String("out", "dist", "output directory for canonical artifacts")
	manifest := fs.String("manifest", "dist/MANIFEST.json", "manifest output path")
	fs.Parse(os.Args[2:])

	if _, err := fatbuild.Assemble(*in, *out, *manifest); err != nil {
		fmt.Fprintln(os.Stderr, "fatpack:", err)
		os.Exit(1)
	}
}
