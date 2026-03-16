package orders

import (
	"encoding/binary"
	"testing"
)

// buildSecondaryOrder builds a secondary order with the given type and data.
func buildSecondaryOrder(orderType byte, data []byte) []byte {
	// controlFlags | orderLength(i16) | extraFlags(u16) | orderType(u8) | data
	// MS-RDPEGDI 2.2.2.2.1.2.1.1: total = orderLength + 13 (including controlFlags).
	// total = 1(controlFlags) + 2(orderLength) + 2(extraFlags) + 1(orderType) + len(data) = 6 + len(data)
	// orderLength = total - 13 = len(data) - 7
	orderLength := len(data) - 7
	var buf []byte
	buf = append(buf, TSStandard|TSSecondary) // controlFlags
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], uint16(int16(orderLength)))
	buf = append(buf, tmp[:]...) // orderLength
	binary.LittleEndian.PutUint16(tmp[:], 0)
	buf = append(buf, tmp[:]...) // extraFlags
	buf = append(buf, orderType) // orderType
	buf = append(buf, data...)
	return buf
}

// buildPrimaryGlyphIndex builds a minimal primary GlyphIndex order.
func buildPrimaryGlyphIndex(cacheID uint8, varData []byte) []byte {
	// controlFlags: TSStandard | TSTypeChange, all field flags set for fields we encode
	var buf []byte
	controlFlags := byte(TSStandard | TSTypeChange)
	buf = append(buf, controlFlags)
	buf = append(buf, OrderGlyphIndex) // orderType

	// Field flags: 22 fields → 3 bytes
	// Set bits 0 (cacheId) and 21 (varBytes)
	var flags [3]byte
	flags[0] = 0x01  // bit 0: cacheId
	flags[2] = 0x20  // bit 21: varBytes (bit 5 of byte 2)
	buf = append(buf, flags[:]...)

	// Field 0: cacheId
	buf = append(buf, cacheID)

	// Field 21: varBytes (length-prefixed)
	buf = append(buf, byte(len(varData)))
	buf = append(buf, varData...)

	return buf
}

func TestDecodeOrdersSecondary(t *testing.T) {
	// Build a CacheGlyph secondary order
	glyphData := []byte{0, 0} // cacheId=0, cGlyphs=0
	orderData := buildSecondaryOrder(SecondaryCacheGlyph, glyphData)

	var state DecoderState
	var called int
	var gotOrd Order

	DecodeOrders(&state, orderData, 1, func(s *DecoderState, ord *Order) {
		called++
		gotOrd = *ord
	})

	if called != 1 {
		t.Fatalf("callback called %d times, want 1", called)
	}
	if !gotOrd.IsSecondary {
		t.Error("expected IsSecondary=true")
	}
	if gotOrd.SecondaryType != SecondaryCacheGlyph {
		t.Errorf("SecondaryType = 0x%02X, want 0x%02X", gotOrd.SecondaryType, SecondaryCacheGlyph)
	}
}

func TestDecodeOrdersPrimaryGlyphIndex(t *testing.T) {
	varData := []byte{0x05} // single glyph index 5
	orderData := buildPrimaryGlyphIndex(3, varData)

	var state DecoderState
	var called int

	DecodeOrders(&state, orderData, 1, func(s *DecoderState, ord *Order) {
		called++
		if ord.IsSecondary {
			t.Error("expected primary order")
		}
		if ord.Type != OrderGlyphIndex {
			t.Errorf("Type = 0x%02X, want 0x%02X", ord.Type, OrderGlyphIndex)
		}
		if s.GlyphIndex.CacheID != 3 {
			t.Errorf("CacheID = %d, want 3", s.GlyphIndex.CacheID)
		}
		if s.GlyphIndex.VarLen != 1 || s.GlyphIndex.VarBytes[0] != 0x05 {
			t.Errorf("VarBytes mismatch: len=%d data[0]=0x%02X",
				s.GlyphIndex.VarLen, s.GlyphIndex.VarBytes[0])
		}
	})

	if called != 1 {
		t.Fatalf("callback called %d times, want 1", called)
	}
}

func TestDecodeOrdersStateful(t *testing.T) {
	// First order sets CacheID=3
	order1 := buildPrimaryGlyphIndex(3, []byte{0x01})

	// Second order: same type (no TSTypeChange), only updates varBytes
	var order2 []byte
	controlFlags := byte(TSStandard) // no TSTypeChange — reuses last order type
	order2 = append(order2, controlFlags)
	// Field flags: only bit 21 (varBytes)
	var flags [3]byte
	flags[2] = 0x20 // bit 21
	order2 = append(order2, flags[:]...)
	order2 = append(order2, 2)          // varLen
	order2 = append(order2, 0x0A, 0x0B) // var data

	// Concatenate both orders
	allData := append(order1, order2...)

	var state DecoderState
	calls := 0

	DecodeOrders(&state, allData, 2, func(s *DecoderState, ord *Order) {
		calls++
		if calls == 2 {
			// CacheID should still be 3 from the first order
			if s.GlyphIndex.CacheID != 3 {
				t.Errorf("order 2: CacheID = %d, want 3 (stateful)", s.GlyphIndex.CacheID)
			}
			if s.GlyphIndex.VarLen != 2 {
				t.Errorf("order 2: VarLen = %d, want 2", s.GlyphIndex.VarLen)
			}
		}
	})

	if calls != 2 {
		t.Fatalf("callback called %d times, want 2", calls)
	}
}

