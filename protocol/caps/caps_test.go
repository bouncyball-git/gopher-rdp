package caps

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestEncodeCapSetHeader(t *testing.T) {
	tests := []struct {
		name     string
		encoder  func() []byte
		wantType uint16
		wantLen  int // total length including 4-byte header
	}{
		{"General", EncodeGeneral, TypeGeneral, 24},
		{"Bitmap", func() []byte { return EncodeBitmap(1024, 768, 24) }, TypeBitmap, 28},
		{"Order", EncodeOrder, TypeOrder, 88},
		{"BitmapCacheV2", EncodeBitmapCacheV2, TypeBitmapCacheV2, 40},
		{"Pointer", EncodePointer, TypePointer, 10},
		{"Input", EncodeInput, TypeInput, 88},
		{"Brush", EncodeBrush, TypeBrush, 8},
		{"GlyphCache", EncodeGlyphCache, TypeGlyphCache, 52},
		{"OffscreenBitmapCache", EncodeOffscreenBitmapCache, TypeOffscreenBitmapCache, 12},
		{"VirtualChannel", EncodeVirtualChannel, TypeVirtualChannel, 8},
		{"Sound", EncodeSound, TypeSound, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.encoder()
			if len(data) != tt.wantLen {
				t.Fatalf("length = %d, want %d", len(data), tt.wantLen)
			}

			gotType := binary.LittleEndian.Uint16(data[0:2])
			if gotType != tt.wantType {
				t.Errorf("type = 0x%04X, want 0x%04X", gotType, tt.wantType)
			}

			gotLen := binary.LittleEndian.Uint16(data[2:4])
			if int(gotLen) != tt.wantLen {
				t.Errorf("encoded length = %d, want %d", gotLen, tt.wantLen)
			}
		})
	}
}

func TestEncodeGeneralExtraFlags(t *testing.T) {
	data := EncodeGeneral()
	// extraFlags is at payload offset 10 (header=4, so byte offset 14)
	flags := binary.LittleEndian.Uint16(data[14:16])
	if flags != 0x040D {
		t.Errorf("extraFlags = 0x%04X, want 0x040D", flags)
	}
}

func TestEncodeBitmapFields(t *testing.T) {
	data := EncodeBitmap(1920, 1080, 32)
	// preferredBitsPerPixel at payload[0] = data[4]
	depth := binary.LittleEndian.Uint16(data[4:6])
	if depth != 32 {
		t.Errorf("depth = %d, want 32", depth)
	}
	width := binary.LittleEndian.Uint16(data[12:14])
	if width != 1920 {
		t.Errorf("width = %d, want 1920", width)
	}
	height := binary.LittleEndian.Uint16(data[14:16])
	if height != 1080 {
		t.Errorf("height = %d, want 1080", height)
	}
	// bitmapCompressionFlag at payload[16] = data[20]
	comp := binary.LittleEndian.Uint16(data[20:22])
	if comp != 1 {
		t.Errorf("bitmapCompressionFlag = %d, want 1", comp)
	}
	// multipleRectangleSupport at payload[20] = data[24]
	multi := binary.LittleEndian.Uint16(data[24:26])
	if multi != 1 {
		t.Errorf("multipleRectangleSupport = %d, want 1", multi)
	}
	// drawingFlags at payload[22] = data[26] — 0 (MS-RDPBCGR 2.2.7.1.2)
	if data[26] != 0x00 {
		t.Errorf("drawingFlags = 0x%02X, want 0x00", data[26])
	}
}

func TestEncodeOrderFlags(t *testing.T) {
	data := EncodeOrder()
	// orderFlags at payload[30] = data[34]
	flags := binary.LittleEndian.Uint16(data[34:36])
	if flags != 0x002A {
		t.Errorf("orderFlags = 0x%04X, want 0x002A", flags)
	}
}

