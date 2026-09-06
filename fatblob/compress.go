package fatblob

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"

	"github.com/ulikunitz/xz"
)

// Compress encodes raw for storage in a slice, returning the codec tag and the
// stored bytes. It targets the smallest possible output: it prefers the system
// `xz` binary at preset 9 | extreme (the reference LZMA2 encoder, which the
// pure-Go match-finder does not match on ratio), and only falls back to the
// pure-Go ulikunitz encoder when `xz` is unavailable. Either way the output is a
// standard .xz stream that Decompress reads with the pure-Go decoder.
//
// Compression runs once at build time, so its cost is irrelevant. If the result
// is not smaller than raw (e.g. tiny or incompressible inputs) the raw bytes are
// stored verbatim with CodecNone.
//
// This function is only reachable from the build-time packer; it is dead code in
// the distributed native binary and is dropped by the linker, so os/exec and the
// xz encoder never bloat the shipped artifact.
func Compress(raw []byte) (codec uint8, data []byte, err error) {
	comp, err := xzCompress(raw)
	if err != nil {
		return 0, nil, err
	}
	if len(comp) >= len(raw) {
		// Compression did not pay off; store verbatim.
		return CodecNone, raw, nil
	}
	return CodecXZ, comp, nil
}

// xzCompress produces a .xz (LZMA2) stream from raw, preferring the reference
// system encoder for maximum ratio and falling back to pure Go.
func xzCompress(raw []byte) ([]byte, error) {
	if out, err := xzSystem(raw); err == nil {
		return out, nil
	} else if !errIsNotFound(err) {
		// xz exists but failed for another reason (bad options, I/O): surface it
		// rather than silently degrading to the weaker encoder.
		return nil, fmt.Errorf("fatblob: system xz failed: %w", err)
	}
	return xzPureGo(raw)
}

// xzSystem runs `xz -9 -e -T1` over raw. -T1 forces single-threaded encoding:
// multi-threaded xz splits the input into independently-compressed blocks, which
// both hurts the ratio and makes the byte output depend on the thread count.
func xzSystem(raw []byte) ([]byte, error) {
	cmd := exec.Command("xz", "-9", "-e", "-T1", "-c")
	cmd.Stdin = bytes.NewReader(raw)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func errIsNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}

// xzPureGo is the fallback encoder: a valid .xz stream from the pure-Go
// ulikunitz encoder. Its ratio is weaker than the reference encoder, but the
// stream still decodes with the same in-binary decoder.
func xzPureGo(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CompressBlob returns a copy of raw in which every present slice's Data is
// compressed (Codec/RawLen set accordingly). Reserved/empty slices pass through
// unchanged with CodecNone. The input slices are expected to hold raw native
// bytes (CodecNone); already-compressed input is an error to avoid double work.
func CompressBlob(raw Blob) (Blob, error) {
	out := Blob{Slices: make([]Slice, len(raw.Slices))}
	for i, s := range raw.Slices {
		if s.Status != StatusPresent || len(s.Data) == 0 {
			// Reserved / empty: carry through, forced to the stored codec.
			out.Slices[i] = Slice{Arch: s.Arch, Status: s.Status, Codec: CodecNone, RawLen: 0, Data: s.Data}
			continue
		}
		if s.Codec != CodecNone {
			return Blob{}, fmt.Errorf("fatblob: CompressBlob: slice %q already has codec %d", s.Arch, s.Codec)
		}
		codec, data, err := Compress(s.Data)
		if err != nil {
			return Blob{}, fmt.Errorf("fatblob: compress %q: %w", s.Arch, err)
		}
		out.Slices[i] = Slice{
			Arch:   s.Arch,
			Status: s.Status,
			Codec:  codec,
			RawLen: uint64(len(s.Data)),
			Data:   data,
		}
	}
	return out, nil
}
