package nscodec

import (
	"encoding/binary"
	"testing"
)

func TestDecodePlaneRLE_Run(t *testing.T) {
	// RLE: value=0x42, duplicate=0x42, factor=3 → run of 5 (3+2)
	// EndData: 0xAA 0xBB 0xCC 0xDD
	src := []byte{0x42, 0x42, 3, 0xAA, 0xBB, 0xCC, 0xDD}
	dstSize := 9 // 5 run + 4 enddata
	dst := make([]byte, dstSize)
	if err := decodePlaneRLE(dst, src, dstSize); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if dst[i] != 0x42 {
			t.Errorf("dst[%d] = 0x%02X, want 0x42", i, dst[i])
		}
	}
	if dst[5] != 0xAA || dst[6] != 0xBB || dst[7] != 0xCC || dst[8] != 0xDD {
		t.Errorf("EndData = %v, want [AA BB CC DD]", dst[5:9])
	}
}

func TestDecodePlaneRLE_LongRun(t *testing.T) {
	// Long run: value=0x10, dup=0x10, factor=0xFF, count(u32)=300
	var src []byte
	src = append(src, 0x10, 0x10, 0xFF)
	var countBuf [4]byte
	binary.LittleEndian.PutUint32(countBuf[:], 300)
	src = append(src, countBuf[:]...)
	// EndData
	src = append(src, 0x01, 0x02, 0x03, 0x04)

	dstSize := 304 // 300 run + 4 enddata
	dst := make([]byte, dstSize)
	if err := decodePlaneRLE(dst, src, dstSize); err != nil {
		t.Fatal(err)
	}
	for i := range 300 {
		if dst[i] != 0x10 {
			t.Fatalf("dst[%d] = 0x%02X, want 0x10", i, dst[i])
		}
	}
	if dst[300] != 0x01 || dst[301] != 0x02 || dst[302] != 0x03 || dst[303] != 0x04 {
		t.Errorf("EndData = %v", dst[300:304])
	}
}

func TestDecodePlaneRLE_Literal(t *testing.T) {
	// All distinct bytes — each is a literal
	// Need dstSize = 8 (4 literals + 4 enddata)
	src := []byte{0x01, 0x02, 0x03, 0x04, 0xE1, 0xE2, 0xE3, 0xE4}
	dstSize := 8
	dst := make([]byte, dstSize)
	if err := decodePlaneRLE(dst, src, dstSize); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x01, 0x02, 0x03, 0x04, 0xE1, 0xE2, 0xE3, 0xE4}
	for i, b := range want {
		if dst[i] != b {
			t.Errorf("dst[%d] = 0x%02X, want 0x%02X", i, dst[i], b)
		}
	}
}

func TestDecodePlaneRLE_EndData(t *testing.T) {
	// One literal + 4 enddata
	src := []byte{0xFF, 0xDE, 0xAD, 0xBE, 0xEF}
	dstSize := 5
	dst := make([]byte, dstSize)
	if err := decodePlaneRLE(dst, src, dstSize); err != nil {
		t.Fatal(err)
	}
	if dst[0] != 0xFF {
		t.Errorf("dst[0] = 0x%02X, want 0xFF", dst[0])
	}
	if dst[1] != 0xDE || dst[2] != 0xAD || dst[3] != 0xBE || dst[4] != 0xEF {
		t.Errorf("EndData = %v, want [DE AD BE EF]", dst[1:5])
	}
}

func TestDecodePlane_Uncompressed(t *testing.T) {
	// When srcLen == dstSize, raw copy
	src := []byte{10, 20, 30, 40, 50}
	dst := make([]byte, 5)
	if err := decodePlaneRLE(dst, src, 5); err != nil {
		t.Fatal(err)
	}
	for i, b := range src {
		if dst[i] != b {
			t.Errorf("dst[%d] = %d, want %d", i, dst[i], b)
		}
	}
}

func TestDecompress_NoSubsampling(t *testing.T) {
	// 2x2 image, no subsampling, colorLossLevel=1 (shift=0)
	width, height := 2, 2
	pixelCount := width * height

	// Planes: all uncompressed (srcLen == dstSize)
	luma := []byte{128, 128, 128, 128}
	co := []byte{0, 0, 0, 0}   // co=0 after int8 cast
	cg := []byte{0, 0, 0, 0}   // cg=0 after int8 cast
	alpha := []byte{255, 255, 255, 255}

	var src []byte
	// Header: lumaLen(4) + coLen(4) + cgLen(4) + alphaLen(4) + colorLossLevel(1) + chromaSubLevel(1) + reserved(2)
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(luma)))
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(co)))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(len(cg)))
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(alpha)))
	hdr[16] = 1 // colorLossLevel
	hdr[17] = 0 // chromaSubLevel (no subsampling)
	src = append(src, hdr...)
	src = append(src, luma...)
	src = append(src, co...)
	src = append(src, cg...)
	src = append(src, alpha...)

	dst, planes, err := Decompress(nil, nil, width, height, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dst) != pixelCount*4 {
		t.Fatalf("dst len = %d, want %d", len(dst), pixelCount*4)
	}
	_ = planes

	// Y=128, Co=0, Cg=0 → R=128, G=128, B=128
	for i := range pixelCount {
		off := i * 4
		if dst[off] != 128 || dst[off+1] != 128 || dst[off+2] != 128 {
			t.Errorf("pixel %d: B=%d G=%d R=%d, want 128/128/128", i, dst[off], dst[off+1], dst[off+2])
		}
		if dst[off+3] != 255 {
			t.Errorf("pixel %d: A=%d, want 255", i, dst[off+3])
		}
	}
}

