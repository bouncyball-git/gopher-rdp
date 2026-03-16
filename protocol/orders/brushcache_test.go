package orders

import "testing"

func TestBrushCacheGetSet(t *testing.T) {
	var bc BrushCache

	// Get from empty cache returns nil
	if bc.Get(0) != nil {
		t.Fatal("expected nil for empty cache")
	}

	// Set and get mono brush
	brush := CachedBrush{Mono: true}
	brush.MonoData = [8]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	bc.Set(5, &brush)

	got := bc.Get(5)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if !got.Mono {
		t.Fatal("expected Mono=true")
	}
	if got.MonoData != brush.MonoData {
		t.Fatalf("MonoData mismatch: got %v, want %v", got.MonoData, brush.MonoData)
	}

	// Out-of-range index
	bc.Set(64, &brush)
	if bc.Get(64) != nil {
		t.Fatal("expected nil for out-of-range index")
	}

	// Reset clears entries
	bc.Reset()
	if bc.Get(5) != nil {
		t.Fatal("expected nil after reset")
	}
}

func TestDecodeCacheBrush1BPP(t *testing.T) {
	// cacheEntry=3, format=BMF_1BPP(1), cx=8, cy=8, style=3, iBytes=8, data=8 bytes
	data := []byte{
		3,    // cacheEntry
		0x01, // iBitmapFormat = BMF_1BPP
		8, 8, // cx, cy
		0x03, // style
		8,    // iBytes
		0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, // brushData
	}

	var bc BrushCache
	DecodeCacheBrush(&bc, data)

	got := bc.Get(3)
	if got == nil {
		t.Fatal("expected non-nil entry for index 3")
	}
	if !got.Mono {
		t.Fatal("expected Mono=true for 1BPP brush")
	}
	// Wire bytes are reversed during decoding (MS-RDPEGDI 2.2.2.2.1.2.7)
	want := [8]byte{0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA}
	if got.MonoData != want {
		t.Fatalf("MonoData: got %v, want %v", got.MonoData, want)
	}
}

func TestDecodeCacheBrushCompressed(t *testing.T) {
	// Compressed 24bpp brush: 16 bytes of 2-bit indices + 4 palette entries (3 bytes each)
	// Palette: color0=BGR(10,20,30), color1=BGR(40,50,60), color2=BGR(70,80,90), color3=BGR(A0,B0,C0)
	// Index table: all pixels use color index 2 (binary 10 repeated → 0xAA per byte)
	indices := make([]byte, 16)
	for i := range indices {
		indices[i] = 0xAA // Each byte: 10 10 10 10 → all index 2
	}
	palette := []byte{
		10, 20, 30, // color 0
		40, 50, 60, // color 1
		70, 80, 90, // color 2
		0xA0, 0xB0, 0xC0, // color 3
	}

	iBytes := byte(len(indices) + len(palette)) // 16 + 12 = 28
	data := []byte{
		7,    // cacheEntry
		0x05, // iBitmapFormat = BMF_24BPP
		8, 8, // cx, cy
		0x03, // style
		iBytes,
	}
	data = append(data, indices...)
	data = append(data, palette...)

	var bc BrushCache
	DecodeCacheBrush(&bc, data)

	got := bc.Get(7)
	if got == nil {
		t.Fatal("expected non-nil entry for index 7")
	}
	if got.Mono {
		t.Fatal("expected Mono=false for color brush")
	}

	// All 64 pixels should be color 2 = BGR(70,80,90) → RGBA(90,80,70,0xFF)
	for i := range 64 {
		off := i * 4
		r, g, b, a := got.Data[off], got.Data[off+1], got.Data[off+2], got.Data[off+3]
		if r != 90 || g != 80 || b != 70 || a != 0xFF {
			t.Fatalf("pixel %d: got RGBA(%d,%d,%d,%d), want (90,80,70,255)", i, r, g, b, a)
		}
	}
}