func TestEncodeOrderGlyphSupport(t *testing.T) {
	data := EncodeOrder()
	// orderSupport[32] starts at payload offset 32 = data[36]
	wantSet := []struct {
		idx  int
		name string
	}{
		{0x00, "DstBlt"}, {0x01, "PatBlt"}, {0x02, "ScrBlt"},
		{0x03, "MemBlt"}, {0x04, "Mem3Blt"}, {0x08, "LineTo"},
		{0x0A, "OpaqueRect"}, {0x0B, "SaveBitmap"},
		{0x13, "FastIndex"}, {0x14, "PolygonSC"}, {0x15, "PolygonCB"},
		{0x16, "Polyline"}, {0x18, "FastGlyph"},
		{0x19, "EllipseSC"}, {0x1A, "EllipseCB"}, {0x1B, "GlyphIndex"},
	}
	for _, tc := range wantSet {
		if data[36+tc.idx] != 1 {
			t.Errorf("orderSupport[0x%02X] (%s) = %d, want 1", tc.idx, tc.name, data[36+tc.idx])
		}
	}
}

func TestEncodeBrushColorSupport(t *testing.T) {
	data := EncodeBrush()
	brushSupport := binary.LittleEndian.Uint32(data[4:8])
	if brushSupport != 1 {
		t.Errorf("brushSupportLevel = %d, want 1 (BRUSH_COLOR_8x8)", brushSupport)
	}
}

func TestEncodeGlyphCacheConfig(t *testing.T) {
	data := EncodeGlyphCache()
	// 10 caches starting at payload offset 0 = data[4]
	// Cache 0: 254 entries, 4 bytes
	entries0 := binary.LittleEndian.Uint16(data[4:6])
	cellSize0 := binary.LittleEndian.Uint16(data[6:8])
	if entries0 != 254 || cellSize0 != 4 {
		t.Errorf("cache 0: entries=%d cellSize=%d, want 254/4", entries0, cellSize0)
	}
	// Cache 9: 64 entries, 2048 bytes (offset 4 + 9*4 = 40)
	entries9 := binary.LittleEndian.Uint16(data[40:42])
	cellSize9 := binary.LittleEndian.Uint16(data[42:44])
	if entries9 != 64 || cellSize9 != 2048 {
		t.Errorf("cache 9: entries=%d cellSize=%d, want 64/2048", entries9, cellSize9)
	}
	// Fragment cache at offset 4 + 10*4 = 44: 256 entries, 256 bytes
	fragEntries := binary.LittleEndian.Uint16(data[44:46])
	fragSize := binary.LittleEndian.Uint16(data[46:48])
	if fragEntries != 256 || fragSize != 256 {
		t.Errorf("frag cache: entries=%d size=%d, want 256/256", fragEntries, fragSize)
	}
	// GlyphSupportLevel at offset 4 + 11*4 = 48
	glyphLevel := binary.LittleEndian.Uint16(data[48:50])
	if glyphLevel != 0x0002 {
		t.Errorf("GlyphSupportLevel = 0x%04X, want 0x0002", glyphLevel)
	}
}

func TestEncodePointerFields(t *testing.T) {
	data := EncodePointer()
	colorFlag := binary.LittleEndian.Uint16(data[4:6])
	if colorFlag != 1 {
		t.Errorf("colorPointerFlag = %d, want 1", colorFlag)
	}
	cacheSize := binary.LittleEndian.Uint16(data[6:8])
	if cacheSize != 20 {
		t.Errorf("colorPointerCacheSize = %d, want 20", cacheSize)
	}
	ptrCache := binary.LittleEndian.Uint16(data[8:10])
	if ptrCache != 20 {
		t.Errorf("pointerCacheSize = %d, want 20", ptrCache)
	}
}