func TestDecompress_WithSubsampling(t *testing.T) {
	// 4x4 image, chromaSub=1 → chroma is 2x2
	width, height := 4, 4
	pixelCount := width * height
	chromaW := (width + 1) / 2
	chromaH := (height + 1) / 2
	chromaSize := chromaW * chromaH // 4

	luma := make([]byte, pixelCount)
	for i := range luma {
		luma[i] = 128
	}
	co := make([]byte, chromaSize) // all zeros
	cg := make([]byte, chromaSize) // all zeros
	alpha := make([]byte, pixelCount)
	for i := range alpha {
		alpha[i] = 200
	}

	var src []byte
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(luma)))
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(co)))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(len(cg)))
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(alpha)))
	hdr[16] = 1 // colorLossLevel
	hdr[17] = 1 // chromaSubLevel
	src = append(src, hdr...)
	src = append(src, luma...)
	src = append(src, co...)
	src = append(src, cg...)
	src = append(src, alpha...)

	dst, _, err := Decompress(nil, nil, width, height, src, nil)
	if err != nil {
		t.Fatal(err)
	}

	for i := range pixelCount {
		off := i * 4
		if dst[off] != 128 || dst[off+1] != 128 || dst[off+2] != 128 {
			t.Errorf("pixel %d: B=%d G=%d R=%d, want 128/128/128", i, dst[off], dst[off+1], dst[off+2])
		}
		if dst[off+3] != 200 {
			t.Errorf("pixel %d: A=%d, want 200", i, dst[off+3])
		}
	}
}

func TestDecompress_ColorLoss(t *testing.T) {
	// colorLossLevel=3 → shift=2
	// co=0x10 → int8(0x10 << 2) = int8(0x40) = 64
	// cg=0x08 → int8(0x08 << 2) = int8(0x20) = 32
	// Y=128: R=128+64-32=160, G=128+32=160, B=128-64-32=32
	width, height := 1, 1

	luma := []byte{128}
	co := []byte{0x10}
	cg := []byte{0x08}
	alpha := []byte{255}

	var src []byte
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], 1)
	binary.LittleEndian.PutUint32(hdr[4:], 1)
	binary.LittleEndian.PutUint32(hdr[8:], 1)
	binary.LittleEndian.PutUint32(hdr[12:], 1)
	hdr[16] = 3 // colorLossLevel
	hdr[17] = 0 // no subsampling
	src = append(src, hdr...)
	src = append(src, luma...)
	src = append(src, co...)
	src = append(src, cg...)
	src = append(src, alpha...)

	dst, _, err := Decompress(nil, nil, width, height, src, nil)
	if err != nil {
		t.Fatal(err)
	}

	r, g, b, a := dst[0], dst[1], dst[2], dst[3]
	if r != 160 || g != 160 || b != 32 || a != 255 {
		t.Errorf("RGBA = %d/%d/%d/%d, want 160/160/32/255", r, g, b, a)
	}
}

func TestDecompress_Clamp(t *testing.T) {
	// Test clamping: Y=250, co=0x40→int8(0x40<<0)=64, cg=0
	// R = 250+64-0 = 314 → clamp to 255
	// G = 250+0 = 250
	// B = 250-64-0 = 186
	width, height := 1, 1

	luma := []byte{250}
	co := []byte{0x40} // int8(0x40) = 64
	cg := []byte{0}
	alpha := []byte{255}

	var src []byte
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], 1)
	binary.LittleEndian.PutUint32(hdr[4:], 1)
	binary.LittleEndian.PutUint32(hdr[8:], 1)
	binary.LittleEndian.PutUint32(hdr[12:], 1)
	hdr[16] = 1 // colorLossLevel (shift=0)
	hdr[17] = 0
	src = append(src, hdr...)
	src = append(src, luma...)
	src = append(src, co...)
	src = append(src, cg...)
	src = append(src, alpha...)

	dst, _, err := Decompress(nil, nil, width, height, src, nil)
	if err != nil {
		t.Fatal(err)
	}

	r, g, b := dst[0], dst[1], dst[2]
	if r != 255 { // clamped
		t.Errorf("R = %d, want 255 (clamped)", r)
	}
	if g != 250 {
		t.Errorf("G = %d, want 250", g)
	}
	if b != 186 {
		t.Errorf("B = %d, want 186", b)
	}
}

