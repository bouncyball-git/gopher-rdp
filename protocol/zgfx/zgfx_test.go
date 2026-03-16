package zgfx

import (
	"bytes"
	"testing"
)

func TestDecompressUncompressedSingle(t *testing.T) {
	// Single segment, uncompressed: descriptor=0xE0, flags=0x00 (no COMPRESSED bit)
	payload := []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F} // "Hello"
	src := make([]byte, 0, 1+1+len(payload))
	src = append(src, 0xE0) // single segment descriptor
	src = append(src, 0x00) // flags: not compressed
	src = append(src, payload...)

	d := &Decompressor{}
	got, err := d.Decompress(nil, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %x, want %x", got, payload)
	}

	// Verify history contains the data
	for i, b := range payload {
		if d.history[i] != b {
			t.Fatalf("history[%d] = %02x, want %02x", i, d.history[i], b)
		}
	}
}

func TestDecompressMultipartUncompressed(t *testing.T) {
	// Multipart with 2 uncompressed segments
	seg1 := []byte{0x00, 0x41, 0x42} // flags=0x00 uncompressed, "AB"
	seg2 := []byte{0x00, 0x43, 0x44} // flags=0x00 uncompressed, "CD"

	src := make([]byte, 0, 64)
	src = append(src, 0xE1) // multipart descriptor
	// segmentCount = 2 (LE u16)
	src = append(src, 0x02, 0x00)
	// uncompressedSize = 4 (LE u32)
	src = append(src, 0x04, 0x00, 0x00, 0x00)
	// segment 1: size=3 (LE u32) + data
	src = append(src, byte(len(seg1)), 0x00, 0x00, 0x00)
	src = append(src, seg1...)
	// segment 2: size=3 (LE u32) + data
	src = append(src, byte(len(seg2)), 0x00, 0x00, 0x00)
	src = append(src, seg2...)

	d := &Decompressor{}
	got, err := d.Decompress(nil, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	want := []byte{0x41, 0x42, 0x43, 0x44}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestDecompressCompressedLiteral(t *testing.T) {
	// Compressed segment with a single literal byte 'A' (0x41).
	// Token 0: prefix=0 (1 bit), 8 extra bits for byte value
	// Binary: 0_01000001 = 9 bits = 0x41
	// Pad to 2 bytes: 0_0100000 1_0000000 → but we need MSB-first:
	// bits: 0 01000001 → byte1=0b00100000=0x20, byte2=0b10000000=0x80
	// Padding = 7 bits

	seg := []byte{
		0x20,       // flags: COMPRESSED (bit 5 set)
		0x20, 0x80, // compressed data: literal 'A' in 9 bits + 7 padding
		7,          // padding bits
	}

	src := []byte{0xE0} // single segment
	src = append(src, seg...)

	d := &Decompressor{}
	got, err := d.Decompress(nil, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if !bytes.Equal(got, []byte{'A'}) {
		t.Fatalf("got %x, want %x", got, []byte{'A'})
	}
}

func TestDecompressCompressedShortcutLiteral(t *testing.T) {
	// Test a shortcut literal: token 6 (prefix=11000, 5 bits) → literal 0x00
	// Binary: 11000 = 5 bits
	// Pad to 1 byte: 11000_000 → 0xC0
	// Padding = 3 bits

	seg := []byte{
		0x20, // flags: COMPRESSED
		0xC0, // compressed data: 11000 + 3 padding bits
		3,    // padding bits
	}

	src := []byte{0xE0}
	src = append(src, seg...)

	d := &Decompressor{}
	got, err := d.Decompress(nil, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if !bytes.Equal(got, []byte{0x00}) {
		t.Fatalf("got %x, want [00]", got)
	}
}

func TestDecompressBufferReuse(t *testing.T) {
	seg := []byte{0x00, 0x48, 0x49} // uncompressed "HI"
	src := []byte{0xE0}
	src = append(src, seg...)

	d := &Decompressor{}
	dst := make([]byte, 0, 100)
	got, err := d.Decompress(dst, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if !bytes.Equal(got, []byte{0x48, 0x49}) {
		t.Fatalf("got %x, want %x", got, []byte{0x48, 0x49})
	}
	// Verify the returned slice reused the original backing array
	if cap(got) != 100 {
		t.Fatalf("buffer was not reused: cap=%d, want 100", cap(got))
	}
}

func TestDecompressErrors(t *testing.T) {
	d := &Decompressor{}

	tests := []struct {
		name string
		src  []byte
	}{
		{"empty", nil},
		{"bad descriptor", []byte{0xFF}},
		{"short single", []byte{0xE0}},
		{"short multipart", []byte{0xE1, 0x00}},
	}
	for _, tt := range tests {
		_, err := d.Decompress(nil, tt.src)
		if err == nil {
			t.Errorf("%s: expected error, got nil", tt.name)
		}
	}
}

func TestPrefixLookupTable(t *testing.T) {
	// Verify token 0 (literal with 8 extra bits): prefix bit 0 → indices 0..255
	for i := 0; i < 256; i++ {
		e := prefixLookup[i]
		if e.tokenType != 0 || e.prefixBits != 1 || e.valueBits != 8 {
			t.Fatalf("prefixLookup[%d] = {type=%d, bits=%d, vbits=%d}, want literal(1,8)",
				i, e.tokenType, e.prefixBits, e.valueBits)
		}
	}

	// Verify token 1 (match, prefix=10001): 10001_xxxx → indices 272..287
	for i := 272; i < 288; i++ {
		e := prefixLookup[i]
		if e.tokenType != 1 || e.valueBase != 0 || e.valueBits != 5 {
			t.Fatalf("prefixLookup[%d] = {type=%d, base=%d, vbits=%d}, want match(0,5)",
				i, e.tokenType, e.valueBase, e.valueBits)
		}
	}

	// Verify token 6 (literal 0x00, prefix=11000): 11000_xxxx → indices 384..399
	for i := 384; i < 400; i++ {
		e := prefixLookup[i]
		if e.tokenType != 0 || e.valueBase != 0x00 || e.valueBits != 0 {
			t.Fatalf("prefixLookup[%d] = {type=%d, base=0x%02x, vbits=%d}, want literal(0x00,0)",
				i, e.tokenType, e.valueBase, e.valueBits)
		}
	}

	// Verify token 7 (literal 0x01, prefix=11001): 11001_xxxx → indices 400..415
	for i := 400; i < 416; i++ {
		e := prefixLookup[i]
		if e.tokenType != 0 || e.valueBase != 0x01 || e.valueBits != 0 {
			t.Fatalf("prefixLookup[%d] = {type=%d, base=0x%02x, vbits=%d}, want literal(0x01,0)",
				i, e.tokenType, e.valueBase, e.valueBits)
		}
	}

	// Count populated entries — table has a few unused bit patterns
	populated := 0
	for i := range prefixLookup {
		if prefixLookup[i].prefixBits != 0 {
			populated++
		}
	}
	// 40 tokens cover most of the 512 space; a few 9-bit patterns are unused
	if populated < 480 {
		t.Fatalf("too few populated entries = %d, expected >= 480", populated)
	}
}
