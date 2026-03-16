package rdp

import (
	"bytes"
	"testing"
)

func TestWriteRect(t *testing.T) {
	fb := NewFramebuffer(4, 4)

	// Write a 2x2 block at (1, 1) — bottom-up BGRX
	src := make([]byte, 2*2*4)
	for i := 0; i < len(src); i += 4 {
		src[i] = 0xAA   // B
		src[i+1] = 0xBB // G
		src[i+2] = 0xCC // R
		src[i+3] = 0xFF // X
	}
	fb.WriteRect(1, 1, 2, 2, src)

	// Read it back
	dst := make([]byte, 2*2*4)
	fb.ReadRect(dst, 1, 1, 2, 2)
	if !bytes.Equal(dst, src) {
		t.Fatalf("read-back mismatch:\n  got  %x\n  want %x", dst, src)
	}
}

func TestWriteRect_Clipping(t *testing.T) {
	fb := NewFramebuffer(4, 4)

	// Write a 3x3 block at (2, 2) — should clip to 2x2
	src := make([]byte, 3*3*4)
	for i := range src {
		src[i] = 0x42
	}
	fb.WriteRect(2, 2, 3, 3, src)

	// The clipped region (2,2)-(3,3) should have data
	dst := make([]byte, 2*2*4)
	fb.ReadRect(dst, 2, 2, 2, 2)
	// Should have non-zero data
	allZero := true
	for _, b := range dst {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("expected non-zero data in clipped region")
	}
}

func TestReadRect(t *testing.T) {
	fb := NewFramebuffer(4, 4)

	// Fill entire framebuffer
	for i := range fb.Pixels {
		fb.Pixels[i] = byte(i)
	}

	// Read back a sub-rect
	dst := make([]byte, 2*2*4)
	n := fb.ReadRect(dst, 0, 0, 2, 2)
	if n != 2*2*4 {
		t.Fatalf("ReadRect returned %d, want %d", n, 2*2*4)
	}
}

func TestCopyRect_NonOverlapping(t *testing.T) {
	fb := NewFramebuffer(8, 8)

	// Write a pattern at (0, 0) 2x2
	src := make([]byte, 2*2*4)
	for i := 0; i < len(src); i += 4 {
		src[i] = 0x11
		src[i+1] = 0x22
		src[i+2] = 0x33
		src[i+3] = 0xFF
	}
	fb.WriteRect(0, 0, 2, 2, src)

	// Copy from (0,0) to (4,4)
	scratch := fb.CopyRect(4, 4, 0, 0, 2, 2, nil)
	if scratch == nil {
		t.Fatal("expected non-nil scratch")
	}

	// Read dest
	dst := make([]byte, 2*2*4)
	fb.ReadRect(dst, 4, 4, 2, 2)
	if !bytes.Equal(dst, src) {
		t.Fatalf("copy mismatch:\n  got  %x\n  want %x", dst, src)
	}
}

func TestCopyRect_Overlapping(t *testing.T) {
	fb := NewFramebuffer(8, 8)

	// Write a pattern at (0, 0) 4x4
	src := make([]byte, 4*4*4)
	for i := 0; i < len(src); i += 4 {
		src[i] = byte(i / 4)
		src[i+1] = 0x22
		src[i+2] = 0x33
		src[i+3] = 0xFF
	}
	fb.WriteRect(0, 0, 4, 4, src)

	// Save original (0,0)-(3,3) before copy
	origDst := make([]byte, 4*4*4)
	fb.ReadRect(origDst, 0, 0, 4, 4)

	// Copy to overlapping position (2, 2) — overlaps the original
	fb.CopyRect(2, 2, 0, 0, 4, 4, nil)

	// Read back from (2,2) — should have original data from (0,0)
	dst := make([]byte, 4*4*4)
	fb.ReadRect(dst, 2, 2, 4, 4)
	if !bytes.Equal(dst, origDst) {
		t.Fatal("overlapping copy produced incorrect result")
	}
}

func TestResize(t *testing.T) {
	fb := NewFramebuffer(4, 4)
	fb.Resize(8, 6)

	if fb.Width != 8 || fb.Height != 6 {
		t.Fatalf("after resize: %dx%d, want 8x6", fb.Width, fb.Height)
	}
	if fb.Stride != 8*4 {
		t.Fatalf("stride = %d, want %d", fb.Stride, 8*4)
	}
	if len(fb.Pixels) != 8*6*4 {
		t.Fatalf("pixels len = %d, want %d", len(fb.Pixels), 8*6*4)
	}
}

func TestWriteRectBpp_24bpp(t *testing.T) {
	fb := NewFramebuffer(4, 4)

	// 2x1 row of 24bpp BGR pixels
	src := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	fb.WriteRectBpp(0, 0, 2, 1, 24, 2, src)

	// Read back as 32bpp
	dst := make([]byte, 2*1*4)
	fb.ReadRect(dst, 0, 0, 2, 1)

	want := []byte{0x30, 0x20, 0x10, 0xFF, 0x60, 0x50, 0x40, 0xFF}
	if !bytes.Equal(dst, want) {
		t.Fatalf("got %x, want %x", dst, want)
	}
}

func TestWriteRectBpp_16bpp_Strided(t *testing.T) {
	// Simulate bitmap with srcW=4 but only cx=2 pixels should be rendered.
	// 16bpp RGB565: 0xFFFF = white, 0xF800 = red, 0x0000 = black (padding)
	fb := NewFramebuffer(4, 4)

	// 2 rows of 4 pixels each (srcW=4), but we only want 2 pixels (w=2)
	// Row 0 (bottom): white white pad pad
	// Row 1 (top):    red   red   pad pad
	src := []byte{
		0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, // row 0: white white black black
		0x00, 0xF8, 0x00, 0xF8, 0x00, 0x00, 0x00, 0x00, // row 1: red red black black
	}
	fb.WriteRectBpp(0, 0, 2, 2, 16, 4, src) // w=2, srcW=4

	dst := make([]byte, 2*2*4)
	fb.ReadRect(dst, 0, 0, 2, 2)

	// Bottom-up: row 0 is bottom of framebuffer, row 1 is top
	// Row 0 (bottom): two white pixels
	// Row 1 (top): two red pixels
	// White in RGBA: (FF, FF, FF, FF)
	// Red in RGBA: (FF, 00, 00, FF)
	wantRow0 := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	wantRow1 := []byte{0xFF, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0xFF}
	want := append(wantRow0, wantRow1...)
	if !bytes.Equal(dst, want) {
		t.Fatalf("strided 16bpp:\ngot  %x\nwant %x", dst, want)
	}
}

func TestWriteRectBpp_32bpp_Strided(t *testing.T) {
	// 32bpp with srcW=3 but only w=2 pixels rendered
	fb := NewFramebuffer(4, 4)

	// 1 row: pixel0(RGBA) pixel1(RGBA) padding(RGBA)
	src := []byte{
		0xAA, 0xBB, 0xCC, 0xFF, // pixel 0
		0x11, 0x22, 0x33, 0xFF, // pixel 1
		0x00, 0x00, 0x00, 0x00, // padding pixel
	}
	fb.WriteRectBpp(0, 0, 2, 1, 32, 3, src) // w=2, srcW=3

	dst := make([]byte, 2*1*4)
	fb.ReadRect(dst, 0, 0, 2, 1)

	want := []byte{0xAA, 0xBB, 0xCC, 0xFF, 0x11, 0x22, 0x33, 0xFF}
	if !bytes.Equal(dst, want) {
		t.Fatalf("strided 32bpp:\ngot  %x\nwant %x", dst, want)
	}
}
