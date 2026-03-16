package orders

import (
	"encoding/binary"
	"testing"
)

func TestBresenhamHorizontal(t *testing.T) {
	w, h := 10, 5
	fb := make([]byte, w*h*4)
	s := &LineToState{
		StartX: 2, StartY: 2, EndX: 7, EndY: 2,
		PenColor: 0x0000FF, // red (wire: R in low byte)
		Rop2:     0x0D,     // COPYPEN
	}
	x, y, bw, bh := DrawLineTo(fb, w, h, s, 24, nil)
	if x != 2 || y != 2 || bw != 6 || bh != 1 {
		t.Errorf("bbox = (%d,%d,%d,%d), want (2,2,6,1)", x, y, bw, bh)
	}
	// Check pixel at (2,2) — bottom-up row = h-1-y = 2
	stride := w * 4
	for px := 2; px <= 7; px++ {
		off := (h-1-2)*stride + px*4
		if fb[off] != 0xFF || fb[off+1] != 0x00 || fb[off+2] != 0x00 {
			t.Errorf("pixel(%d,2) = RGBA(%d,%d,%d), want (255,0,0)",
				px, fb[off], fb[off+1], fb[off+2])
		}
	}
	// Check pixel outside the line is untouched
	off := (h-1-2)*stride + 1*4
	if fb[off] != 0 || fb[off+1] != 0 || fb[off+2] != 0 {
		t.Errorf("pixel(1,2) should be black, got RGBA(%d,%d,%d)", fb[off], fb[off+1], fb[off+2])
	}
}

func TestBresenhamVertical(t *testing.T) {
	w, h := 5, 10
	fb := make([]byte, w*h*4)
	s := &LineToState{
		StartX: 2, StartY: 1, EndX: 2, EndY: 8,
		PenColor: 0x00FF00, // green (wire: G in mid byte)
		Rop2:     0x0D,
	}
	_, _, bw, bh := DrawLineTo(fb, w, h, s, 24, nil)
	if bw != 1 || bh != 8 {
		t.Errorf("bbox size = (%d,%d), want (1,8)", bw, bh)
	}
	stride := w * 4
	for py := 1; py <= 8; py++ {
		off := (h-1-py)*stride + 2*4
		if fb[off+1] != 0xFF {
			t.Errorf("pixel(2,%d) G=%d, want 255", py, fb[off+1])
		}
	}
}

func TestBresenhamDiagonal(t *testing.T) {
	w, h := 10, 10
	fb := make([]byte, w*h*4)
	s := &LineToState{
		StartX: 0, StartY: 0, EndX: 4, EndY: 4,
		PenColor: 0xFFFFFF,
		Rop2:     0x0D,
	}
	DrawLineTo(fb, w, h, s, 24, nil)
	stride := w * 4
	// Diagonal: each (i,i) from 0..4 should be set
	for i := 0; i <= 4; i++ {
		off := (h-1-i)*stride + i*4
		if fb[off] != 0xFF || fb[off+1] != 0xFF || fb[off+2] != 0xFF {
			t.Errorf("pixel(%d,%d) not white", i, i)
		}
	}
}

func TestBresenhamROP2XOR(t *testing.T) {
	w, h := 5, 1
	fb := make([]byte, w*h*4)
	// Pre-fill pixel (2,0) with white
	fb[2*4] = 0xFF
	fb[2*4+1] = 0xFF
	fb[2*4+2] = 0xFF
	fb[2*4+3] = 0xFF

	s := &LineToState{
		StartX: 2, StartY: 0, EndX: 2, EndY: 0,
		PenColor: 0x0000FF, // red (wire: R in low byte)
		Rop2:     0x07,     // R2_XORPEN
	}
	DrawLineTo(fb, w, h, s, 24, nil)
	// White XOR Red(R=FF,G=0,B=0) → R=FF^FF=00, G=FF^00=FF, B=FF^00=FF
	off := 2 * 4
	if fb[off] != 0x00 || fb[off+1] != 0xFF || fb[off+2] != 0xFF {
		t.Errorf("XOR result RGBA(%d,%d,%d), want (0,255,255)", fb[off], fb[off+1], fb[off+2])
	}
}

