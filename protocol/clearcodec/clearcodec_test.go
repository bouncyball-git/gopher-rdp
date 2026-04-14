package clearcodec

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestDecodeResidualSolidColor(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))

	// Build a ClearCodec packet with residual layer only (solid red, 4 pixels)
	// Residual: B=0x00, G=0x00, R=0xFF, runLengthFactor=4
	var src []byte
	src = append(src, 0x00) // glyphFlags
	src = append(src, 0x00) // seqNumber
	// composition header
	residual := []byte{0x00, 0x00, 0xFF, 4} // B, G, R, runLength=4
	var comp [12]byte
	binary.LittleEndian.PutUint32(comp[0:4], uint32(len(residual)))
	binary.LittleEndian.PutUint32(comp[4:8], 0)  // bandsLen
	binary.LittleEndian.PutUint32(comp[8:12], 0)  // subcodecLen
	src = append(src, comp[:]...)
	src = append(src, residual...)

	got, err := d.Decompress(nil, 2, 2, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}

	if len(got) != 16 {
		t.Fatalf("output size = %d, want 16", len(got))
	}

	// Check all 4 pixels are red (R=0xFF, G=0, B=0, A=0xFF)
	for px := 0; px < 4; px++ {
		off := px * 4
		if got[off] != 0xFF || got[off+1] != 0x00 || got[off+2] != 0x00 || got[off+3] != 0xFF {
			t.Fatalf("pixel[%d] = (%d,%d,%d,%d), want (255,0,0,255)",
				px, got[off], got[off+1], got[off+2], got[off+3])
		}
	}
}

func TestDecodeResidualCascadeRunLength(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))

	// Test cascading run-length: factor=0xFF → read u16 replacement
	var src []byte
	src = append(src, 0x00, 0x00) // glyphFlags, seqNumber

	// Residual: wire B=0xAA, G=0xBB, R=0xCC → output R=0xCC, G=0xBB, B=0xAA
	residual := []byte{0xAA, 0xBB, 0xCC, 0xFF}
	var rl16 [2]byte
	binary.LittleEndian.PutUint16(rl16[:], 300)
	residual = append(residual, rl16[:]...)

	var comp [12]byte
	binary.LittleEndian.PutUint32(comp[0:4], uint32(len(residual)))
	src = append(src, comp[:]...)
	src = append(src, residual...)

	// 300 pixels → 20x15
	got, err := d.Decompress(nil, 20, 15, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}

	// Check first and last pixel (RGBA order: R=0xCC, G=0xBB, B=0xAA)
	if got[0] != 0xCC || got[1] != 0xBB || got[2] != 0xAA {
		t.Fatalf("first pixel wrong: (%d,%d,%d)", got[0], got[1], got[2])
	}
	lastOff := 299 * 4
	if got[lastOff] != 0xCC || got[lastOff+1] != 0xBB || got[lastOff+2] != 0xAA {
		t.Fatalf("last pixel wrong: (%d,%d,%d)", got[lastOff], got[lastOff+1], got[lastOff+2])
	}
}

func TestDecodeSubcodecRawBGR(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))

	var src []byte
	src = append(src, 0x00, 0x00) // glyphFlags, seqNumber

	// Subcodec rect: x=0, y=0, w=2, h=1, raw BGR
	var subData []byte
	var rectHdr [13]byte
	binary.LittleEndian.PutUint16(rectHdr[0:2], 0)  // x
	binary.LittleEndian.PutUint16(rectHdr[2:4], 0)  // y
	binary.LittleEndian.PutUint16(rectHdr[4:6], 2)  // w
	binary.LittleEndian.PutUint16(rectHdr[6:8], 1)  // h
	binary.LittleEndian.PutUint32(rectHdr[8:12], 6) // dataLen
	rectHdr[12] = 0                                   // raw BGR
	subData = append(subData, rectHdr[:]...)
	subData = append(subData, 0xFF, 0x00, 0x00) // blue pixel (BGR)
	subData = append(subData, 0x00, 0xFF, 0x00) // green pixel (BGR)

	var comp [12]byte
	binary.LittleEndian.PutUint32(comp[0:4], 0)
	binary.LittleEndian.PutUint32(comp[4:8], 0)
	binary.LittleEndian.PutUint32(comp[8:12], uint32(len(subData)))
	src = append(src, comp[:]...)
	src = append(src, subData...)

	got, err := d.Decompress(nil, 2, 1, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}

	// Wire pixel 0: B=0xFF,G=0,R=0 (blue) → RGBA: R=0,G=0,B=0xFF,A=0xFF
	if got[0] != 0x00 || got[1] != 0x00 || got[2] != 0xFF || got[3] != 0xFF {
		t.Fatalf("pixel[0] = (%d,%d,%d,%d), want (0,0,255,255)",
			got[0], got[1], got[2], got[3])
	}
	// Wire pixel 1: B=0,G=0xFF,R=0 (green) → RGBA: R=0,G=0xFF,B=0,A=0xFF
	if got[4] != 0x00 || got[5] != 0xFF || got[6] != 0x00 || got[7] != 0xFF {
		t.Fatalf("pixel[1] = (%d,%d,%d,%d), want (0,255,0,255)",
			got[4], got[5], got[6], got[7])
	}
}