func TestEncodeInputFlags(t *testing.T) {
	data := EncodeInput()
	flags := binary.LittleEndian.Uint16(data[4:6])
	if flags != 0x0001 {
		t.Errorf("inputFlags = 0x%04X, want 0x0001", flags)
	}
	kbLayout := binary.LittleEndian.Uint32(data[8:12])
	if kbLayout != 0x409 {
		t.Errorf("keyboardLayout = 0x%X, want 0x409", kbLayout)
	}
	kbType := binary.LittleEndian.Uint32(data[12:16])
	if kbType != 4 {
		t.Errorf("keyboardType = %d, want 4", kbType)
	}
	kbFunc := binary.LittleEndian.Uint32(data[20:24])
	if kbFunc != 12 {
		t.Errorf("keyboardFunctionKey = %d, want 12", kbFunc)
	}
}

func TestEncodeSoundFlags(t *testing.T) {
	data := EncodeSound()
	flags := binary.LittleEndian.Uint16(data[4:6])
	if flags != 1 {
		t.Errorf("soundFlags = %d, want 1", flags)
	}
}

func TestEncodeVirtualChannelFlags(t *testing.T) {
	data := EncodeVirtualChannel()
	flags := binary.LittleEndian.Uint32(data[4:8])
	if flags != 1 {
		t.Errorf("virtualChannel flags = %d, want 1", flags)
	}
}

func TestBuildConfirmCapabilities(t *testing.T) {
	// Server advertises all cap types — all conditional caps should be echoed.
	var allCaps uint32 = 0xFFFFFFFF
	data, count := BuildConfirmCapabilities(1024, 768, 24, true, allCaps)
	if count != 22 {
		t.Fatalf("count = %d, want 22", count)
	}

	// Parse them back to verify all types are present
	sets, err := DecodeCapabilitySets(slog.Default(), data, count)
	if err != nil {
		t.Fatalf("DecodeCapabilitySets: %v", err)
	}
	if len(sets) != 22 {
		t.Fatalf("decoded %d sets, want 22", len(sets))
	}

	wantTypes := map[uint16]bool{
		TypeGeneral: true, TypeBitmap: true, TypeOrder: true,
		TypeBitmapCacheV2: true, TypePointer: true, TypeInput: true,
		TypeBrush: true, TypeGlyphCache: true, TypeOffscreenBitmapCache: true,
		TypeVirtualChannel: true, TypeSound: true,
		TypeControl: true, TypeActivation: true, TypeShare: true,
		TypeColorCache: true, TypeFont: true, TypeMultifragUpdate: true,
		TypeLargePointer: true, TypeCompDesk: true, TypeSurfaceCommands: true,
		TypeBitmapCodecs: true, TypeFrameAcknowledge: true,
	}
	for _, s := range sets {
		if !wantTypes[s.Type] {
			t.Errorf("unexpected capability type 0x%04X", s.Type)
		}
		delete(wantTypes, s.Type)
	}
	for typ := range wantTypes {
		t.Errorf("missing capability type 0x%04X", typ)
	}
}

func TestBuildConfirmCapabilitiesNoGFX(t *testing.T) {
	// Server advertises all caps, but gfx=false — GFX caps should be absent.
	var allCaps uint32 = 0xFFFFFFFF
	data, count := BuildConfirmCapabilities(1024, 768, 24, false, allCaps)
	if count != 19 {
		t.Fatalf("count = %d, want 19", count)
	}

	sets, err := DecodeCapabilitySets(slog.Default(), data, count)
	if err != nil {
		t.Fatalf("DecodeCapabilitySets: %v", err)
	}
	if len(sets) != 19 {
		t.Fatalf("decoded %d sets, want 19", len(sets))
	}

	// GFX-specific caps should be absent
	for _, s := range sets {
		switch s.Type {
		case TypeSurfaceCommands, TypeBitmapCodecs, TypeFrameAcknowledge:
			t.Errorf("unexpected GFX capability type 0x%04X when gfx=false", s.Type)
		}
	}
}