func TestDecodeOrdersEmptyData(t *testing.T) {
	var state DecoderState
	called := 0
	DecodeOrders(&state, nil, 0, func(s *DecoderState, ord *Order) {
		called++
	})
	if called != 0 {
		t.Errorf("callback called %d times on empty data", called)
	}
}

func TestBlitGlyph1bpp(t *testing.T) {
	// 4x2 glyph: row0=0xF0 (1111 0000), row1=0x50 (0101 0000)
	src := []byte{0xF0, 0x50}

	dstW, dstH := 8, 4
	dst := make([]byte, dstW*dstH*4)

	blitGlyph1bpp(dst, dstW, dstH, 0, 0, 0, 0,
		src, 4, 2, 0xFF, 0xFF, 0xFF)

	// Check row 0, col 0 (top-left logical → bottom-up buffer row dstH-1-0 = 3)
	// Pixel (0,0) → buffer row 3, col 0
	off := (dstH - 1) * dstW * 4
	if dst[off] != 0xFF || dst[off+1] != 0xFF || dst[off+2] != 0xFF {
		t.Errorf("pixel (0,0) = [%d,%d,%d], want [255,255,255]",
			dst[off], dst[off+1], dst[off+2])
	}

	// Check row 1, col 0 (logical row 1 → buffer row 2)
	off = (dstH - 1 - 1) * dstW * 4
	if dst[off] != 0 {
		t.Errorf("pixel (0,1) should be 0 (0101 pattern, bit 7=0)")
	}
	// row 1, col 1 should be set (bit 6=1)
	off += 4
	if dst[off] != 0xFF {
		t.Errorf("pixel (1,1) = %d, want 255 (0101 pattern, bit 6=1)", dst[off])
	}
}

func TestRenderGlyphIndexBasic(t *testing.T) {
	var cache GlyphCache

	// Cache a simple 4x1 glyph: all pixels set → 0xF0
	g := CachedGlyph{X: 0, Y: 0, CX: 4, CY: 1, Data: []byte{0xF0}}
	cache.Set(0, 5, &g)

	s := &GlyphIndexState{
		CacheID:   0,
		UlCharInc: 4, // fixed-pitch
		BackColor: 0xFFFFFF, // wire field 4: actually the glyph/text colour
		ForeColor: 0x000000, // wire field 5: actually the background colour
		BkLeft:    0, BkTop: 0, BkRight: 8, BkBottom: 2,
		X: 0, Y: 0,
		VarLen: 1,
	}
	s.VarBytes[0] = 5 // glyph index 5

	pixels, x, y, w, h, _ := RenderGlyphIndex(nil, s, &cache, 24, nil)
	if x != 0 || y != 0 || w != 8 || h != 2 {
		t.Errorf("rect = (%d,%d,%d,%d), want (0,0,8,2)", x, y, w, h)
	}
	if len(pixels) != w*h*4 {
		t.Fatalf("pixels len = %d, want %d", len(pixels), w*h*4)
	}

	// Verify some foreground pixel is set (bottom-up row 1 = logical row 0)
	// Logical (0,0) → buffer row (h-1-0)=1, col 0
	off := (h - 1) * w * 4
	if pixels[off] != 0xFF || pixels[off+1] != 0xFF || pixels[off+2] != 0xFF {
		t.Errorf("glyph pixel (0,0) = [%d,%d,%d], want white",
			pixels[off], pixels[off+1], pixels[off+2])
	}
}

func BenchmarkDecodeOrders(b *testing.B) {
	// Build 10 GlyphIndex orders
	var data []byte
	for i := 0; i < 10; i++ {
		data = append(data, buildPrimaryGlyphIndex(0, []byte{byte(i)})...)
	}

	var state DecoderState
	noop := func(s *DecoderState, ord *Order) {}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		state.LastOrderType = 0
		DecodeOrders(&state, data, 10, noop)
	}
}

func BenchmarkRenderGlyphIndex(b *testing.B) {
	var cache GlyphCache
	g := CachedGlyph{X: 0, Y: 0, CX: 8, CY: 16, Data: make([]byte, 16)}
	for i := range g.Data {
		g.Data[i] = 0xFF
	}
	cache.Set(0, 0, &g)

	s := &GlyphIndexState{
		CacheID:   0,
		UlCharInc: 8,
		BackColor: 0xFFFFFF,
		ForeColor: 0,
		BkLeft:    0, BkTop: 0, BkRight: 80, BkBottom: 16,
		X: 0, Y: 0,
		VarLen: 10,
	}
	for i := range s.VarBytes[:10] {
		s.VarBytes[i] = 0
	}

	var buf []byte

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _, _, buf = RenderGlyphIndex(buf, s, &cache, 24, nil)
	}
}