func TestDecodeCacheBrushUncompressed24(t *testing.T) {
	// Uncompressed 24bpp: 64 pixels × 3 bytes = 192 bytes
	pixels := make([]byte, 192)
	for i := range 64 {
		pixels[i*3] = 0x11   // B
		pixels[i*3+1] = 0x22 // G
		pixels[i*3+2] = 0x33 // R
	}

	data := []byte{
		2,          // cacheEntry
		0x05,       // iBitmapFormat = BMF_24BPP
		8, 8,       // cx, cy
		0x03,       // style
		byte(192),  // iBytes
	}
	data = append(data, pixels...)

	var bc BrushCache
	DecodeCacheBrush(&bc, data)

	got := bc.Get(2)
	if got == nil {
		t.Fatal("expected non-nil entry")
	}
	if got.Mono {
		t.Fatal("expected color brush")
	}
	for i := range 64 {
		off := i * 4
		if got.Data[off] != 0x33 || got.Data[off+1] != 0x22 || got.Data[off+2] != 0x11 || got.Data[off+3] != 0xFF {
			t.Fatalf("pixel %d: got RGBA(%02x,%02x,%02x,%02x), want (33,22,11,FF)",
				i, got.Data[off], got.Data[off+1], got.Data[off+2], got.Data[off+3])
		}
	}
}

func TestDecodeCacheBrushUncompressed16(t *testing.T) {
	// Uncompressed 16bpp: 64 pixels × 2 bytes = 128 bytes
	// RGB565 white = 0xFFFF → R=31, G=63, B=31 → R=0xFF, G=0xFF, B=0xFF
	pixels := make([]byte, 128)
	for i := range 64 {
		pixels[i*2] = 0xFF
		pixels[i*2+1] = 0xFF
	}

	data := []byte{
		0,         // cacheEntry
		0x04,      // iBitmapFormat = BMF_16BPP
		8, 8,      // cx, cy
		0x03,      // style
		byte(128), // iBytes
	}
	data = append(data, pixels...)

	var bc BrushCache
	DecodeCacheBrush(&bc, data)

	got := bc.Get(0)
	if got == nil {
		t.Fatal("expected non-nil entry")
	}
	for i := range 64 {
		off := i * 4
		if got.Data[off] != 0xFF || got.Data[off+1] != 0xFF || got.Data[off+2] != 0xFF {
			t.Fatalf("pixel %d: got BGR(%02x,%02x,%02x), want (FF,FF,FF)",
				i, got.Data[off], got.Data[off+1], got.Data[off+2])
		}
	}
}

func TestHatchPatterns(t *testing.T) {
	if len(HatchPatterns) != 6 {
		t.Fatalf("expected 6 hatch patterns, got %d", len(HatchPatterns))
	}

	// HS_HORIZONTAL: row 3 is 0xFF, others are 0x00
	for i, v := range HatchPatterns[0] {
		if i == 3 {
			if v != 0xFF {
				t.Errorf("HS_HORIZONTAL row %d: got 0x%02X, want 0xFF", i, v)
			}
		} else {
			if v != 0x00 {
				t.Errorf("HS_HORIZONTAL row %d: got 0x%02X, want 0x00", i, v)
			}
		}
	}

	// HS_VERTICAL: each row has bit 4 set (0x08)
	for i, v := range HatchPatterns[1] {
		if v != 0x08 {
			t.Errorf("HS_VERTICAL row %d: got 0x%02X, want 0x08", i, v)
		}
	}

	// HS_CROSS: combination of horizontal and vertical
	for i, v := range HatchPatterns[4] {
		if i == 3 {
			if v != 0xFF {
				t.Errorf("HS_CROSS row %d: got 0x%02X, want 0xFF", i, v)
			}
		} else {
			if v != 0x08 {
				t.Errorf("HS_CROSS row %d: got 0x%02X, want 0x08", i, v)
			}
		}
	}
}

func TestDecodeCacheBrushTooShort(t *testing.T) {
	var bc BrushCache

	// Data too short for header
	DecodeCacheBrush(&bc, []byte{0, 1, 8, 8})
	if bc.Get(0) != nil {
		t.Fatal("expected nil for truncated data")
	}

	// 1BPP with insufficient brush data
	DecodeCacheBrush(&bc, []byte{0, 0x01, 8, 8, 0x03, 4, 0, 0, 0, 0})
	if bc.Get(0) != nil {
		t.Fatal("expected nil for truncated 1BPP data")
	}
}
