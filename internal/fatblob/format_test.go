package fatblob

import (
	"bytes"
	"testing"
)

func testBlob() Blob {
	return Blob{Slices: []Slice{
		{Arch: "386", Status: 0, Data: []byte("iii")},
		{Arch: "amd64", Status: 0, Data: []byte("AAAA")},
		{Arch: "arm", Status: 0, Data: []byte("rr")},
		{Arch: "arm64", Status: 0, Data: []byte("bbbbb")},
		{Arch: "riscv64", Status: 0, Data: []byte("V")},
		{Arch: "riscv32", Status: 1, Data: nil}, // reserved placeholder
	}}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	b := testBlob()
	enc, err := Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.HasPrefix(enc, []byte(Magic)) {
		t.Fatalf("missing magic prefix")
	}
	if !bytes.HasSuffix(enc, []byte(TrailerMagic)) {
		t.Fatalf("missing trailer magic")
	}
	got, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.Slices) != len(b.Slices) {
		t.Fatalf("slice count = %d, want %d", len(got.Slices), len(b.Slices))
	}
	for i := range b.Slices {
		if got.Slices[i].Arch != b.Slices[i].Arch ||
			got.Slices[i].Status != b.Slices[i].Status ||
			!bytes.Equal(got.Slices[i].Data, b.Slices[i].Data) {
			t.Fatalf("slice %d mismatch: got %+v want %+v", i, got.Slices[i], b.Slices[i])
		}
	}
}

func TestEncodeDeterministic(t *testing.T) {
	a, _ := Encode(testBlob())
	c, _ := Encode(testBlob())
	if !bytes.Equal(a, c) {
		t.Fatalf("Encode not deterministic")
	}
}

func TestTrailerRecordsTotalLength(t *testing.T) {
	enc, err := Encode(testBlob())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// The trailer's recorded length must equal the whole encoded blob length,
	// so a self-image reader can locate the blob from EOF.
	if got := decodeTrailerLen(enc); got != uint64(len(enc)) {
		t.Fatalf("trailer length = %d, want %d", got, len(enc))
	}
}

func TestDecodeRejectsBadMagic(t *testing.T) {
	enc, _ := Encode(testBlob())
	bad := append([]byte(nil), enc...)
	bad[0] = 'X'
	if _, err := Decode(bad); err == nil {
		t.Fatalf("expected error for corrupt magic")
	}
}
