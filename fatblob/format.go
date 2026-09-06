// Package fatblob defines the on-disk container ("FATBLOB") that carries every
// architecture's native binary as trailing data appended after a normal ELF.
//
// Layout of an encoded blob (all integers little-endian):
//
//	magic        8 bytes   "FATBLOB\x02"
//	count        u16       number of index entries
//	index        count * 40-byte entries, each:
//	                 name    8 bytes  arch id, NUL-padded (error if > 8 bytes)
//	                 status  u8       0 = present, 1 = reserved/empty
//	                 codec   u8       0 = stored/none, 1 = xz (LZMA2)
//	                 _rsvd   6 bytes  zero (alignment / future use)
//	                 offset  u64      byte offset of payload within the payload region
//	                 length  u64      stored (possibly compressed) payload length
//	                 rawLen  u64      uncompressed payload length (0 when codec=none)
//	payload      concatenation of every slice's Data, in index order
//	trailer      u64 totalLen (whole blob length) + "FATBLOBZ" (8 bytes)
//
// Slice.Data is treated as OPAQUE bytes by Encode/Decode: when a slice is
// compressed, Data holds the compressed stream and Codec/RawLen describe how to
// recover it (see codec.go). The fixed-size trailer lets a running program
// locate the appended blob from EOF without knowing the size of the ELF it is
// glued behind. Encoding is fully deterministic: identical Blob input always
// yields identical bytes.
//
// Format history:
//
//	\x01  original, uncompressed only, 32-byte index entries (no codec/rawLen).
//	\x02  adds per-slice Codec + RawLen in a 40-byte index entry. There are no
//	      \x01 releases carrying compressed data in the wild; \x02 code refuses
//	      to parse a \x01 blob (and vice-versa) rather than silently mis-reading
//	      the wider index entries.
package fatblob

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Magic is the 8-byte prefix of every encoded blob (current format version).
const Magic = "FATBLOB\x02"

// MagicV1 is the prefix of the retired, uncompressed-only format. It is
// recognised only so Decode can report a clear version mismatch.
const MagicV1 = "FATBLOB\x01"

// TrailerMagic is the 8-byte suffix of every encoded blob.
const TrailerMagic = "FATBLOBZ"

// TrailerLen is the fixed size of the EOF trailer: u64 length + TrailerMagic.
const TrailerLen = 16

const (
	magicLen      = 8
	indexEntryLen = 40 // name[8]+status[1]+codec[1]+rsvd[6]+offset[8]+length[8]+rawLen[8]
)

// Slice status values.
const (
	StatusPresent  uint8 = 0 // a real native binary is embedded
	StatusReserved uint8 = 1 // reserved placeholder (e.g. riscv32); Data is empty
)

// Codec identifies how a slice's stored Data must be decoded to recover the raw
// native ELF. See codec.go for compression/decompression.
const (
	CodecNone uint8 = 0 // Data is the raw native, stored verbatim
	CodecXZ   uint8 = 1 // Data is an xz (LZMA2) stream; RawLen is the native size
)

// Slice is one architecture's entry in the container. For a present slice, Data
// holds the stored (possibly compressed) bytes, Codec says how they were
// encoded, and RawLen is the uncompressed native length. For CodecNone, Data is
// the native itself and RawLen is ignored (it may be 0).
type Slice struct {
	Arch   string
	Status uint8
	Codec  uint8
	RawLen uint64
	Data   []byte
}

// Blob is the full set of architecture slices, in canonical (index) order.
type Blob struct {
	Slices []Slice
}