func TestBuildConfirmCapabilitiesConditional(t *testing.T) {
	// Server doesn't advertise any conditional caps — 15 base caps only.
	data, count := BuildConfirmCapabilities(1024, 768, 24, true, 0)
	if count != 15 {
		t.Fatalf("count = %d, want 15", count)
	}

	sets, err := DecodeCapabilitySets(slog.Default(), data, count)
	if err != nil {
		t.Fatalf("DecodeCapabilitySets: %v", err)
	}

	// None of the conditional caps should be present
	for _, s := range sets {
		switch s.Type {
		case TypeMultifragUpdate, TypeLargePointer, TypeCompDesk,
			TypeOffscreenBitmapCache, TypeSurfaceCommands,
			TypeBitmapCodecs, TypeFrameAcknowledge:
			t.Errorf("unexpected conditional cap type 0x%04X when server didn't advertise", s.Type)
		}
	}
}

func TestDecodeCapabilitySetsRoundTrip(t *testing.T) {
	data, count := BuildConfirmCapabilities(800, 600, 16, true, 0xFFFFFFFF)
	sets, err := DecodeCapabilitySets(slog.Default(), data, count)
	if err != nil {
		t.Fatalf("DecodeCapabilitySets: %v", err)
	}

	// Re-encode and compare
	rebuilt := make([]byte, 0, len(data))
	for _, s := range sets {
		capLen := 4 + len(s.Payload)
		var hdr [4]byte
		binary.LittleEndian.PutUint16(hdr[0:2], s.Type)
		binary.LittleEndian.PutUint16(hdr[2:4], uint16(capLen))
		rebuilt = append(rebuilt, hdr[:]...)
		rebuilt = append(rebuilt, s.Payload...)
	}
	if len(rebuilt) != len(data) {
		t.Fatalf("round-trip length %d != original %d", len(rebuilt), len(data))
	}
	for i := range data {
		if rebuilt[i] != data[i] {
			t.Fatalf("round-trip mismatch at byte %d: got 0x%02X, want 0x%02X", i, rebuilt[i], data[i])
		}
	}
}

