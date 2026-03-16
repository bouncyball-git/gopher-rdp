package rle

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Helper: encode a regular BG_RUN (code 0) with given run length.
func bgRun(n int) []byte {
	if n < 32 {
		return []byte{byte(0x00 | n)} // code=0, runLength in low 5 bits
	}
	return []byte{0x00, byte(n - 32)} // code=0, runLength=0 → next + 32
}

// Helper: encode a regular FG_RUN (code 1) with given run length.
func fgRun(n int) []byte {
	if n < 32 {
		return []byte{byte(0x20 | n)} // code=1, runLength in low 5 bits
	}
	return []byte{0x20, byte(n - 32)} // code=1, runLength=0 → next + 32
}

// Helper: encode a regular COLOR_RUN (code 3) with given run length and pixel.
func colorRun8(n int, pel byte) []byte {
	var b []byte
	if n < 32 {
		b = []byte{byte(0x60 | n)}
	} else {
		b = []byte{0x60, byte(n - 32)}
	}
	return append(b, pel)
}

// Helper: encode a regular COLOR_IMAGE (code 4) with given pixel data.
func colorImage8(data []byte) []byte {
	n := len(data)
	var b []byte
	if n < 32 {
		b = []byte{byte(0x80 | n)}
	} else {
		b = []byte{0x80, byte(n - 32)}
	}
	return append(b, data...)
}

// Helper: encode a regular FGBG_IMAGE (code 2) with given pixel count and bitmask bytes.
// For embedded encoding (n/8 fits in 5 bits, i.e. n <= 248 and n%8==0),
// the low 5 bits hold n/8 (decoder multiplies by 8).
// For extended encoding (n/8 == 0 or > 31), uses next byte = n-1 (decoder adds 1).
func fgBgImage(n int, mask []byte) []byte {
	var b []byte
	div := n / 8
	if div >= 1 && div < 32 && n%8 == 0 {
		b = []byte{byte(0x40 | div)}
	} else {
		b = []byte{0x40, byte(n - 1)} // code=2, runLength=0 → next + 1
	}
	return append(b, mask...)
}

// Helper: encode a regular SET_FG_FG_RUN (lite, code 0xC) with fg pixel (8bpp) and run length.
func setFgFgRun8(n int, fg byte) []byte {
	var b []byte
	if n < 16 {
		b = []byte{byte(0xC0 | n)}
	} else {
		b = []byte{0xC0, byte(n - 16)}
	}
	return append(b, fg)
}

// Helper: encode a DITHERED_RUN (lite, code 0xE) with two 8bpp pixels and pair count.
func ditheredRun8(n int, pelA, pelB byte) []byte {
	var b []byte
	if n < 16 {
		b = []byte{byte(0xE0 | n)}
	} else {
		b = []byte{0xE0, byte(n - 16)}
	}
	return append(b, pelA, pelB)
}

// Helper: encode a mega BG_RUN (0xF0) with given run length.
func megaBgRun(n int) []byte {
	b := []byte{0xF0, 0, 0}
	binary.LittleEndian.PutUint16(b[1:], uint16(n))
	return b
}

// Helper: encode a mega COLOR_IMAGE (0xF4) with given pixel data.
func megaColorImage(data []byte) []byte {
	b := []byte{0xF4, 0, 0}
	binary.LittleEndian.PutUint16(b[1:], uint16(len(data)))
	return append(b, data...)
}

func TestDecompressColorImage(t *testing.T) {
	// 4x2 @ 8bpp, entire bitmap as COLOR_IMAGE
	pixels := []byte{
		0x01, 0x02, 0x03, 0x04, // row 0
		0x05, 0x06, 0x07, 0x08, // row 1
	}
	src := colorImage8(pixels)

	got, err := Decompress(4, 2, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, pixels) {
		t.Errorf("got %X, want %X", got, pixels)
	}
}