func TestDecompress_ClampNegative(t *testing.T) {
	// Test negative clamping: Y=10, co=0xC0→int8(0xC0)=-64, cg=0
	// R = 10+(-64)-0 = -54 → clamp to 0
	// G = 10+0 = 10
	// B = 10-(-64)-0 = 74
	width, height := 1, 1

	luma := []byte{10}
	co := []byte{0xC0} // int8(0xC0) = -64
	cg := []byte{0}
	alpha := []byte{255}

	var src []byte
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], 1)
	binary.LittleEndian.PutUint32(hdr[4:], 1)
	binary.LittleEndian.PutUint32(hdr[8:], 1)
	binary.LittleEndian.PutUint32(hdr[12:], 1)
	hdr[16] = 1
	hdr[17] = 0
	src = append(src, hdr...)
	src = append(src, luma...)
	src = append(src, co...)
	src = append(src, cg...)
	src = append(src, alpha...)

	dst, _, err := Decompress(nil, nil, width, height, src, nil)
	if err != nil {
		t.Fatal(err)
	}

	r, g, b := dst[0], dst[1], dst[2]
	if r != 0 { // clamped
		t.Errorf("R = %d, want 0 (clamped)", r)
	}
	if g != 10 {
		t.Errorf("G = %d, want 10", g)
	}
	if b != 74 {
		t.Errorf("B = %d, want 74", b)
	}
}

func TestDecompress_BufferReuse(t *testing.T) {
	width, height := 2, 2

	buildSrc := func(y byte) []byte {
		luma := []byte{y, y, y, y}
		co := []byte{0, 0, 0, 0}
		cg := []byte{0, 0, 0, 0}
		alpha := []byte{255, 255, 255, 255}

		hdr := make([]byte, headerSize)
		binary.LittleEndian.PutUint32(hdr[0:], 4)
		binary.LittleEndian.PutUint32(hdr[4:], 4)
		binary.LittleEndian.PutUint32(hdr[8:], 4)
		binary.LittleEndian.PutUint32(hdr[12:], 4)
		hdr[16] = 1
		hdr[17] = 0
		var src []byte
		src = append(src, hdr...)
		src = append(src, luma...)
		src = append(src, co...)
		src = append(src, cg...)
		src = append(src, alpha...)
		return src
	}

	// First call — allocates
	dst, planes, err := Decompress(nil, nil, width, height, buildSrc(100), nil)
	if err != nil {
		t.Fatal(err)
	}
	if dst[0] != 100 {
		t.Fatalf("first call: B = %d, want 100", dst[0])
	}

	// Second call — reuses buffers
	dst2, planes2, err := Decompress(dst, planes, width, height, buildSrc(200), nil)
	if err != nil {
		t.Fatal(err)
	}
	if dst2[0] != 200 {
		t.Fatalf("second call: B = %d, want 200", dst2[0])
	}
	// Verify it actually reused the slice backing arrays
	if cap(dst2) != cap(dst) {
		t.Errorf("dst buffer was reallocated (cap %d vs %d)", cap(dst2), cap(dst))
	}
	if cap(planes2) != cap(planes) {
		t.Errorf("planes buffer was reallocated (cap %d vs %d)", cap(planes2), cap(planes))
	}
}

func TestDecompress_ShortHeader(t *testing.T) {
	_, _, err := Decompress(nil, nil, 1, 1, make([]byte, 19), nil)
	if err == nil {
		t.Fatal("expected error for short header")
	}
}

// buildBenchSrc creates an NSCodec-encoded buffer for benchmarking.
// Uses uncompressed planes (srcLen == dstSize) for worst-case decode.
func buildBenchSrc(width, height int) []byte {
	pixelCount := width * height
	luma := make([]byte, pixelCount)
	co := make([]byte, pixelCount)
	cg := make([]byte, pixelCount)
	alpha := make([]byte, pixelCount)
	for i := range pixelCount {
		luma[i] = byte(i)
		co[i] = byte(i >> 1)
		cg[i] = byte(i >> 2)
		alpha[i] = 255
	}

	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(pixelCount))
	binary.LittleEndian.PutUint32(hdr[4:], uint32(pixelCount))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(pixelCount))
	binary.LittleEndian.PutUint32(hdr[12:], uint32(pixelCount))
	hdr[16] = 1 // colorLossLevel
	hdr[17] = 0 // no subsampling

	var src []byte
	src = append(src, hdr...)
	src = append(src, luma...)
	src = append(src, co...)
	src = append(src, cg...)
	src = append(src, alpha...)
	return src
}

func BenchmarkDecompress_64x64(b *testing.B) {
	src := buildBenchSrc(64, 64)
	var dst, planes []byte
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		dst, planes, err = Decompress(dst, planes, 64, 64, src, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompress_1920x1080(b *testing.B) {
	src := buildBenchSrc(1920, 1080)
	var dst, planes []byte
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		dst, planes, err = Decompress(dst, planes, 1920, 1080, src, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