// Encode serializes a Blob into its deterministic byte representation.
func Encode(b Blob) ([]byte, error) {
	count := len(b.Slices)
	if count > 0xFFFF {
		return nil, fmt.Errorf("fatblob: too many slices: %d", count)
	}

	// Compute total size up front so we can write the trailer length.
	payloadLen := 0
	for _, s := range b.Slices {
		if len(s.Arch) > 8 {
			return nil, fmt.Errorf("fatblob: arch id %q exceeds 8 bytes", s.Arch)
		}
		if s.Codec != CodecNone && s.Codec != CodecXZ {
			return nil, fmt.Errorf("fatblob: slice %q unknown codec %d", s.Arch, s.Codec)
		}
		payloadLen += len(s.Data)
	}
	total := magicLen + 2 + count*indexEntryLen + payloadLen + TrailerLen

	out := make([]byte, 0, total)
	out = append(out, Magic...)
	out = binary.LittleEndian.AppendUint16(out, uint16(count))

	// Index. Offsets are relative to the start of the payload region.
	var offset uint64
	for _, s := range b.Slices {
		var name [8]byte
		copy(name[:], s.Arch)
		out = append(out, name[:]...)
		out = append(out, s.Status)
		out = append(out, s.Codec)
		out = append(out, make([]byte, 6)...) // reserved
		out = binary.LittleEndian.AppendUint64(out, offset)
		out = binary.LittleEndian.AppendUint64(out, uint64(len(s.Data)))
		out = binary.LittleEndian.AppendUint64(out, s.RawLen)
		offset += uint64(len(s.Data))
	}

	// Payload region.
	for _, s := range b.Slices {
		out = append(out, s.Data...)
	}

	// Trailer: total length of the whole blob, then trailer magic.
	out = binary.LittleEndian.AppendUint64(out, uint64(total))
	out = append(out, TrailerMagic...)

	if len(out) != total {
		// Internal invariant; guards against future layout mistakes.
		return nil, fmt.Errorf("fatblob: encoded length %d != computed %d", len(out), total)
	}
	return out, nil
}

// Decode parses an encoded blob whose first byte is the start of Magic.
func Decode(data []byte) (Blob, error) {
	if len(data) < magicLen+2+TrailerLen {
		return Blob{}, errors.New("fatblob: data too short")
	}
	if string(data[:magicLen]) != Magic {
		if string(data[:magicLen]) == MagicV1 {
			return Blob{}, errors.New("fatblob: unsupported format version \\x01 (this build reads \\x02); rebuild the blob")
		}
		return Blob{}, errors.New("fatblob: bad magic")
	}
	if string(data[len(data)-magicLen:]) != TrailerMagic {
		return Blob{}, errors.New("fatblob: bad trailer magic")
	}
	if decodeTrailerLen(data) != uint64(len(data)) {
		return Blob{}, fmt.Errorf("fatblob: trailer length %d != data length %d",
			decodeTrailerLen(data), len(data))
	}

	count := int(binary.LittleEndian.Uint16(data[magicLen : magicLen+2]))
	indexStart := magicLen + 2
	payloadStart := indexStart + count*indexEntryLen
	if payloadStart+TrailerLen > len(data) {
		return Blob{}, errors.New("fatblob: index overruns data")
	}
	payloadEnd := len(data) - TrailerLen

	slices := make([]Slice, 0, count)
	for i := 0; i < count; i++ {
		e := data[indexStart+i*indexEntryLen : indexStart+(i+1)*indexEntryLen]
		arch := trimNul(e[0:8])
		status := e[8]
		codec := e[9]
		offset := binary.LittleEndian.Uint64(e[16:24])
		length := binary.LittleEndian.Uint64(e[24:32])
		rawLen := binary.LittleEndian.Uint64(e[32:40])

		start := payloadStart + int(offset)
		end := start + int(length)
		if start < payloadStart || end > payloadEnd || end < start {
			return Blob{}, fmt.Errorf("fatblob: slice %d (%s) payload out of range", i, arch)
		}
		var payload []byte
		if length > 0 {
			payload = append([]byte(nil), data[start:end]...)
		}
		slices = append(slices, Slice{Arch: arch, Status: status, Codec: codec, RawLen: rawLen, Data: payload})
	}
	return Blob{Slices: slices}, nil
}

// decodeTrailerLen reads the total-length field from the fixed EOF trailer.
func decodeTrailerLen(data []byte) uint64 {
	if len(data) < TrailerLen {
		return 0
	}
	return binary.LittleEndian.Uint64(data[len(data)-TrailerLen : len(data)-magicLen])
}

func trimNul(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// FixedArchOrder is the canonical order of architecture slices in the blob.
// riscv32 is last: it is the reserved placeholder slot (no Go toolchain can
// build it today; see docs/research/multi-arch-binary-approaches.md §3/§7).
func FixedArchOrder() []string {
	return []string{"386", "amd64", "arm", "arm64", "riscv64", "riscv32"}
}
