package cliprdr

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestEncodeDecodeUTF16LE(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"ASCII", "hello"},
		{"CJK", "\u4f60\u597d"},       // 你好
		{"emoji", "\U0001F600"},        // 😀 (surrogate pair)
		{"mixed", "Hi 你好 \U0001F600"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeUTF16LE(tt.input)
			decoded := decodeUTF16LE(encoded)
			if decoded != tt.input {
				t.Errorf("round-trip failed: got %q, want %q", decoded, tt.input)
			}
		})
	}
}

func TestDecodeUTF16LE_TrailingNulls(t *testing.T) {
	// "AB" in UTF-16LE + 2 null code units
	data := []byte{0x41, 0x00, 0x42, 0x00, 0x00, 0x00, 0x00, 0x00}
	got := decodeUTF16LE(data)
	if got != "AB" {
		t.Errorf("got %q, want %q", got, "AB")
	}
}

func TestDecodeUTF16LE_OddLength(t *testing.T) {
	// 5 bytes — last byte should be dropped
	data := []byte{0x41, 0x00, 0x42, 0x00, 0xFF}
	got := decodeUTF16LE(data)
	if got != "AB" {
		t.Errorf("got %q, want %q", got, "AB")
	}
}

func TestDecodeUTF16LE_TooShort(t *testing.T) {
	if got := decodeUTF16LE(nil); got != "" {
		t.Errorf("nil: got %q, want empty", got)
	}
	if got := decodeUTF16LE([]byte{0x41}); got != "" {
		t.Errorf("1 byte: got %q, want empty", got)
	}
}

func TestTextToWire(t *testing.T) {
	// "a\nb" should become "a\r\nb" in UTF-16LE with null terminator
	wire := textToWire("a\nb")
	// Expected: 'a'(2) + CR(2) + LF(2) + 'b'(2) + null(2) = 10 bytes
	want := []byte{
		0x61, 0x00, // a
		0x0D, 0x00, // CR
		0x0A, 0x00, // LF
		0x62, 0x00, // b
		0x00, 0x00, // null
	}
	if !bytes.Equal(wire, want) {
		t.Errorf("textToWire(\"a\\nb\"):\n  got  %v\n  want %v", wire, want)
	}
}

func TestTextToWire_ExistingCRLF(t *testing.T) {
	// Already-normalized CR-LF should not be doubled
	wire := textToWire("a\r\nb")
	want := []byte{
		0x61, 0x00,
		0x0D, 0x00,
		0x0A, 0x00,
		0x62, 0x00,
		0x00, 0x00,
	}
	if !bytes.Equal(wire, want) {
		t.Errorf("textToWire(\"a\\r\\nb\"):\n  got  %v\n  want %v", wire, want)
	}
}

func TestTextFromWire(t *testing.T) {
	// "a\r\nb" in UTF-16LE with null terminator
	wire := []byte{
		0x61, 0x00,
		0x0D, 0x00,
		0x0A, 0x00,
		0x62, 0x00,
		0x00, 0x00,
	}
	got := textFromWire(wire)
	if got != "a\nb" {
		t.Errorf("got %q, want %q", got, "a\nb")
	}
}

func TestLineEndingRoundTrip(t *testing.T) {
	tests := []string{
		"hello\nworld",
		"line1\nline2\nline3",
		"no newlines",
		"",
		"trailing\n",
	}
	for _, s := range tests {
		wire := textToWire(s)
		got := textFromWire(wire)
		if got != s {
			t.Errorf("round-trip %q: got %q", s, got)
		}
	}
}

// --- DIB ↔ PNG conversion tests ---

