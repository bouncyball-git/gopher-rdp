package orders

import (
	"encoding/binary"
	"testing"
)

func putI16LE(b []byte, v int16) {
	binary.LittleEndian.PutUint16(b, uint16(v))
}

func TestGlyphCacheSetGet(t *testing.T) {
	var cache GlyphCache

	g := CachedGlyph{
		X: -1, Y: -5, CX: 8, CY: 10,
		Data: make([]byte, 10), // 8px wide = 1 byte/row * 10 rows
	}
	g.Data[0] = 0xFF // top row all set

	cache.Set(0, 42, &g)

	got := cache.Get(0, 42)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.X != -1 || got.Y != -5 || got.CX != 8 || got.CY != 10 {
		t.Errorf("fields mismatch: X=%d Y=%d CX=%d CY=%d", got.X, got.Y, got.CX, got.CY)
	}
	if got.Data[0] != 0xFF {
		t.Errorf("data[0] = 0x%02X, want 0xFF", got.Data[0])
	}
}

func TestGlyphCacheGetInvalid(t *testing.T) {
	var cache GlyphCache
	if cache.Get(0, 0) != nil {
		t.Error("expected nil for unset entry")
	}
	if cache.Get(10, 0) != nil {
		t.Error("expected nil for out-of-range cacheID")
	}
	if cache.Get(0, 254) != nil {
		t.Error("expected nil for out-of-range index")
	}
}

func TestGlyphCacheOverwrite(t *testing.T) {
	var cache GlyphCache

	g1 := CachedGlyph{CX: 8, CY: 1, Data: []byte{0xAA}}
	cache.Set(0, 0, &g1)

	g2 := CachedGlyph{CX: 8, CY: 1, Data: []byte{0x55}}
	cache.Set(0, 0, &g2)

	got := cache.Get(0, 0)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Data[0] != 0x55 {
		t.Errorf("data[0] = 0x%02X, want 0x55 after overwrite", got.Data[0])
	}
}

func TestDecodeCacheGlyph(t *testing.T) {
	// Build a Cache Glyph Rev 1 secondary order payload:
	// cacheId=2, cGlyphs=2
	// Glyph 0: index=5, x=0, y=-3, cx=8, cy=2, data=[0xFF, 0x80]
	// Glyph 1: index=10, x=1, y=-2, cx=4, cy=1, data=[0xF0]
	var buf []byte
	buf = append(buf, 2) // cacheId
	buf = append(buf, 2) // cGlyphs

	// Glyph 0
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], 5) // cacheIndex
	buf = append(buf, tmp[:]...)
	putI16LE(tmp[:], 0) // x
	buf = append(buf, tmp[:]...)
	putI16LE(tmp[:], -3) // y
	buf = append(buf, tmp[:]...)
	binary.LittleEndian.PutUint16(tmp[:], 8) // cx
	buf = append(buf, tmp[:]...)
	binary.LittleEndian.PutUint16(tmp[:], 2) // cy
	buf = append(buf, tmp[:]...)
	buf = append(buf, 0xFF, 0x80) // 1 byte/row * 2 rows = 2 bytes
	buf = append(buf, 0x00, 0x00) // 4-byte alignment padding (2 → 4)

	// Glyph 1
	binary.LittleEndian.PutUint16(tmp[:], 10) // cacheIndex
	buf = append(buf, tmp[:]...)
	putI16LE(tmp[:], 1) // x
	buf = append(buf, tmp[:]...)
	putI16LE(tmp[:], -2) // y
	buf = append(buf, tmp[:]...)
	binary.LittleEndian.PutUint16(tmp[:], 4) // cx
	buf = append(buf, tmp[:]...)
	binary.LittleEndian.PutUint16(tmp[:], 1) // cy
	buf = append(buf, tmp[:]...)
	buf = append(buf, 0xF0)             // 1 byte/row * 1 row = 1 byte
	buf = append(buf, 0x00, 0x00, 0x00) // 4-byte alignment padding (1 → 4)

	var cache GlyphCache
	DecodeCacheGlyph(&cache, buf)

	g0 := cache.Get(2, 5)
	if g0 == nil {
		t.Fatal("glyph 0 not cached")
	}
	if g0.CX != 8 || g0.CY != 2 || g0.Y != -3 {
		t.Errorf("glyph 0: CX=%d CY=%d Y=%d", g0.CX, g0.CY, g0.Y)
	}
	if g0.Data[0] != 0xFF || g0.Data[1] != 0x80 {
		t.Errorf("glyph 0 data: %v", g0.Data)
	}

	g1 := cache.Get(2, 10)
	if g1 == nil {
		t.Fatal("glyph 1 not cached")
	}
	if g1.CX != 4 || g1.CY != 1 || g1.X != 1 || g1.Y != -2 {
		t.Errorf("glyph 1: CX=%d CY=%d X=%d Y=%d", g1.CX, g1.CY, g1.X, g1.Y)
	}
	if g1.Data[0] != 0xF0 {
		t.Errorf("glyph 1 data[0] = 0x%02X", g1.Data[0])
	}
}

func TestDecodeCacheGlyphTruncated(t *testing.T) {
	// Should not panic on truncated data
	var cache GlyphCache
	DecodeCacheGlyph(&cache, nil)
	DecodeCacheGlyph(&cache, []byte{0})
	DecodeCacheGlyph(&cache, []byte{0, 1}) // 1 glyph, but no glyph data
}
