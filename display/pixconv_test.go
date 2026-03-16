package display

import (
	"bytes"
	"testing"
)

func TestConvertToRGBA_15bpp(t *testing.T) {
	// 2x2 image, 15bpp (5-5-5): 0RRRRRGGGGGBBBBB
	// Bottom-up layout: bottom row first in src, then top row.
	//
	// Src row 0 (bottom of image): R=31,G=0,B=0 | R=0,G=31,B=0
	// Src row 1 (top of image):    R=0,G=0,B=31 | R=31,G=31,B=31
	src := []byte{
		0x00, 0x7C, // bottom row: pixel(0) = red   0x7C00
		0xE0, 0x03, // bottom row: pixel(1) = green 0x03E0
		0x1F, 0x00, // top row:    pixel(0) = blue  0x001F
		0xFF, 0x7F, // top row:    pixel(1) = white 0x7FFF
	}
	dst := make([]byte, 2*2*4)
	ConvertToRGBA(dst, src, 2, 2, 15)

	// After bottom-up→top-down flip:
	//   dst row 0 = src row 1 (top of image): blue, white
	//   dst row 1 = src row 0 (bottom of image): red, green
	tests := []struct {
		name    string
		off     int
		r, g, b byte
	}{
		{"(0,0) blue", 0, 0x00, 0x00, 0xFF},
		{"(1,0) white", 4, 0xFF, 0xFF, 0xFF},
		{"(0,1) red", 8, 0xFF, 0x00, 0x00},
		{"(1,1) green", 12, 0x00, 0xFF, 0x00},
	}
	for _, tt := range tests {
		if dst[tt.off] != tt.r || dst[tt.off+1] != tt.g || dst[tt.off+2] != tt.b || dst[tt.off+3] != 0xFF {
			t.Errorf("%s: got RGBA(%d,%d,%d,%d), want (%d,%d,%d,255)",
				tt.name, dst[tt.off], dst[tt.off+1], dst[tt.off+2], dst[tt.off+3],
				tt.r, tt.g, tt.b)
		}
	}
}

func TestConvertToRGBA_16bpp(t *testing.T) {
	// 2x1 image, 16bpp (5-6-5): RRRRRGGGGGGBBBBB
	// Only 1 row so no flip effect.
	//
	// Pixel 0: R=31,G=0,B=0  → 11111 000000 00000 = 0xF800
	// Pixel 1: R=0,G=63,B=0  → 00000 111111 00000 = 0x07E0
	src := []byte{
		0x00, 0xF8, // red
		0xE0, 0x07, // green
	}
	dst := make([]byte, 2*1*4)
	ConvertToRGBA(dst, src, 2, 1, 16)

	if dst[0] != 0xFF || dst[1] != 0x00 || dst[2] != 0x00 || dst[3] != 0xFF {
		t.Errorf("pixel 0: got RGBA(%d,%d,%d,%d), want (255,0,0,255)",
			dst[0], dst[1], dst[2], dst[3])
	}
	if dst[4] != 0x00 || dst[5] != 0xFF || dst[6] != 0x00 || dst[7] != 0xFF {
		t.Errorf("pixel 1: got RGBA(%d,%d,%d,%d), want (0,255,0,255)",
			dst[4], dst[5], dst[6], dst[7])
	}
}

func TestConvertToRGBA_16bpp_Blue(t *testing.T) {
	// Pixel: R=0,G=0,B=31  → 00000 000000 11111 = 0x001F
	src := []byte{0x1F, 0x00}
	dst := make([]byte, 4)
	ConvertToRGBA(dst, src, 1, 1, 16)

	if dst[0] != 0x00 || dst[1] != 0x00 || dst[2] != 0xFF || dst[3] != 0xFF {
		t.Errorf("blue pixel: got RGBA(%d,%d,%d,%d), want (0,0,255,255)",
			dst[0], dst[1], dst[2], dst[3])
	}
}

func TestConvertToRGBA_24bpp(t *testing.T) {
	// 2x2 image, 24bpp (B,G,R byte order), bottom-up
	src := []byte{
		// Src row 0 (bottom of image): red, green
		0x00, 0x00, 0xFF, // B=0, G=0, R=255 → red
		0x00, 0xFF, 0x00, // B=0, G=255, R=0 → green
		// Src row 1 (top of image): blue, white
		0xFF, 0x00, 0x00, // B=255, G=0, R=0 → blue
		0xFF, 0xFF, 0xFF, // B=255, G=255, R=255 → white
	}
	dst := make([]byte, 2*2*4)
	ConvertToRGBA(dst, src, 2, 2, 24)

	// After flip: dst row 0 = top of image (blue, white)
	//             dst row 1 = bottom of image (red, green)
	want := []byte{
		0x00, 0x00, 0xFF, 0xFF, // blue
		0xFF, 0xFF, 0xFF, 0xFF, // white
		0xFF, 0x00, 0x00, 0xFF, // red
		0x00, 0xFF, 0x00, 0xFF, // green
	}
	if !bytes.Equal(dst, want) {
		t.Errorf("24bpp:\n got: %v\nwant: %v", dst, want)
	}
}