func TestApplyROP3(t *testing.T) {
	tests := []struct {
		name          string
		rop           byte
		pat, src, dst byte
		want          byte
	}{
		{"SRCCOPY", 0xCC, 0x00, 0xAB, 0x55, 0xAB},
		{"BLACKNESS", 0x00, 0xFF, 0xFF, 0xFF, 0x00},
		{"WHITENESS", 0xFF, 0x00, 0x00, 0x00, 0xFF},
		{"SRCPAINT", 0xEE, 0x00, 0xA0, 0x55, 0xF5},    // src | dst
		{"SRCAND", 0x88, 0x00, 0xA0, 0x55, 0x00},       // src & dst
		{"SRCINVERT", 0x66, 0x00, 0xA0, 0x55, 0xF5},    // src ^ dst
		{"PATCOPY", 0xF0, 0xAB, 0x00, 0x00, 0xAB},      // pat
		{"NOTSRCCOPY", 0x33, 0x00, 0xA0, 0x55, 0x5F},   // ~src
		{"MERGEPAINT", 0xBB, 0x00, 0xA0, 0x55, 0x5F},   // ~src | dst
	}
	for _, tt := range tests {
		got := ApplyROP3(tt.rop, tt.pat, tt.src, tt.dst)
		if got != tt.want {
			t.Errorf("ROP3(%s): ApplyROP3(0x%02X, 0x%02X, 0x%02X, 0x%02X) = 0x%02X, want 0x%02X",
				tt.name, tt.rop, tt.pat, tt.src, tt.dst, got, tt.want)
		}
	}
}

func TestDecodeDeltaPoints(t *testing.T) {
	// Encode 2 delta entries: (10, -5), (0, 20)
	// Zero flags: entry0 x=present, y=present; entry1 x=zero, y=present
	// Flags byte: 0b00100000 = 0x20 (MSB first: 0,0,1,0, padding)
	//   bit7=0 (entry0.x present), bit6=0 (entry0.y present)
	//   bit5=1 (entry1.x zero), bit4=0 (entry1.y present)

	var buf [20]byte
	off := 0

	// cbData placeholder
	cbDataOff := off
	off++

	// flags: 1 byte for 2 entries
	buf[off] = 0x20 // entry0.x=present, entry0.y=present, entry1.x=zero, entry1.y=present
	off++

	// entry0.x = 10 (positive, fits in 6 bits, 1 byte)
	buf[off] = 10
	off++

	// entry0.y = -5 (negative, 1 byte: sign bit 6 set, bits 0-5 = 0x3B)
	// -5 as 6-bit signed = 0x3B, with sign flag = 0x7B
	buf[off] = 0x7B
	off++

	// entry1.y = 20 (positive, fits in 6 bits, 1 byte)
	buf[off] = 20
	off++

	// Write cbData
	buf[cbDataOff] = byte(off - 1) // total data after cbData byte

	dst := make([]int, 4)
	if !decodeDeltaPoints(buf[:off], 2, dst) {
		t.Fatal("decodeDeltaPoints returned false")
	}

	want := []int{10, -5, 0, 20}
	for i, w := range want {
		if dst[i] != w {
			t.Errorf("deltas[%d] = %d, want %d", i, dst[i], w)
		}
	}
}

func TestDecodeDeltaPointsTwoByteValue(t *testing.T) {
	// Single entry with a 2-byte X delta = 300
	// 300 = 0x012C. Encoded: first byte = 0x80 | (0x01) = 0x81, second byte = 0x2C
	var buf [10]byte
	off := 0

	cbDataOff := off
	off++

	// flags: 1 byte, entry0.x=present, entry0.y=zero
	buf[off] = 0x40 // bit7=0 (x present), bit6=1 (y zero)
	off++

	// entry0.x = 300: two-byte encoding
	buf[off] = 0x80 | 0x01 // bit7=twoBytes, bits0-5=high(0x01), bit6=0(positive)
	off++
	buf[off] = 0x2C // low byte
	off++

	buf[cbDataOff] = byte(off - 1)

	dst := make([]int, 2)
	if !decodeDeltaPoints(buf[:off], 1, dst) {
		t.Fatal("decode failed")
	}
	if dst[0] != 300 {
		t.Errorf("x delta = %d, want 300", dst[0])
	}
	if dst[1] != 0 {
		t.Errorf("y delta = %d, want 0", dst[1])
	}
}