func TestEncodeBitmapCodecsFields(t *testing.T) {
	data := EncodeBitmapCodecs()
	if len(data) != 118 {
		t.Fatalf("length = %d, want 118", len(data))
	}
	// Header
	capType := binary.LittleEndian.Uint16(data[0:2])
	if capType != TypeBitmapCodecs {
		t.Errorf("type = 0x%04X, want 0x%04X", capType, TypeBitmapCodecs)
	}
	capLen := binary.LittleEndian.Uint16(data[2:4])
	if capLen != 118 {
		t.Errorf("encoded length = %d, want 118", capLen)
	}
	// Codec count
	if data[4] != 3 {
		t.Errorf("bitmapCodecCount = %d, want 3", data[4])
	}

	// Codec 1: NSCodec at offset 5
	nsGUID := []byte{0xB9, 0x1B, 0x8D, 0xCA, 0x0F, 0x00, 0x4F, 0x15, 0x58, 0x9F, 0xAE, 0x2D, 0x1A, 0x87, 0xE2, 0xD6}
	for i, b := range nsGUID {
		if data[5+i] != b {
			t.Errorf("NSCodec GUID[%d] = 0x%02X, want 0x%02X", i, data[5+i], b)
		}
	}
	if data[21] != 0x01 {
		t.Errorf("NSCodec codecID = 0x%02X, want 0x01", data[21])
	}
	nsPropLen := binary.LittleEndian.Uint16(data[22:24])
	if nsPropLen != 3 {
		t.Errorf("NSCodec propLen = %d, want 3", nsPropLen)
	}
	if data[24] != 1 {
		t.Errorf("fAllowDynamicFidelity = %d, want 1", data[24])
	}
	if data[25] != 1 {
		t.Errorf("fAllowSubsampling = %d, want 1", data[25])
	}
	if data[26] != 3 {
		t.Errorf("colorLossLevel = %d, want 3", data[26])
	}

	// Codec 2: RemoteFX at offset 27
	rfxGUID := []byte{0x12, 0x2F, 0x77, 0x76, 0x72, 0xBD, 0x63, 0x44, 0xAF, 0xB3, 0xB7, 0x3C, 0x9C, 0x6F, 0x78, 0x86}
	for i, b := range rfxGUID {
		if data[27+i] != b {
			t.Errorf("RFX GUID[%d] = 0x%02X, want 0x%02X", i, data[27+i], b)
		}
	}
	if data[43] != 0x03 {
		t.Errorf("RFX codecID = 0x%02X, want 0x03", data[43])
	}
	rfxPropLen := binary.LittleEndian.Uint16(data[44:46])
	if rfxPropLen != 49 {
		t.Errorf("RFX propLen = %d, want 49", rfxPropLen)
	}
	// Verify TS_RFX_CLNT_CAPS_CONTAINER fields
	rfxBase := 46 // start of container
	rfxContLen := binary.LittleEndian.Uint32(data[rfxBase : rfxBase+4])
	if rfxContLen != 49 {
		t.Errorf("RFX container length = %d, want 49", rfxContLen)
	}
	rfxCaptureFlags := binary.LittleEndian.Uint32(data[rfxBase+4 : rfxBase+8])
	if rfxCaptureFlags != 1 {
		t.Errorf("RFX captureFlags = %d, want 1", rfxCaptureFlags)
	}
	rfxCapsLen := binary.LittleEndian.Uint32(data[rfxBase+8 : rfxBase+12])
	if rfxCapsLen != 37 {
		t.Errorf("RFX capsLength = %d, want 37", rfxCapsLen)
	}
	rfxCapsType := binary.LittleEndian.Uint16(data[rfxBase+12 : rfxBase+14])
	if rfxCapsType != 0xCBC0 {
		t.Errorf("RFX CBY_CAPS = 0x%04X, want 0xCBC0", rfxCapsType)
	}
	rfxNumIcaps := binary.LittleEndian.Uint16(data[rfxBase+29 : rfxBase+31])
	if rfxNumIcaps != 2 {
		t.Errorf("RFX numIcaps = %d, want 2", rfxNumIcaps)
	}
	// ICAP 1 entropy = RLGR1
	if data[rfxBase+40] != 0x01 {
		t.Errorf("RFX ICAP1 entropyBits = 0x%02X, want 0x01", data[rfxBase+40])
	}
	// ICAP 2 entropy = RLGR3
	if data[rfxBase+48] != 0x04 {
		t.Errorf("RFX ICAP2 entropyBits = 0x%02X, want 0x04", data[rfxBase+48])
	}

	// Codec 3: RemoteFX Image at offset 95 (27 + 68)
	rfxImgBase := 95
	rfxImgGUID := []byte{0xA6, 0x51, 0x43, 0x9C, 0x35, 0x35, 0xAE, 0x42, 0x91, 0x0C, 0xCD, 0xFC, 0xE5, 0x76, 0x0B, 0x58}
	for i, b := range rfxImgGUID {
		if data[rfxImgBase+i] != b {
			t.Errorf("RFX Image GUID[%d] = 0x%02X, want 0x%02X", i, data[rfxImgBase+i], b)
		}
	}
	if data[rfxImgBase+16] != 0x00 {
		t.Errorf("RFX Image codecID = 0x%02X, want 0x00", data[rfxImgBase+16])
	}
	rfxImgPropLen := binary.LittleEndian.Uint16(data[rfxImgBase+17 : rfxImgBase+19])
	if rfxImgPropLen != 4 {
		t.Errorf("RFX Image propLen = %d, want 4", rfxImgPropLen)
	}
}

func TestDecodeCapabilitySetsPartial(t *testing.T) {
	// Truncated data — should parse what it can
	data, _ := BuildConfirmCapabilities(1024, 768, 24, true, 0xFFFFFFFF)
	sets, err := DecodeCapabilitySets(slog.Default(), data[:10], 11)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sets) != 0 {
		t.Errorf("expected 0 parsed sets from truncated data, got %d", len(sets))
	}
}
