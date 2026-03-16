package orders

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBitmapCacheSetGet(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{10, 10, 10})

	data := make([]byte, 4*4*4) // 4x4 BGRX
	for i := range data {
		data[i] = byte(i)
	}

	bc.Set(0, 5, 4, 4, data)

	entry := bc.Get(0, 5)
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if entry.Width != 4 || entry.Height != 4 {
		t.Fatalf("got %dx%d, want 4x4", entry.Width, entry.Height)
	}
	if !bytes.Equal(entry.Data, data) {
		t.Fatal("pixel data mismatch")
	}

	// Different cache
	if bc.Get(1, 5) != nil {
		t.Fatal("expected nil for different cache")
	}
	// Out of range
	if bc.Get(0, 99) != nil {
		t.Fatal("expected nil for out of range index")
	}
}

func TestBitmapCacheOverwrite(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{5, 5, 5})

	data1 := make([]byte, 2*2*4)
	for i := range data1 {
		data1[i] = 0xAA
	}
	bc.Set(0, 0, 2, 2, data1)

	data2 := make([]byte, 2*2*4)
	for i := range data2 {
		data2[i] = 0xBB
	}
	bc.Set(0, 0, 2, 2, data2)

	entry := bc.Get(0, 0)
	if entry == nil {
		t.Fatal("expected non-nil")
	}
	if !bytes.Equal(entry.Data, data2) {
		t.Fatal("expected overwritten data")
	}
}

func TestBitmapCacheWaitingList(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{10, 10, 10, 10, 10})

	data := make([]byte, 2*2*4)
	for i := range data {
		data[i] = 0xDD
	}

	// Store at waiting list index
	bc.Set(0, WaitingListIndex, 2, 2, data)

	// Retrieve via waiting list index
	entry := bc.Get(0, WaitingListIndex)
	if entry == nil {
		t.Fatal("expected non-nil entry at waiting list slot")
	}
	if !bytes.Equal(entry.Data, data) {
		t.Fatal("waiting list pixel data mismatch")
	}

	// Regular slot 0 is unaffected
	if bc.Get(0, 0) != nil {
		t.Fatal("regular slot 0 should be nil")
	}
}

func TestBitmapCacheFiveCaches(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{10, 10, 10, 10, 10})

	data := make([]byte, 2*2*4)
	for i := range data {
		data[i] = 0xEE
	}

	// Store in cache 4 (the 5th cache)
	bc.Set(4, 7, 2, 2, data)
	entry := bc.Get(4, 7)
	if entry == nil {
		t.Fatal("expected non-nil entry in cache 4")
	}
	if !bytes.Equal(entry.Data, data) {
		t.Fatal("cache 4 pixel data mismatch")
	}

	// Cache 5 (out of range) should be rejected
	bc.Set(5, 0, 2, 2, data)
	if bc.Get(5, 0) != nil {
		t.Fatal("cache 5 should be out of range")
	}
}

func TestBitmapCacheReset(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{5, 5, 5})

	data := make([]byte, 2*2*4)
	bc.Set(0, 0, 2, 2, data)
	bc.Set(1, 3, 2, 2, data)

	bc.Reset()

	if bc.Get(0, 0) != nil {
		t.Fatal("expected nil after reset")
	}
	if bc.Get(1, 3) != nil {
		t.Fatal("expected nil after reset")
	}
}

func TestReadVar2(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		val  uint16
		off  int
	}{
		{"1-byte form", []byte{0x05}, 5, 1},               // bit 7 clear → value = 5
		{"2-byte form", []byte{0x85, 0x03}, 0x0503, 2},    // bit 7 set → ((0x05)<<8)|0x03
		{"2-byte zero", []byte{0x80, 0x00}, 0, 2},         // bit 7 set, value = 0
		{"1-byte zero", []byte{0x00}, 0, 1},               // single byte zero
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, off := readVar2(tt.data, 0)
			if v != tt.val || off != tt.off {
				t.Fatalf("readVar2(%x) = (%d, %d), want (%d, %d)", tt.data, v, off, tt.val, tt.off)
			}
		})
	}
}

func TestReadVar2_Truncated(t *testing.T) {
	// Empty data
	_, off := readVar2(nil, 0)
	if off != -1 {
		t.Fatal("expected -1 for empty data")
	}
	// 2-byte form but only 1 byte available
	_, off = readVar2([]byte{0x85}, 0)
	if off != -1 {
		t.Fatal("expected -1 for truncated 2-byte form")
	}
}

func TestReadVar4(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		val  uint32
		off  int
	}{
		{"0 extra bytes", []byte{0x05}, 5, 1},                       // top 2 bits = 00, value = 0x05
		{"1 extra byte", []byte{0x45, 0x03}, 0x0503, 2},             // top 2 bits = 01
		{"2 extra bytes", []byte{0x85, 0x03, 0x07}, 0x050307, 3},    // top 2 bits = 10
		{"3 extra bytes", []byte{0xC5, 0x03, 0x07, 0x09}, 0x05030709, 4}, // top 2 bits = 11
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, off := readVar4(tt.data, 0)
			if v != tt.val || off != tt.off {
				t.Fatalf("readVar4(%x) = (0x%X, %d), want (0x%X, %d)", tt.data, v, off, tt.val, tt.off)
			}
		})
	}
}