func TestDibToPNG_24bit(t *testing.T) {
	// 2x2 bottom-up 24-bit DIB: row stride = (2*3 + 3) &^ 3 = 8
	w, h := 2, 2
	rowBytes := 8
	dib := make([]byte, bihSize+rowBytes*h)
	binary.LittleEndian.PutUint32(dib[0:4], bihSize)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(w))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(h)) // positive = bottom-up
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 24)

	// Bottom row (y=1 in image) first, then top row (y=0 in image).
	// Row 0 in DIB (bottom of image): blue, green
	off := bihSize
	dib[off], dib[off+1], dib[off+2] = 0xFF, 0x00, 0x00 // pixel(0,1) = B=FF, G=00, R=00 → blue
	dib[off+3], dib[off+4], dib[off+5] = 0x00, 0xFF, 0x00 // pixel(1,1) = B=00, G=FF, R=00 → green
	// Row 1 in DIB (top of image): red, white
	off = bihSize + rowBytes
	dib[off], dib[off+1], dib[off+2] = 0x00, 0x00, 0xFF // pixel(0,0) = B=00, G=00, R=FF → red
	dib[off+3], dib[off+4], dib[off+5] = 0xFF, 0xFF, 0xFF // pixel(1,0) = white

	pngData, err := dibToPNG(dib)
	if err != nil {
		t.Fatalf("dibToPNG: %v", err)
	}

	// Decode the PNG and verify pixels
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != w || bounds.Dy() != h {
		t.Fatalf("image = %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), w, h)
	}

	// Top-left (0,0) should be red
	r, g, b, a := img.At(0, 0).RGBA()
	if r>>8 != 0xFF || g>>8 != 0x00 || b>>8 != 0x00 || a>>8 != 0xFF {
		t.Errorf("pixel(0,0) = (%d,%d,%d,%d), want (255,0,0,255)", r>>8, g>>8, b>>8, a>>8)
	}
	// Bottom-left (0,1) should be blue
	r, g, b, a = img.At(0, 1).RGBA()
	if r>>8 != 0x00 || g>>8 != 0x00 || b>>8 != 0xFF || a>>8 != 0xFF {
		t.Errorf("pixel(0,1) = (%d,%d,%d,%d), want (0,0,255,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestDibToPNG_32bit(t *testing.T) {
	// 1x1 32-bit BGRA bottom-up
	w, h := 1, 1
	rowBytes := 4
	dib := make([]byte, bihSize+rowBytes*h)
	binary.LittleEndian.PutUint32(dib[0:4], bihSize)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(w))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(h))
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)

	off := bihSize
	dib[off] = 0x00   // B
	dib[off+1] = 0xFF // G
	dib[off+2] = 0x00 // R
	dib[off+3] = 0x80 // A = 128

	pngData, err := dibToPNG(dib)
	if err != nil {
		t.Fatalf("dibToPNG: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	// NRGBA pixel: R=0, G=255, B=0, A=128.
	// img.At().RGBA() returns premultiplied values scaled to 0..65535,
	// so verify via the underlying NRGBA model directly.
	nrgba := img.(*image.NRGBA).NRGBAAt(0, 0)
	if nrgba.G != 0xFF || nrgba.A != 0x80 {
		t.Errorf("pixel = NRGBA(%d,%d,%d,%d), want G=255,A=128", nrgba.R, nrgba.G, nrgba.B, nrgba.A)
	}
}

func TestDibToPNG_TopDown(t *testing.T) {
	// Negative height = top-down
	w, h := 1, 1
	rowBytes := 4
	dib := make([]byte, bihSize+rowBytes*h)
	binary.LittleEndian.PutUint32(dib[0:4], bihSize)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(w))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(int32(-h))) // negative = top-down
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)

	off := bihSize
	dib[off] = 0xFF   // B
	dib[off+1] = 0x00 // G
	dib[off+2] = 0x00 // R
	dib[off+3] = 0xFF // A

	pngData, err := dibToPNG(dib)
	if err != nil {
		t.Fatalf("dibToPNG: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	r, g, b, _ := img.At(0, 0).RGBA()
	if r>>8 != 0 || g>>8 != 0 || b>>8 != 0xFF {
		t.Errorf("pixel = (%d,%d,%d), want (0,0,255)", r>>8, g>>8, b>>8)
	}
}

func TestDibToPNG_Errors(t *testing.T) {
	// Too short
	if _, err := dibToPNG([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for short DIB")
	}

	// Zero width
	dib := make([]byte, bihSize)
	binary.LittleEndian.PutUint32(dib[0:4], bihSize)
	binary.LittleEndian.PutUint32(dib[8:12], 1) // height
	binary.LittleEndian.PutUint16(dib[14:16], 24)
	if _, err := dibToPNG(dib); err == nil {
		t.Error("expected error for zero-width DIB")
	}

	// Unsupported compression
	dib2 := make([]byte, bihSize)
	binary.LittleEndian.PutUint32(dib2[0:4], bihSize)
	binary.LittleEndian.PutUint32(dib2[4:8], 1)
	binary.LittleEndian.PutUint32(dib2[8:12], 1)
	binary.LittleEndian.PutUint16(dib2[14:16], 16) // 16-bit not supported
	binary.LittleEndian.PutUint32(dib2[16:20], biRGB)
	if _, err := dibToPNG(dib2); err == nil {
		t.Error("expected error for unsupported bit depth")
	}
}

func TestPngToDIB_RoundTrip(t *testing.T) {
	// Create a 3x2 NRGBA image
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{R: 0, G: 255, B: 0, A: 255})
	img.SetNRGBA(2, 0, color.NRGBA{R: 0, G: 0, B: 255, A: 255})
	img.SetNRGBA(0, 1, color.NRGBA{R: 255, G: 255, B: 0, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{R: 0, G: 255, B: 255, A: 255})
	img.SetNRGBA(2, 1, color.NRGBA{R: 255, G: 0, B: 255, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	pngData := buf.Bytes()

	// PNG → DIB → PNG round-trip
	dib, err := pngToDIB(pngData)
	if err != nil {
		t.Fatalf("pngToDIB: %v", err)
	}

	// Verify DIB header
	if binary.LittleEndian.Uint32(dib[0:4]) != bihSize {
		t.Error("biSize mismatch")
	}
	w := binary.LittleEndian.Uint32(dib[4:8])
	h := binary.LittleEndian.Uint32(dib[8:12])
	if w != 3 || h != 2 {
		t.Errorf("DIB dimensions = %dx%d, want 3x2", w, h)
	}
	bpp := binary.LittleEndian.Uint16(dib[14:16])
	if bpp != 32 {
		t.Errorf("bpp = %d, want 32", bpp)
	}

	// Convert back to PNG
	pngData2, err := dibToPNG(dib)
	if err != nil {
		t.Fatalf("dibToPNG: %v", err)
	}

	img2, err := png.Decode(bytes.NewReader(pngData2))
	if err != nil {
		t.Fatalf("png.Decode round-trip: %v", err)
	}

	// Verify top-left pixel is red
	r, g, b, a := img2.At(0, 0).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 || a>>8 != 255 {
		t.Errorf("round-trip pixel(0,0) = (%d,%d,%d,%d), want (255,0,0,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestPngToDIB_Invalid(t *testing.T) {
	if _, err := pngToDIB([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for invalid PNG")
	}
}