func TestDrawPolyline(t *testing.T) {
	w, h := 20, 20
	fb := make([]byte, w*h*4)

	// Build coded delta list: 2 entries, both (5,0) → horizontal line segments
	var varBuf [20]byte
	off := 0
	varBuf[off] = 0 // cbData placeholder
	off++
	varBuf[off] = 0x00 // flags: all present
	off++
	varBuf[off] = 5 // entry0.x = 5
	off++
	varBuf[off] = 0 // entry0.y = 0 (but flag says present, so encode 0)
	off++
	varBuf[off] = 5 // entry1.x = 5
	off++
	varBuf[off] = 0 // entry1.y = 0
	off++
	varBuf[0] = byte(off - 1)

	s := &PolylineState{
		StartX:          5,
		StartY:          10,
		Rop2:            0x0D,
		PenColor:        0x0000FF, // red (wire: R in low byte)
		NumDeltaEntries: 2,
		VarLen:          uint8(off),
	}
	copy(s.VarBytes[:], varBuf[:off])

	x, y, bw, bh, _ := DrawPolyline(fb, w, h, s, nil, 24, nil)
	if x != 5 || y != 10 {
		t.Errorf("bbox origin = (%d,%d), want (5,10)", x, y)
	}
	if bw != 11 || bh != 1 {
		t.Errorf("bbox size = (%d,%d), want (11,1)", bw, bh)
	}

	// Check that pixels from x=5 to x=15 at y=10 are set
	stride := w * 4
	for px := 5; px <= 15; px++ {
		pOff := (h-1-10)*stride + px*4
		if fb[pOff] != 0xFF {
			t.Errorf("pixel(%d,10) R=%d, want 255", px, fb[pOff])
		}
	}
}

func TestDrawEllipseSCFilled(t *testing.T) {
	w, h := 30, 30
	fb := make([]byte, w*h*4)
	s := &EllipseSCState{
		Left: 5, Top: 5, Right: 25, Bottom: 25,
		Rop2:     0x0D,
		FillMode: 1, // filled
		Color:    0x00FF00, // green (wire: G in mid byte)
	}
	x, y, bw, bh := DrawEllipseSC(fb, w, h, s, 24, nil)
	if x != 5 || y != 5 || bw != 20 || bh != 20 {
		t.Errorf("bbox = (%d,%d,%d,%d), want (5,5,20,20)", x, y, bw, bh)
	}

	// Center (15,15) must be filled
	stride := w * 4
	off := (h-1-15)*stride + 15*4
	if fb[off+1] != 0xFF {
		t.Errorf("center pixel G=%d, want 255", fb[off+1])
	}

	// Corner outside the ellipse (5,5) should be empty
	off = (h-1-5)*stride + 5*4
	if fb[off+1] != 0 {
		t.Errorf("corner pixel G=%d, want 0", fb[off+1])
	}
}

func TestDrawEllipseSCOutline(t *testing.T) {
	w, h := 30, 30
	fb := make([]byte, w*h*4)
	s := &EllipseSCState{
		Left: 5, Top: 5, Right: 25, Bottom: 25,
		Rop2:     0x0D,
		FillMode: 0, // outline
		Color:    0x0000FF, // red (wire: R in low byte)
	}
	DrawEllipseSC(fb, w, h, s, 24, nil)

	// Center should NOT be filled (outline only)
	stride := w * 4
	off := (h-1-15)*stride + 15*4
	if fb[off] != 0 {
		t.Errorf("center pixel R=%d, want 0 (outline only)", fb[off])
	}

	// Top of ellipse (x=15, y=5) should be drawn
	off = (h-1-5)*stride + 15*4
	if fb[off] != 0xFF {
		t.Errorf("top pixel R=%d, want 255", fb[off])
	}
}