func TestDecodeCacheBitmapV2_Uncompressed32bpp(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{600, 300, 2048})

	// Build secData for an uncompressed 32bpp 2x2 bitmap (wire format: BGRX)
	// V2 wire format (MS-RDPEGDI 2.2.2.2.1.3.3): width(u8) height(u8) bufsize(u16BE) cacheIdx(1-2) data
	w, h := 2, 2
	wirePixels := make([]byte, w*h*4)
	for i := 0; i < len(wirePixels); i += 4 {
		wirePixels[i] = 0x10   // B
		wirePixels[i+1] = 0x20 // G
		wirePixels[i+2] = 0x30 // R
		wirePixels[i+3] = 0xFF // X (padding)
	}

	var secData []byte
	secData = append(secData, byte(w))              // width
	secData = append(secData, byte(h))              // height
	secData = append(secData, 0x00, byte(len(wirePixels))) // bufsize (u16 BE)
	secData = append(secData, 0x03)                 // cacheIndex (short format)
	secData = append(secData, wirePixels...)

	// extraFlags: cacheId=1(bits 0-2) | bppMode=6(bits 3-5, for 32bpp: bppBytes=4, mode=4+2=6)
	extraFlags := uint16(1) | (6 << bmpCache2ModeShift)

	DecodeCacheBitmapV2(&bc, extraFlags, secData, false, nil, nil)

	entry := bc.Get(1, 3)
	if entry == nil {
		t.Fatal("expected cached entry")
	}
	if entry.Width != w || entry.Height != h {
		t.Fatalf("got %dx%d, want %dx%d", entry.Width, entry.Height, w, h)
	}
	// Uncompressed path flips rows (bottom-up), then BGRX→RGBA.
	// For 2x2 with all identical pixels, flipping doesn't change the result.
	// BGRX(0x10,0x20,0x30,0xFF) → RGBA(0x30,0x20,0x10,0xFF)
	wantPixels := make([]byte, w*h*4)
	for i := 0; i < len(wantPixels); i += 4 {
		wantPixels[i] = 0x30   // R
		wantPixels[i+1] = 0x20 // G
		wantPixels[i+2] = 0x10 // B
		wantPixels[i+3] = 0xFF // A
	}
	if !bytes.Equal(entry.Data, wantPixels) {
		t.Fatalf("pixel data mismatch:\n got: %X\nwant: %X", entry.Data, wantPixels)
	}
}

func TestDecodeCacheBitmapV2_HeightSameAsWidth(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{600, 300, 2048})

	w := 3
	pixels := make([]byte, w*w*4)

	var secData []byte
	secData = append(secData, byte(w))                    // width=3
	// No height field — bmpCache2Square flag
	bufsize := len(pixels)
	secData = append(secData, byte(bufsize>>8), byte(bufsize)) // bufsize (u16 BE)
	secData = append(secData, 0x00)                       // cacheIndex = 0
	secData = append(secData, pixels...)

	// extraFlags: cacheId=0 | bppMode=6(bits 3-5) | square(bit 7)
	extraFlags := uint16(0) | (6 << bmpCache2ModeShift) | bmpCache2Square

	DecodeCacheBitmapV2(&bc, extraFlags, secData, false, nil, nil)

	entry := bc.Get(0, 0)
	if entry == nil {
		t.Fatal("expected cached entry")
	}
	if entry.Width != w || entry.Height != w {
		t.Fatalf("got %dx%d, want %dx%d", entry.Width, entry.Height, w, w)
	}
}

func TestDecodeCacheBitmapV2_Truncated(t *testing.T) {
	var bc BitmapCache
	bc.Init([NumBitmapCaches]int{10, 10, 10})

	// Valid bppMode (6 for 32bpp) but truncated secData should not panic
	flags := uint16(6 << bmpCache2ModeShift)
	DecodeCacheBitmapV2(&bc, flags, nil, false, nil, nil)
	DecodeCacheBitmapV2(&bc, flags, []byte{0x02}, false, nil, nil) // just width byte
}

func TestNormalizeToRGBA32_24bpp(t *testing.T) {
	// 2 pixels, 24bpp BGR wire format → RGBA
	src := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	dst := make([]byte, 2*4)
	normalizeToRGBA32(dst, src, 2, 1, 24, nil)

	// src B=0x10,G=0x20,R=0x30 → RGBA: R=0x30,G=0x20,B=0x10,A=0xFF
	// src B=0x40,G=0x50,R=0x60 → RGBA: R=0x60,G=0x50,B=0x40,A=0xFF
	want := []byte{0x30, 0x20, 0x10, 0xFF, 0x60, 0x50, 0x40, 0xFF}
	if !bytes.Equal(dst, want) {
		t.Fatalf("got %x, want %x", dst, want)
	}
}

func TestExtraFlagsThreaded(t *testing.T) {
	// Verify that DecodeOrders properly threads extraFlags to Order.ExtraFlags
	// Build a minimal secondary order
	var buf []byte
	// controlFlags: TSStandard | TSSecondary
	buf = append(buf, TSStandard|TSSecondary)
	// orderLength (int16 LE): total = orderLength + 13, so orderLength = total - 13
	// total = 1(ctrl) + 2(ordLen) + 2(extraFlags) + 1(ordType) + 2(data) = 8
	// orderLength = 8 - 13 = -5
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(0xFFFB)) // int16(-5)
	buf = append(buf, lenBuf[:]...)
	// extraFlags
	binary.LittleEndian.PutUint16(lenBuf[:], 0x1234)
	buf = append(buf, lenBuf[:]...)
	// orderType
	buf = append(buf, SecondaryCacheGlyph)
	// 2 bytes of sec data
	buf = append(buf, 0, 0)

	var state DecoderState
	var gotFlags uint16
	DecodeOrders(&state, buf, 1, func(s *DecoderState, ord *Order) {
		gotFlags = ord.ExtraFlags
	})
	if gotFlags != 0x1234 {
		t.Fatalf("ExtraFlags = 0x%04X, want 0x1234", gotFlags)
	}
}