func TestDecompressBgRunFirstLine(t *testing.T) {
	// 4x1 @ 8bpp: BG_RUN(4) → all black
	src := bgRun(4)
	got, err := Decompress(4, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressBgRunSecondLine(t *testing.T) {
	// 4x2 @ 8bpp: Fill row 0 with COLOR_IMAGE, then BG_RUN(4) copies from above.
	row0 := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	src := append(colorImage8(row0), bgRun(4)...)

	got, err := Decompress(4, 2, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := append(row0, row0...) // row 1 = copy of row 0
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressFgRunFirstLine(t *testing.T) {
	// 4x1 @ 8bpp: FG_RUN(4) → all white (fgPel=0xFF for 8bpp)
	src := fgRun(4)
	got, err := Decompress(4, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressFgRunSecondLine(t *testing.T) {
	// 4x2 @ 8bpp: Row 0 = [0x10, 0x20, 0x30, 0x40]
	// FG_RUN on row 1: XOR fgPel(0xFF) with above
	row0 := []byte{0x10, 0x20, 0x30, 0x40}
	src := append(colorImage8(row0), fgRun(4)...)

	got, err := Decompress(4, 2, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := make([]byte, 8)
	copy(want[:4], row0)
	for i := 0; i < 4; i++ {
		want[4+i] = 0xFF ^ row0[i]
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressColorRun(t *testing.T) {
	// 4x1 @ 8bpp: COLOR_RUN(4, 0x42)
	src := colorRun8(4, 0x42)
	got, err := Decompress(4, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x42, 0x42, 0x42, 0x42}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressFgBgImage(t *testing.T) {
	// 8x1 @ 8bpp: FGBG_IMAGE(8, mask=0xA5)
	// Mask 0xA5 = 10100101 → LSB first: bits 0,2,5,7 are set
	// bit set → fgPel (0xFF), bit clear → black (0x00)
	src := fgBgImage(8, []byte{0xA5})
	got, err := Decompress(8, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// LSB first: bit0=1, bit1=0, bit2=1, bit3=0, bit4=0, bit5=1, bit6=0, bit7=1
	want := []byte{0xFF, 0x00, 0xFF, 0x00, 0x00, 0xFF, 0x00, 0xFF}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressDitheredRun(t *testing.T) {
	// 6x1 @ 8bpp: DITHERED_RUN(3, 0xAA, 0x55) → 3 pairs = 6 pixels
	src := ditheredRun8(3, 0xAA, 0x55)
	got, err := Decompress(6, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressSpecialOrders(t *testing.T) {
	t.Run("White", func(t *testing.T) {
		// 1x1 @ 8bpp
		src := []byte{0xFD}
		got, err := Decompress(1, 1, 8, src)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0] != 0xFF {
			t.Errorf("got %X, want FF", got[0])
		}
	})

	t.Run("Black", func(t *testing.T) {
		// 1x1 @ 8bpp
		src := []byte{0xFE}
		got, err := Decompress(1, 1, 8, src)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0] != 0x00 {
			t.Errorf("got %X, want 00", got[0])
		}
	})

	t.Run("SpecialFGBG1", func(t *testing.T) {
		// 8x1 @ 8bpp, mask=0x03 → bits 0,1 set
		src := []byte{0xF9}
		got, err := Decompress(8, 1, 8, src)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []byte{0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		if !bytes.Equal(got, want) {
			t.Errorf("got %X, want %X", got, want)
		}
	})

	t.Run("SpecialFGBG2", func(t *testing.T) {
		// 8x1 @ 8bpp, mask=0x05 → bits 0,2 set
		src := []byte{0xFA}
		got, err := Decompress(8, 1, 8, src)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []byte{0xFF, 0x00, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00}
		if !bytes.Equal(got, want) {
			t.Errorf("got %X, want %X", got, want)
		}
	})
}

func TestDecompressFInsertFgPel(t *testing.T) {
	// 8x1 @ 8bpp: BG_RUN(4) then BG_RUN(4)
	// First BG_RUN: 4 black pixels, sets fInsertFgPel=true
	// Second BG_RUN: fInsertFgPel → write 1 fg pixel (0xFF), then 3 bg pixels
	src := append(bgRun(4), bgRun(4)...)
	got, err := Decompress(8, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressMultiBpp(t *testing.T) {
	tests := []struct {
		name    string
		bpp     int
		bytesPP int
		whitePel []byte
	}{
		{"8bpp", 8, 1, []byte{0xFF}},
		{"16bpp", 16, 2, []byte{0xFF, 0xFF}},
		{"24bpp", 24, 3, []byte{0xFF, 0xFF, 0xFF}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1x1: FG_RUN(1) → white pixel
			src := fgRun(1)
			got, err := Decompress(1, 1, tt.bpp, src)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tt.whitePel) {
				t.Errorf("got %X, want %X", got, tt.whitePel)
			}
		})
	}
}

func TestDecompressErrors(t *testing.T) {
	t.Run("UnsupportedBpp", func(t *testing.T) {
		_, err := Decompress(1, 1, 7, []byte{0xFD})
		if err == nil {
			t.Fatal("expected error for bpp=7")
		}
	})

	t.Run("TruncatedSource", func(t *testing.T) {
		// COLOR_RUN(1) but no pixel byte follows
		src := []byte{0x61} // code 3, runLength 1
		_, err := Decompress(1, 1, 8, src)
		if err == nil {
			t.Fatal("expected error for truncated source")
		}
	})

	t.Run("TruncatedMegaRunLength", func(t *testing.T) {
		// Mega opcode 0xF0 but only 1 byte for run length instead of 2
		src := []byte{0xF0, 0x04}
		_, err := Decompress(4, 1, 8, src)
		if err == nil {
			t.Fatal("expected error for truncated mega run length")
		}
	})
}

func TestDecompressSetFgFgRun(t *testing.T) {
	// 4x1 @ 8bpp: SET_FG_FG_RUN with fg=0x42, run=4
	src := setFgFgRun8(4, 0x42)
	got, err := Decompress(4, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x42, 0x42, 0x42, 0x42}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressMegaOrders(t *testing.T) {
	// 4x1 @ 8bpp: Mega BG_RUN(4) → all black
	src := megaBgRun(4)
	got, err := Decompress(4, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressMegaColorImage(t *testing.T) {
	pixels := []byte{0x11, 0x22, 0x33, 0x44}
	src := megaColorImage(pixels)
	got, err := Decompress(4, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, pixels) {
		t.Errorf("got %X, want %X", got, pixels)
	}
}

func TestDecompress15Bpp(t *testing.T) {
	// 1x1 @ 15bpp: FG_RUN(1) → mix = 0xFFFF (same as 16bpp)
	src := fgRun(1)
	got, err := Decompress(1, 1, 15, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressMultiRow16bpp(t *testing.T) {
	// 2x2 @ 16bpp: Row 0 = COLOR_IMAGE([0x34,0x12, 0x78,0x56])
	//              Row 1 = FG_RUN(2) → XOR 0xFFFF with above
	row0 := []byte{0x34, 0x12, 0x78, 0x56}
	// COLOR_IMAGE for 2 pixels at 16bpp = 4 bytes
	ciSrc := colorImage16(row0)
	fgSrc := fgRun(2)
	src := append(ciSrc, fgSrc...)

	got, err := Decompress(2, 2, 16, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Row 1: 0xFFFF ^ 0x1234 = 0xEDCB, 0xFFFF ^ 0x5678 = 0xA987
	want := []byte{0x34, 0x12, 0x78, 0x56, 0xCB, 0xED, 0x87, 0xA9}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

// colorImage16 encodes a COLOR_IMAGE for 16bpp data.
func colorImage16(data []byte) []byte {
	n := len(data) / 2 // pixel count
	var b []byte
	if n < 32 {
		b = []byte{byte(0x80 | n)}
	} else {
		b = []byte{0x80, byte(n - 32)}
	}
	return append(b, data...)
}

func TestDecompressFgBgImageExtended(t *testing.T) {
	// Test FGBG_IMAGE with extended run length (next byte + 1).
	// 9 pixels can't be encoded embedded (9/8 = 1 remainder 1), use extended.
	// Encode: 0x40 (code=2, runLength=0), 0x08 (8 + 1 = 9), then 2 mask bytes.
	mask := []byte{0xFF, 0x01} // 8 fg + 1 fg = 9 fg pixels
	src := append([]byte{0x40, 0x08}, mask...)
	got, err := Decompress(9, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := bytes.Repeat([]byte{0xFF}, 9)
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressSetFgFgBgImageLite(t *testing.T) {
	// Test lite SET_FG_FGBG_IMAGE (code 0xD, high nibble of 0xD0-0xDF).
	// SET_FG_FGBG_IMAGE reads a fg pixel, then processes mask like FGBG_IMAGE.
	// Embedded: low 4 bits hold pixelCount/8, decoder multiplies by 8.
	// 0xD1 → code=0xD, embedded=1 → runLength = 1*8 = 8 pixels.
	fg := byte(0x42)
	mask := []byte{0xA5} // 10100101 → bits 0,2,5,7 set
	src := append([]byte{0xD1, fg}, mask...)
	got, err := Decompress(8, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// bit set → fg(0x42), bit clear → bg(0x00) on first line
	want := []byte{0x42, 0x00, 0x42, 0x00, 0x00, 0x42, 0x00, 0x42}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressSetFgFgBgImageLiteExtended(t *testing.T) {
	// Test lite SET_FG_FGBG_IMAGE with extended run length (next byte + 1).
	// 0xD0 → code=0xD, embedded=0 → read next byte + 1.
	// 0x07 → runLength = 7 + 1 = 8 pixels.
	fg := byte(0x42)
	mask := []byte{0xA5}
	src := append([]byte{0xD0, 0x07, fg}, mask...)
	got, err := Decompress(8, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x42, 0x00, 0x42, 0x00, 0x00, 0x42, 0x00, 0x42}
	if !bytes.Equal(got, want) {
		t.Errorf("got %X, want %X", got, want)
	}
}

func TestDecompressExtendedRunLength(t *testing.T) {
	// Test extended run length encoding: BG_RUN with runLength=0 in low bits
	// → next byte + 32
	// 40x1 @ 8bpp: BG_RUN(40) = [0x00, 0x08] (0 + 32 + 8 = 40)
	src := bgRun(40)
	got, err := Decompress(40, 1, 8, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := make([]byte, 40) // all zeros
	if !bytes.Equal(got, want) {
		t.Errorf("result doesn't match expected all-zeros for 40 pixels")
	}
}
