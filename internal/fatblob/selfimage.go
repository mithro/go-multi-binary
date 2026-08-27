package fatblob

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// A "canonical image" is the distributed artifact for one architecture:
//
//	canonical(arch) = native(arch) ++ Encode(blob)
//
// where native(arch) is a normal, kernel-loadable ELF for arch, and the encoded
// blob is appended as trailing data (which the kernel ELF loader ignores). Every
// canonical(*) shares the identical trailing blob; they differ only in the
// leading native bytes. This is what makes cross-arch reconstruction byte-exact.

// BuildCanonical returns nativeForTarget followed by the encoded blob.
func BuildCanonical(nativeForTarget []byte, blob Blob) ([]byte, error) {
	enc, err := Encode(blob)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nativeForTarget)+len(enc))
	out = append(out, nativeForTarget...)
	out = append(out, enc...)
	return out, nil
}

// SplitCanonical locates the appended blob in a canonical image using the fixed
// EOF trailer, and returns the leading native bytes and the parsed blob.
func SplitCanonical(image []byte) (native []byte, blob Blob, err error) {
	if len(image) < TrailerLen {
		return nil, Blob{}, errors.New("fatblob: image shorter than trailer")
	}
	if string(image[len(image)-magicLen:]) != TrailerMagic {
		return nil, Blob{}, errors.New("fatblob: no FATBLOB trailer at EOF")
	}
	blobLen := binary.LittleEndian.Uint64(image[len(image)-TrailerLen : len(image)-magicLen])
	if blobLen == 0 || blobLen > uint64(len(image)) {
		return nil, Blob{}, fmt.Errorf("fatblob: invalid trailer length %d (image %d bytes)", blobLen, len(image))
	}
	blobStart := len(image) - int(blobLen)
	blob, err = Decode(image[blobStart:])
	if err != nil {
		return nil, Blob{}, err
	}
	native = append([]byte(nil), image[:blobStart]...)
	return native, blob, nil
}

// Reconstruct produces canonical(target) from any canonical image. Because the
// appended blob is identical across every canonical(*) and the target's native
// bytes are pulled verbatim from that blob, the result is byte-identical to the
// canonical image originally built for target.
func Reconstruct(image []byte, target string) ([]byte, error) {
	_, blob, err := SplitCanonical(image)
	if err != nil {
		return nil, err
	}
	for _, s := range blob.Slices {
		if s.Arch != target {
			continue
		}
		if s.Status != StatusPresent || len(s.Data) == 0 {
			return nil, fmt.Errorf("fatblob: target arch %q is reserved/empty in this image", target)
		}
		return BuildCanonical(s.Data, blob)
	}
	return nil, fmt.Errorf("fatblob: target arch %q not present in image", target)
}

// ReadSelf reads the current executable's own file bytes (resolving symlinks).
func ReadSelf() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}
