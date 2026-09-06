package fatblob

import (
	"bytes"
	"fmt"
	"io"

	"github.com/ulikunitz/xz"
)

// Decompress recovers a slice's raw native bytes from its stored Data according
// to codec. rawLen is the expected uncompressed length (from the index entry)
// and is verified for compressed codecs. This is the ONLY compression code that
// links into the distributed native binary (via Reconstruct), so it deliberately
// pulls in nothing but the pure-Go xz decoder: it must build under
// CGO_ENABLED=0.
//
// For CodecNone the data is returned as-is (rawLen ignored). For CodecXZ the
// data is an xz (LZMA2) stream, decoded and checked against rawLen.
func Decompress(codec uint8, data []byte, rawLen uint64) ([]byte, error) {
	switch codec {
	case CodecNone:
		return data, nil
	case CodecXZ:
		r, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("fatblob: xz reader: %w", err)
		}
		// Pre-size the buffer to rawLen (+1 so a stream that decodes to more
		// than rawLen still triggers the length check rather than a realloc).
		buf := bytes.NewBuffer(make([]byte, 0, rawLen+1))
		if _, err := io.Copy(buf, r); err != nil {
			return nil, fmt.Errorf("fatblob: xz decode: %w", err)
		}
		out := buf.Bytes()
		if uint64(len(out)) != rawLen {
			return nil, fmt.Errorf("fatblob: decompressed length %d != recorded rawLen %d", len(out), rawLen)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("fatblob: unknown codec %d", codec)
	}
}