func TestDecodeLineTo(t *testing.T) {
	var s LineToState
	// Build field data: all 10 fields present (flags = 0x03FF)
	var data [32]byte
	off := 0
	binary.LittleEndian.PutUint16(data[off:], 2) // backMode=OPAQUE
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(10))) // startX
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(20))) // startY
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(100))) // endX
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(50))) // endY
	off += 2
	data[off] = 0x00 // backColor B
	data[off+1] = 0xFF // backColor G
	data[off+2] = 0x00 // backColor R
	off += 3
	data[off] = 0x0D // rop2 = COPYPEN
	off++
	data[off] = 0x00 // penStyle
	off++
	data[off] = 0x01 // penWidth
	off++
	data[off] = 0xFF // penColor B
	data[off+1] = 0x00 // penColor G
	data[off+2] = 0x00 // penColor R
	off += 3

	newOff := decodeLineTo(data[:], 0, 0x03FF, false, &s)
	if newOff != off {
		t.Errorf("offset = %d, want %d", newOff, off)
	}
	if s.BackMode != 2 {
		t.Errorf("BackMode = %d, want 2", s.BackMode)
	}
	if s.StartX != 10 || s.StartY != 20 {
		t.Errorf("Start = (%d,%d), want (10,20)", s.StartX, s.StartY)
	}
	if s.EndX != 100 || s.EndY != 50 {
		t.Errorf("End = (%d,%d), want (100,50)", s.EndX, s.EndY)
	}
	if s.Rop2 != 0x0D {
		t.Errorf("Rop2 = 0x%02X, want 0x0D", s.Rop2)
	}
	if s.PenColor != 0x0000FF {
		t.Errorf("PenColor = 0x%06X, want 0x0000FF", s.PenColor)
	}
}

func TestDecodeEllipseSC(t *testing.T) {
	var s EllipseSCState
	// All 7 fields present (flags = 0x7F)
	var data [16]byte
	off := 0
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(5))) // left
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(10))) // top
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(50))) // right
	off += 2
	binary.LittleEndian.PutUint16(data[off:], uint16(int16(40))) // bottom
	off += 2
	data[off] = 0x0D // rop2
	off++
	data[off] = 0x01 // fillMode
	off++
	data[off] = 0x00 // color B
	data[off+1] = 0xFF // color G
	data[off+2] = 0x00 // color R
	off += 3

	newOff := decodeEllipseSC(data[:], 0, 0x7F, false, &s)
	if newOff != off {
		t.Errorf("offset = %d, want %d", newOff, off)
	}
	if s.Left != 5 || s.Top != 10 || s.Right != 50 || s.Bottom != 40 {
		t.Errorf("rect = (%d,%d,%d,%d), want (5,10,50,40)", s.Left, s.Top, s.Right, s.Bottom)
	}
	if s.Rop2 != 0x0D {
		t.Errorf("Rop2 = 0x%02X, want 0x0D", s.Rop2)
	}
	if s.FillMode != 1 {
		t.Errorf("FillMode = %d, want 1", s.FillMode)
	}
}

func TestRenderFastGlyphInline(t *testing.T) {
	var cache GlyphCache
	s := &FastIndexState{
		CacheID:   0,
		FDrawing:  0x0100, // flAccel=1 (SO_CHAR_INC_EQUAL_BM_BASE), ulCharInc=0
		BackColor: 0xFFFFFF, // wire field 4: actually the glyph colour
		ForeColor: 0x000000, // wire field 5: actually the background colour
		BkLeft:    0, BkTop: 0, BkRight: 8, BkBottom: 8,
		OpLeft:    0, OpTop: 0, OpRight: 0, OpBottom: 0,
		X: 0, Y: 8,
	}

	// Build varData: glyph index 42, then inline glyph 4x4 pixels
	var varData [20]byte
	varData[0] = 42 // glyph index
	binary.LittleEndian.PutUint16(varData[1:], uint16(0))  // x offset
	binary.LittleEndian.PutUint16(varData[3:], uint16(0))  // y offset
	binary.LittleEndian.PutUint16(varData[5:], 4)          // cx
	binary.LittleEndian.PutUint16(varData[7:], 4)          // cy
	// 4px wide = 1 byte/row, 4 rows = 4 bytes of bitmap
	varData[9] = 0xF0  // row 0: 4 pixels set
	varData[10] = 0xF0 // row 1
	varData[11] = 0xF0 // row 2
	varData[12] = 0xF0 // row 3
	s.VarLen = 13
	copy(s.VarBytes[:], varData[:13])

	pixels, x, y, w, h, _ := RenderFastGlyph(nil, s, &cache, 24, nil)
	if pixels == nil {
		t.Fatal("RenderFastGlyph returned nil pixels")
	}
	if x != 0 || y != 0 || w != 8 || h != 8 {
		t.Errorf("rect = (%d,%d,%d,%d), want (0,0,8,8)", x, y, w, h)
	}

	// Verify glyph was cached
	g := cache.Get(0, 42)
	if g == nil {
		t.Fatal("glyph not cached")
	}
	if g.CX != 4 || g.CY != 4 {
		t.Errorf("cached glyph size = (%d,%d), want (4,4)", g.CX, g.CY)
	}
}
