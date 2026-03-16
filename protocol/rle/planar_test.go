package rle

import (
	"bytes"
	"testing"
)

// encodePlanarCtrl builds a control byte: run in low nibble, raw in high nibble.
func encodePlanarCtrl(nRunLength, cRawBytes int) byte {
	return byte(nRunLength&0x0F) | byte((cRawBytes&0x0F)<<4)
}

func TestDecodePlaneRLE_SingleRowAbsolute(t *testing.T) {
	// 4-pixel wide, 1 row. Control: 0 run, 4 raw bytes.
	width, height := 4, 1
	src := []byte{
		encodePlanarCtrl(0, 4), // run=0, raw=4
		0xAA, 0xBB, 0xCC, 0xDD,
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(src) {
		t.Errorf("consumed=%d, want %d", consumed, len(src))
	}
	want := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecodePlaneRLE_RunOnly(t *testing.T) {
	// 8-pixel wide, 1 row. Control: 8 run, 0 raw → pixel stays 0.
	width, height := 8, 1
	src := []byte{
		encodePlanarCtrl(8, 0), // run=8, raw=0
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 1 {
		t.Errorf("consumed=%d, want 1", consumed)
	}
	want := make([]byte, 8) // all zeros (pixel starts at 0)
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecodePlaneRLE_RawThenRun(t *testing.T) {
	// 6-pixel wide, 1 row. raw=2 (set pixel), run=4 (repeat last).
	width, height := 6, 1
	src := []byte{
		encodePlanarCtrl(4, 2), // run=4, raw=2
		0x11, 0x22,             // raw bytes; pixel becomes 0x22
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 3 {
		t.Errorf("consumed=%d, want 3", consumed)
	}
	// raw: 0x11, 0x22; run: 4 copies of 0x22
	want := []byte{0x11, 0x22, 0x22, 0x22, 0x22, 0x22}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecodePlaneRLE_SpecialRun1(t *testing.T) {
	// nRunLength=1 is special: actual = cRawBytes + 16, cRawBytes = 0.
	// ctrl = 0x31: low=1, high=3 → actual run = 3 + 16 = 19.
	width, height := 19, 1
	src := []byte{
		encodePlanarCtrl(1, 3), // 0x31: special run = 3+16=19
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 1 {
		t.Errorf("consumed=%d, want 1", consumed)
	}
	want := make([]byte, 19) // all zeros
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecodePlaneRLE_SpecialRun2(t *testing.T) {
	// nRunLength=2 is special: actual = cRawBytes + 32, cRawBytes = 0.
	// ctrl = 0x52: low=2, high=5 → actual run = 5 + 32 = 37.
	width, height := 37, 1
	src := []byte{
		encodePlanarCtrl(2, 5), // 0x52: special run = 5+32=37
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 1 {
		t.Errorf("consumed=%d, want 1", consumed)
	}
	want := make([]byte, 37)
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecodePlaneRLE_64x64_F2_11_Pattern(t *testing.T) {
	// Real-world pattern: 64×64 plane encoded as repeating F2 11.
	// F2: low=2 (special), high=0x0F=15 → run = 15+32 = 47
	// 11: low=1 (special), high=1 → run = 1+16 = 17
	// Total per row: 47 + 17 = 64. All zeros (no raw bytes).
	width, height := 64, 64
	src := make([]byte, 0, height*2)
	for range height {
		src = append(src, 0xF2, 0x11)
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != height*2 {
		t.Errorf("consumed=%d, want %d", consumed, height*2)
	}
	// All zeros: first row absolute=0, subsequent rows delta=0.
	for i, b := range dst {
		if b != 0 {
			t.Fatalf("dst[%d]=%d, want 0", i, b)
		}
	}
}

func TestDecodePlaneRLE_DeltaEncoding(t *testing.T) {
	// 4-pixel wide, 3 rows.
	// Row 0 (absolute): raw=4 → [10, 20, 30, 40]
	// Row 1 (delta): raw=1 (delta=+5 zigzag=10), run=3
	//   zigzag: +5 → encoded as 10 (5*2)
	//   pixel[0] = 10 + 5 = 15, run repeats delta=+5 for remaining
	//   pixel[1] = 20 + 5 = 25, pixel[2] = 30 + 5 = 35, pixel[3] = 40 + 5 = 45
	// Row 2 (delta): raw=0, run=4 (delta stays 0 from row init)
	//   pixel[0] = 15+0 = 15, etc.
	width, height := 4, 3
	src := []byte{
		// Row 0: 4 raw, 0 run
		encodePlanarCtrl(0, 4), 10, 20, 30, 40,
		// Row 1: 1 raw (zigzag +5 = 0x0A), 3 run
		encodePlanarCtrl(3, 1), 0x0A,
		// Row 2: 0 raw, 4 run (delta=0)
		encodePlanarCtrl(4, 0),
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(src) {
		t.Errorf("consumed=%d, want %d", consumed, len(src))
	}
	want := []byte{
		10, 20, 30, 40, // row 0: absolute
		15, 25, 35, 45, // row 1: +5 delta
		15, 25, 35, 45, // row 2: +0 delta (same as row 1)
	}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %v, want %v", dst, want)
	}
}

func TestDecodePlaneRLE_NegativeDelta(t *testing.T) {
	// Zigzag encoding: odd byte = negative.
	// deltaValue=1 → -(1>>1) - 1 = -1
	// deltaValue=3 → -(3>>1) - 1 = -2
	width, height := 2, 2

	// Use non-special run lengths.
	src := []byte{
		// Row 0: raw=2 → [100, 200]
		encodePlanarCtrl(0, 2), 100, 200,
		// Row 1: raw=2 (delta -1, delta -2)
		// zigzag -1 = 1, zigzag -2 = 3
		encodePlanarCtrl(0, 2), 1, 3,
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(src) {
		t.Errorf("consumed=%d, want %d", consumed, len(src))
	}
	want := []byte{
		100, 200, // row 0
		99, 198, // row 1: 100-1, 200-2
	}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %v, want %v", dst, want)
	}
}

func TestDecodePlaneRLE_DeltaWrap(t *testing.T) {
	// Delta that overflows/underflows wraps around mod 256 (not clamped).
	width, height := 2, 2
	src := []byte{
		// Row 0: raw=2 → [2, 254]
		encodePlanarCtrl(0, 2), 2, 254,
		// Row 1: raw=2, deltas: -5 (zigzag=9), +5 (zigzag=10)
		encodePlanarCtrl(0, 2), 9, 10,
	}
	dst := make([]byte, width*height)
	consumed, err := decodePlaneRLE(dst, src, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(src) {
		t.Errorf("consumed=%d, want %d", consumed, len(src))
	}
	// 2 + (-5) = -3 → byte(-3) = 253 (wraps)
	// 254 + 5 = 259 → byte(259) = 3 (wraps)
	want := []byte{2, 254, 253, 3}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %v, want %v", dst, want)
	}
}

func TestDecodePlaneRLEStrided_Stride4(t *testing.T) {
	// Verify strided decode writes at stride-4 intervals.
	// 3-pixel wide, 2 rows.
	width, height := 3, 2
	stride := 4
	dst := make([]byte, width*height*stride)
	src := []byte{
		// Row 0: raw=3 → [0xAA, 0xBB, 0xCC]
		encodePlanarCtrl(0, 3), 0xAA, 0xBB, 0xCC,
		// Row 1: raw=3, deltas: +1(zigzag=2), +2(zigzag=4), +3(zigzag=6)
		encodePlanarCtrl(0, 3), 0x02, 0x04, 0x06,
	}
	consumed, err := decodePlaneRLEStrided(dst, src, width, height, stride)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(src) {
		t.Errorf("consumed=%d, want %d", consumed, len(src))
	}
	// Row 0: dst[0]=0xAA, dst[4]=0xBB, dst[8]=0xCC
	// Row 1: dst[12]=0xAA+1=0xAB, dst[16]=0xBB+2=0xBD, dst[20]=0xCC+3=0xCF
	want := make([]byte, width*height*stride)
	want[0] = 0xAA
	want[4] = 0xBB
	want[8] = 0xCC
	want[12] = 0xAB
	want[16] = 0xBD
	want[20] = 0xCF
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecompressPlanar_NoAlphaRLE(t *testing.T) {
	// Header: RLE + NA (no alpha) = 0x30.
	// 2×1 image, 3 planes (R, G, B), each 2 bytes.
	width, height := 2, 1
	hdr := byte(planarRLE | planarNA) // 0x30
	src := []byte{
		hdr,
		// Red plane: raw=2 → [0xAA, 0xBB]
		encodePlanarCtrl(0, 2), 0xAA, 0xBB,
		// Green plane: raw=2 → [0xCC, 0xDD]
		encodePlanarCtrl(0, 2), 0xCC, 0xDD,
		// Blue plane: raw=2 → [0xEE, 0xFF]
		encodePlanarCtrl(0, 2), 0xEE, 0xFF,
	}
	dst, err := DecompressPlanar(nil, width, height, src)
	if err != nil {
		t.Fatal(err)
	}
	// Output is bottom-up RGBA. 1 row = no flip needed (height=1, dstY=0).
	// Pixel 0: R=0xAA, G=0xCC, B=0xEE, A=0xFF
	// Pixel 1: R=0xBB, G=0xDD, B=0xFF, A=0xFF
	want := []byte{0xAA, 0xCC, 0xEE, 0xFF, 0xBB, 0xDD, 0xFF, 0xFF}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecompressPlanar_WithAlphaRLE(t *testing.T) {
	// Header: RLE only = 0x10. Alpha present.
	// 2×1 image, 4 planes (A, R, G, B), each 2 bytes.
	width, height := 2, 1
	hdr := byte(planarRLE) // 0x10
	src := []byte{
		hdr,
		// Alpha plane: raw=2 → [0xFF, 0xFF]
		encodePlanarCtrl(0, 2), 0xFF, 0xFF,
		// Red plane: raw=2 → [0x11, 0x22]
		encodePlanarCtrl(0, 2), 0x11, 0x22,
		// Green plane: raw=2 → [0x33, 0x44]
		encodePlanarCtrl(0, 2), 0x33, 0x44,
		// Blue plane: raw=2 → [0x55, 0x66]
		encodePlanarCtrl(0, 2), 0x55, 0x66,
	}
	dst, err := DecompressPlanar(nil, width, height, src)
	if err != nil {
		t.Fatal(err)
	}
	// Pixel 0: R=0x11, G=0x33, B=0x55, A=0xFF (alpha forced)
	// Pixel 1: R=0x22, G=0x44, B=0x66, A=0xFF (alpha forced)
	want := []byte{0x11, 0x33, 0x55, 0xFF, 0x22, 0x44, 0x66, 0xFF}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func TestDecompressPlanar_NoVerticalFlip(t *testing.T) {
	// 1×2 image (1 wide, 2 tall), 3 planes (no alpha).
	// Planar stream is bottom-up (matching RDP convention), no flip needed.
	width, height := 1, 2
	hdr := byte(planarRLE | planarNA) // 0x30
	src := []byte{
		hdr,
		// Red plane: raw=1 (val=0xAA), run=0 for row 0; delta row 1
		encodePlanarCtrl(0, 1), 0xAA, // row 0: R=0xAA
		encodePlanarCtrl(0, 1), 0x04, // row 1: delta +2 (zigzag 4) → 0xAA+2=0xAC
		// Green plane:
		encodePlanarCtrl(0, 1), 0x10, // row 0: G=0x10
		encodePlanarCtrl(0, 1), 0x00, // row 1: delta 0 → 0x10
		// Blue plane:
		encodePlanarCtrl(0, 1), 0xFF, // row 0: B=0xFF
		encodePlanarCtrl(0, 1), 0x01, // row 1: delta -1 (zigzag 1) → 0xFE
	}
	dst, err := DecompressPlanar(nil, width, height, src)
	if err != nil {
		t.Fatal(err)
	}
	// Output preserves stream order (bottom-up RGBA): row 0 first, row 1 second.
	// Pixel at dst[0..3] = row 0: R=0xAA, G=0x10, B=0xFF, A=0xFF
	// Pixel at dst[4..7] = row 1: R=0xAC, G=0x10, B=0xFE, A=0xFF
	want := []byte{
		0xAA, 0x10, 0xFF, 0xFF, // row 0
		0xAC, 0x10, 0xFE, 0xFF, // row 1
	}
	if !bytes.Equal(dst, want) {
		t.Errorf("got %X, want %X", dst, want)
	}
}

func BenchmarkDecompressPlanar_64x64(b *testing.B) {
	width, height := 64, 64
	// Build a valid planar RLE source: header + 3 planes (no alpha), each all-zeros.
	src := make([]byte, 0, 1+3*height*2)
	src = append(src, byte(planarRLE|planarNA)) // 0x30
	for range 3 {
		for range height {
			src = append(src, 0xF2, 0x11) // 47+17=64 run per row
		}
	}
	var dst []byte
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		var err error
		dst, err = DecompressPlanar(dst, width, height, src)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompressPlanar_1920x1080(b *testing.B) {
	width, height := 1920, 1080
	// Build an uncompressed planar source (no RLE) with no alpha.
	planeSize := width * height
	src := make([]byte, 1+3*planeSize)
	src[0] = byte(planarNA) // 0x20 (no RLE, no alpha)
	var dst []byte
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		var err error
		dst, err = DecompressPlanar(dst, width, height, src)
		if err != nil {
			b.Fatal(err)
		}
	}
}