func TestGlyphCacheHit(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))

	// First: cache miss with glyph index
	var src1 []byte
	src1 = append(src1, flagGlyphIndex) // GLYPH_INDEX
	src1 = append(src1, 0x00)           // seqNumber
	src1 = append(src1, 0x05, 0x00)     // glyphIndex = 5

	residual := []byte{0xFF, 0x00, 0x00, 1} // wire B=0xFF, run=1 → RGBA: R=0,G=0,B=0xFF
	var comp [12]byte
	binary.LittleEndian.PutUint32(comp[0:4], uint32(len(residual)))
	src1 = append(src1, comp[:]...)
	src1 = append(src1, residual...)

	_, err := d.Decompress(nil, 1, 1, src1)
	if err != nil {
		t.Fatalf("First decompress error: %v", err)
	}

	// Second: glyph hit
	src2 := []byte{
		flagGlyphIndex | flagGlyphHit,
		0x01, // seqNumber (incremented)
		0x05, 0x00,
	}

	got, err := d.Decompress(nil, 1, 1, src2)
	if err != nil {
		t.Fatalf("Glyph hit error: %v", err)
	}

	if got[0] != 0x00 || got[1] != 0x00 || got[2] != 0xFF {
		t.Fatalf("glyph pixel = (%d,%d,%d), want (0,0,255)", got[0], got[1], got[2])
	}
}

func TestCacheReset(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))
	d.vbarCursor = 100
	d.shortVBarCursor = 50

	src := []byte{flagCacheReset, 0x00}
	var comp [12]byte
	src = append(src, comp[:]...)

	_, err := d.Decompress(nil, 1, 1, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}

	if d.vbarCursor != 0 || d.shortVBarCursor != 0 {
		t.Fatalf("cursors not reset: vbar=%d, short=%d", d.vbarCursor, d.shortVBarCursor)
	}
}

func TestSequenceNumberMismatch(t *testing.T) {
	// Abort on mismatch (matching FreeRDP), but advance seqNumber so
	// subsequent tiles can re-sync.
	d := New(slog.New(slog.DiscardHandler))
	d.seqNumber = 5 // expect seq=5

	src := []byte{0x00, 0x03} // seqNumber=3, mismatch
	var comp [12]byte
	src = append(src, comp[:]...)

	_, err := d.Decompress(nil, 1, 1, src)
	if err == nil {
		t.Fatal("expected error on sequence number mismatch, got nil")
	}
	// seqNumber should advance past the mismatch so later tiles can re-sync
	if d.seqNumber != 4 {
		t.Fatalf("expected seqNumber=4 after processing seq=3, got %d", d.seqNumber)
	}
}

func TestGlyphHitWithoutIndex(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))

	src := []byte{flagGlyphHit, 0x00} // GLYPH_HIT without GLYPH_INDEX
	var comp [12]byte
	src = append(src, comp[:]...)

	_, err := d.Decompress(nil, 1, 1, src)
	if err == nil {
		t.Fatal("expected bad glyph flags error")
	}
}

func TestBufferReuse(t *testing.T) {
	d := New(slog.New(slog.DiscardHandler))

	src := []byte{0x00, 0x00}
	var comp [12]byte
	src = append(src, comp[:]...)

	dst := make([]byte, 0, 100)
	got, err := d.Decompress(dst, 1, 1, src)
	if err != nil {
		t.Fatalf("Decompress error: %v", err)
	}
	if cap(got) != 100 {
		t.Fatalf("buffer not reused: cap=%d, want 100", cap(got))
	}
}