func TestConvertToRGBA_32bpp(t *testing.T) {
	// 1x2 image, 32bpp RGBA, bottom-up
	src := []byte{
		// Src row 0 (bottom of image): green (R=0,G=255,B=0,A=255)
		0x00, 0xFF, 0x00, 0xFF,
		// Src row 1 (top of image): red (R=255,G=0,B=0,A=255)
		0xFF, 0x00, 0x00, 0xFF,
	}
	dst := make([]byte, 1*2*4)
	ConvertToRGBA(dst, src, 1, 2, 32)

	// After flip: dst row 0 = top of image (red), dst row 1 = bottom (green)
	if dst[0] != 0xFF || dst[1] != 0x00 || dst[2] != 0x00 || dst[3] != 0xFF {
		t.Errorf("row 0: got RGBA(%d,%d,%d,%d), want (255,0,0,255)",
			dst[0], dst[1], dst[2], dst[3])
	}
	if dst[4] != 0x00 || dst[5] != 0xFF || dst[6] != 0x00 || dst[7] != 0xFF {
		t.Errorf("row 1: got RGBA(%d,%d,%d,%d), want (0,255,0,255)",
			dst[4], dst[5], dst[6], dst[7])
	}
}

func TestConvertToRGBA_BottomUpFlip(t *testing.T) {
	// 1x3 image, 24bpp — verify 3-row flip
	// Src layout (bottom-up): row0=bottom, row1=middle, row2=top
	src := []byte{
		0x01, 0x02, 0x03, // src row 0 (bottom of image)
		0x04, 0x05, 0x06, // src row 1 (middle)
		0x07, 0x08, 0x09, // src row 2 (top of image)
	}
	dst := make([]byte, 1*3*4)
	ConvertToRGBA(dst, src, 1, 3, 24)

	// After flip: dst row 0 = src row 2 (top of image)
	//             dst row 1 = src row 1 (middle)
	//             dst row 2 = src row 0 (bottom of image)
	// Row 0: B=0x07,G=0x08,R=0x09 → R=0x09,G=0x08,B=0x07
	if dst[0] != 0x09 || dst[1] != 0x08 || dst[2] != 0x07 {
		t.Errorf("row 0: got RGB(%d,%d,%d), want (9,8,7)", dst[0], dst[1], dst[2])
	}
	// Row 1: B=0x04,G=0x05,R=0x06 → R=0x06,G=0x05,B=0x04
	if dst[4] != 0x06 || dst[5] != 0x05 || dst[6] != 0x04 {
		t.Errorf("row 1: got RGB(%d,%d,%d), want (6,5,4)", dst[4], dst[5], dst[6])
	}
	// Row 2: B=0x01,G=0x02,R=0x03 → R=0x03,G=0x02,B=0x01
	if dst[8] != 0x03 || dst[9] != 0x02 || dst[10] != 0x01 {
		t.Errorf("row 2: got RGB(%d,%d,%d), want (3,2,1)", dst[8], dst[9], dst[10])
	}
}

func BenchmarkConvertToRGBA_32bpp_64x64(b *testing.B) {
	w, h := 64, 64
	src := make([]byte, w*h*4)
	dst := make([]byte, w*h*4)
	for i := range src {
		src[i] = byte(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		ConvertToRGBA(dst, src, w, h, 32)
	}
}

func BenchmarkConvertToRGBA_32bpp_1920x1080(b *testing.B) {
	w, h := 1920, 1080
	src := make([]byte, w*h*4)
	dst := make([]byte, w*h*4)
	for i := range src {
		src[i] = byte(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		ConvertToRGBA(dst, src, w, h, 32)
	}
}

func BenchmarkConvertToRGBA_16bpp_64x64(b *testing.B) {
	w, h := 64, 64
	src := make([]byte, w*h*2)
	dst := make([]byte, w*h*4)
	for i := range src {
		src[i] = byte(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		ConvertToRGBA(dst, src, w, h, 16)
	}
}

func BenchmarkConvertToRGBA_15bpp_64x64(b *testing.B) {
	w, h := 64, 64
	src := make([]byte, w*h*2)
	dst := make([]byte, w*h*4)
	for i := range src {
		src[i] = byte(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		ConvertToRGBA(dst, src, w, h, 15)
	}
}
