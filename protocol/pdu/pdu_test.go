package pdu

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestDecodeShareControlHeader(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantLen uint16
		wantTyp uint16
		wantSrc uint16
		wantErr bool
	}{
		{
			name:    "DemandActive",
			data:    leU16(100, TypeDemandActive, 1003),
			wantLen: 100,
			wantTyp: TypeDemandActive,
			wantSrc: 1003,
		},
		{
			name:    "ConfirmActive",
			data:    leU16(50, TypeConfirmActive, 1004),
			wantLen: 50,
			wantTyp: TypeConfirmActive,
			wantSrc: 1004,
		},
		{
			name:    "Data",
			data:    leU16(20, TypeData, 1003),
			wantLen: 20,
			wantTyp: TypeData,
			wantSrc: 1003,
		},
		{
			name:    "TooShort",
			data:    []byte{0x01, 0x02, 0x03},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr, rest, err := DecodeShareControlHeader(slog.Default(), tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hdr.TotalLength != tt.wantLen {
				t.Errorf("totalLength = %d, want %d", hdr.TotalLength, tt.wantLen)
			}
			if hdr.PDUType != tt.wantTyp {
				t.Errorf("pduType = 0x%04X, want 0x%04X", hdr.PDUType, tt.wantTyp)
			}
			if hdr.PDUSource != tt.wantSrc {
				t.Errorf("pduSource = %d, want %d", hdr.PDUSource, tt.wantSrc)
			}
			if len(rest) != len(tt.data)-6 {
				t.Errorf("remaining = %d bytes, want %d", len(rest), len(tt.data)-6)
			}
		})
	}
}

func TestDecodeDemandActive(t *testing.T) {
	// Build a Demand Active payload:
	// shareID(4) + srcDescLen(2) + combinedCapsLen(2) + srcDesc(4) + numCaps(2) + pad(2) + caps(8) + sessionID(4)
	srcDesc := []byte("RDP\x00")
	fakeCap := make([]byte, 8)
	binary.LittleEndian.PutUint16(fakeCap[0:2], 0x0001)
	binary.LittleEndian.PutUint16(fakeCap[2:4], 8)

	payload := make([]byte, 0, 28)
	payload = appendU32(payload, 0xDEADBEEF) // shareID
	payload = appendU16(payload, uint16(len(srcDesc)))
	payload = appendU16(payload, uint16(len(fakeCap)))
	payload = append(payload, srcDesc...)
	payload = appendU16(payload, 1) // numberCapabilities
	payload = appendU16(payload, 0) // pad
	payload = append(payload, fakeCap...)
	payload = appendU32(payload, 0x12345678) // sessionID

	da, err := DecodeDemandActive(slog.Default(), payload)
	if err != nil {
		t.Fatalf("DecodeDemandActive: %v", err)
	}
	if da.ShareID != 0xDEADBEEF {
		t.Errorf("shareID = 0x%08X, want 0xDEADBEEF", da.ShareID)
	}
	if string(da.SourceDescriptor) != "RDP\x00" {
		t.Errorf("sourceDescriptor = %q, want %q", da.SourceDescriptor, "RDP\x00")
	}
	if da.NumberCapabilities != 1 {
		t.Errorf("numberCapabilities = %d, want 1", da.NumberCapabilities)
	}
	if len(da.CapabilitySets) != 8 {
		t.Errorf("capabilitySets len = %d, want 8", len(da.CapabilitySets))
	}
	if da.SessionID != 0x12345678 {
		t.Errorf("sessionID = 0x%08X, want 0x12345678", da.SessionID)
	}
}

func TestDecodeDemandActiveTruncated(t *testing.T) {
	_, err := DecodeDemandActive(slog.Default(), []byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestEncodeConfirmActive(t *testing.T) {
	srcDesc := []byte("MSTSC\x00")
	caps := make([]byte, 16)
	binary.LittleEndian.PutUint16(caps[0:2], 0x0001)
	binary.LittleEndian.PutUint16(caps[2:4], 16)

	ca := &ConfirmActive{
		ShareID:            0xCAFEBABE,
		SourceDescriptor:   srcDesc,
		NumberCapabilities: 1,
		CapabilitySets:     caps,
	}
	data := EncodeConfirmActive(ca, 1005)

	// Total length = 6 + 4 + 2(originatorId) + 2 + 2 + 6 + 2 + 2 + 16 = 42
	wantLen := 42
	if len(data) != wantLen {
		t.Fatalf("len = %d, want %d", len(data), wantLen)
	}

	// Verify Share Control Header
	totalLen := binary.LittleEndian.Uint16(data[0:2])
	if int(totalLen) != wantLen {
		t.Errorf("totalLength = %d, want %d", totalLen, wantLen)
	}
	pduType := binary.LittleEndian.Uint16(data[2:4])
	if pduType != TypeConfirmActive {
		t.Errorf("pduType = 0x%04X, want 0x%04X", pduType, TypeConfirmActive)
	}
	pduSource := binary.LittleEndian.Uint16(data[4:6])
	if pduSource != 1005 {
		t.Errorf("pduSource = %d, want 1005", pduSource)
	}

	// Verify shareID
	shareID := binary.LittleEndian.Uint32(data[6:10])
	if shareID != 0xCAFEBABE {
		t.Errorf("shareID = 0x%08X, want 0xCAFEBABE", shareID)
	}

	// Verify originatorId = 0x03EA
	originatorID := binary.LittleEndian.Uint16(data[10:12])
	if originatorID != 0x03EA {
		t.Errorf("originatorId = 0x%04X, want 0x03EA", originatorID)
	}

	// Verify sourceDescriptorLen
	sdLen := binary.LittleEndian.Uint16(data[12:14])
	if sdLen != 6 {
		t.Errorf("sourceDescriptorLen = %d, want 6", sdLen)
	}

	// Verify combinedCapsLen = numCaps(2) + pad(2) + caps(16) = 20
	ccLen := binary.LittleEndian.Uint16(data[14:16])
	if ccLen != 20 {
		t.Errorf("combinedCapsLen = %d, want 20", ccLen)
	}

	// Verify numberCapabilities (after srcDesc at offset 16+6=22)
	numCaps := binary.LittleEndian.Uint16(data[22:24])
	if numCaps != 1 {
		t.Errorf("numberCapabilities = %d, want 1", numCaps)
	}
}

func TestEncodeShareDataPDU(t *testing.T) {
	payload := []byte{0xAA, 0xBB, 0xCC}
	data := EncodeShareDataPDU(0xDEADBEEF, PDUType2Synchronize, 1003, payload)

	wantLen := 18 + 3
	if len(data) != wantLen {
		t.Fatalf("len = %d, want %d", len(data), wantLen)
	}

	// Share Control Header
	totalLen := binary.LittleEndian.Uint16(data[0:2])
	if int(totalLen) != wantLen {
		t.Errorf("totalLength = %d, want %d", totalLen, wantLen)
	}
	pduType := binary.LittleEndian.Uint16(data[2:4])
	if pduType != TypeData {
		t.Errorf("pduType = 0x%04X, want 0x%04X", pduType, TypeData)
	}
	pduSource := binary.LittleEndian.Uint16(data[4:6])
	if pduSource != 1003 {
		t.Errorf("pduSource = %d, want 1003", pduSource)
	}

	// Share Data Header
	shareID := binary.LittleEndian.Uint32(data[6:10])
	if shareID != 0xDEADBEEF {
		t.Errorf("shareID = 0x%08X, want 0xDEADBEEF", shareID)
	}
	if data[10] != 0 {
		t.Errorf("pad1 = %d, want 0", data[10])
	}
	if data[11] != StreamLow {
		t.Errorf("streamID = %d, want %d", data[11], StreamLow)
	}
	uncompLen := binary.LittleEndian.Uint16(data[12:14])
	if uncompLen != 3 {
		t.Errorf("uncompressedLength = %d, want 3", uncompLen)
	}
	if data[14] != PDUType2Synchronize {
		t.Errorf("pduType2 = %d, want %d", data[14], PDUType2Synchronize)
	}
	if data[15] != 0 {
		t.Errorf("compressedType = %d, want 0", data[15])
	}
	compLen := binary.LittleEndian.Uint16(data[16:18])
	if compLen != 0 {
		t.Errorf("compressedLength = %d, want 0", compLen)
	}

	// Payload
	if data[18] != 0xAA || data[19] != 0xBB || data[20] != 0xCC {
		t.Errorf("payload = %X, want AABBCC", data[18:21])
	}
}

func TestDecodeShareDataHeader(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantID     uint32
		wantStream uint8
		wantType2  uint8
		wantErr    bool
	}{
		{
			name: "Valid",
			data: func() []byte {
				buf := make([]byte, 15) // 12 header + 3 payload
				binary.LittleEndian.PutUint32(buf[0:4], 0xCAFEBABE)
				buf[5] = StreamLow
				binary.LittleEndian.PutUint16(buf[6:8], 3)
				buf[8] = PDUType2Control
				buf[12] = 0xDD
				buf[13] = 0xEE
				buf[14] = 0xFF
				return buf
			}(),
			wantID:     0xCAFEBABE,
			wantStream: StreamLow,
			wantType2:  PDUType2Control,
		},
		{
			name:    "TooShort",
			data:    []byte{0x01, 0x02, 0x03},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr, rest, err := DecodeShareDataHeader(slog.Default(), tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hdr.ShareID != tt.wantID {
				t.Errorf("shareID = 0x%08X, want 0x%08X", hdr.ShareID, tt.wantID)
			}
			if hdr.StreamID != tt.wantStream {
				t.Errorf("streamID = %d, want %d", hdr.StreamID, tt.wantStream)
			}
			if hdr.PDUType2 != tt.wantType2 {
				t.Errorf("pduType2 = %d, want %d", hdr.PDUType2, tt.wantType2)
			}
			if len(rest) != len(tt.data)-12 {
				t.Errorf("remaining = %d bytes, want %d", len(rest), len(tt.data)-12)
			}
		})
	}
}

func TestEncodeSynchronize(t *testing.T) {
	data := EncodeSynchronize(1003)
	if len(data) != 4 {
		t.Fatalf("len = %d, want 4", len(data))
	}
	msgType := binary.LittleEndian.Uint16(data[0:2])
	if msgType != 1 {
		t.Errorf("messageType = %d, want 1", msgType)
	}
	target := binary.LittleEndian.Uint16(data[2:4])
	if target != 1003 {
		t.Errorf("targetUser = %d, want 1003", target)
	}
}

func TestEncodeControl(t *testing.T) {
	tests := []struct {
		name       string
		action     uint16
		wantAction uint16
	}{
		{"Cooperate", ControlCooperate, 0x0004},
		{"RequestControl", ControlRequestControl, 0x0001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := EncodeControl(tt.action)
			if len(data) != 8 {
				t.Fatalf("len = %d, want 8", len(data))
			}
			action := binary.LittleEndian.Uint16(data[0:2])
			if action != tt.wantAction {
				t.Errorf("action = 0x%04X, want 0x%04X", action, tt.wantAction)
			}
			grantID := binary.LittleEndian.Uint16(data[2:4])
			if grantID != 0 {
				t.Errorf("grantID = %d, want 0", grantID)
			}
			controlID := binary.LittleEndian.Uint32(data[4:8])
			if controlID != 0 {
				t.Errorf("controlID = %d, want 0", controlID)
			}
		})
	}
}

func TestEncodeFontList(t *testing.T) {
	data := EncodeFontList()
	if len(data) != 8 {
		t.Fatalf("len = %d, want 8", len(data))
	}
	numFonts := binary.LittleEndian.Uint16(data[0:2])
	if numFonts != 0 {
		t.Errorf("numberFonts = %d, want 0", numFonts)
	}
	totalFonts := binary.LittleEndian.Uint16(data[2:4])
	if totalFonts != 0 {
		t.Errorf("totalNumFonts = %d, want 0", totalFonts)
	}
	listFlags := binary.LittleEndian.Uint16(data[4:6])
	if listFlags != 0x0003 {
		t.Errorf("listFlags = 0x%04X, want 0x0003", listFlags)
	}
	entrySize := binary.LittleEndian.Uint16(data[6:8])
	if entrySize != 0x0032 {
		t.Errorf("entrySize = 0x%04X, want 0x0032", entrySize)
	}
}

func TestDecodeBitmapUpdateData(t *testing.T) {
	// buildRect builds a TS_BITMAP_DATA entry with given fields and bitmap payload.
	buildRect := func(destLeft, destTop, destRight, destBottom, width, height, bpp, flags uint16, bmpData []byte) []byte {
		buf := make([]byte, 18+len(bmpData))
		binary.LittleEndian.PutUint16(buf[0:2], destLeft)
		binary.LittleEndian.PutUint16(buf[2:4], destTop)
		binary.LittleEndian.PutUint16(buf[4:6], destRight)
		binary.LittleEndian.PutUint16(buf[6:8], destBottom)
		binary.LittleEndian.PutUint16(buf[8:10], width)
		binary.LittleEndian.PutUint16(buf[10:12], height)
		binary.LittleEndian.PutUint16(buf[12:14], bpp)
		binary.LittleEndian.PutUint16(buf[14:16], flags)
		binary.LittleEndian.PutUint16(buf[16:18], uint16(len(bmpData)))
		copy(buf[18:], bmpData)
		return buf
	}

	tests := []struct {
		name      string
		data      []byte
		wantRects int
		wantErr   bool
	}{
		{
			name: "OneRectangle",
			data: func() []byte {
				rect := buildRect(0, 0, 63, 63, 64, 64, 16, BitmapCompression, []byte{0xAA, 0xBB})
				hdr := make([]byte, 4)
				binary.LittleEndian.PutUint16(hdr[0:2], UpdateBitmap)
				binary.LittleEndian.PutUint16(hdr[2:4], 1) // numRects
				return append(hdr, rect...)
			}(),
			wantRects: 1,
		},
		{
			name: "TwoRectangles",
			data: func() []byte {
				r1 := buildRect(0, 0, 31, 31, 32, 32, 16, 0, []byte{0x01, 0x02, 0x03})
				r2 := buildRect(32, 0, 63, 31, 32, 32, 24, BitmapCompression, []byte{0x04, 0x05})
				hdr := make([]byte, 4)
				binary.LittleEndian.PutUint16(hdr[0:2], UpdateBitmap)
				binary.LittleEndian.PutUint16(hdr[2:4], 2)
				out := append(hdr, r1...)
				return append(out, r2...)
			}(),
			wantRects: 2,
		},
		{
			name:    "TooShort",
			data:    []byte{0x01, 0x00},
			wantErr: true,
		},
		{
			name: "TruncatedBitmapData",
			data: func() []byte {
				// Claim 10 bytes of bitmap data but only provide 2
				hdr := make([]byte, 4)
				binary.LittleEndian.PutUint16(hdr[0:2], UpdateBitmap)
				binary.LittleEndian.PutUint16(hdr[2:4], 1)
				rect := make([]byte, 18+2) // header + only 2 bytes of data
				binary.LittleEndian.PutUint16(rect[16:18], 10) // claim 10 bytes
				rect[18] = 0xFF
				rect[19] = 0xFE
				return append(hdr, rect...)
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rects, err := DecodeBitmapUpdateData(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rects) != tt.wantRects {
				t.Fatalf("got %d rects, want %d", len(rects), tt.wantRects)
			}
		})
	}

	// Verify first rectangle fields from "OneRectangle" case
	t.Run("OneRectangleFields", func(t *testing.T) {
		rect := buildRect(10, 20, 73, 83, 64, 64, 16, BitmapCompression, []byte{0xDE, 0xAD})
		hdr := make([]byte, 4)
		binary.LittleEndian.PutUint16(hdr[0:2], UpdateBitmap)
		binary.LittleEndian.PutUint16(hdr[2:4], 1)
		data := append(hdr, rect...)

		rects, err := DecodeBitmapUpdateData(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := rects[0]
		if r.DestLeft != 10 {
			t.Errorf("DestLeft = %d, want 10", r.DestLeft)
		}
		if r.DestTop != 20 {
			t.Errorf("DestTop = %d, want 20", r.DestTop)
		}
		if r.DestRight != 73 {
			t.Errorf("DestRight = %d, want 73", r.DestRight)
		}
		if r.DestBottom != 83 {
			t.Errorf("DestBottom = %d, want 83", r.DestBottom)
		}
		if r.Width != 64 {
			t.Errorf("Width = %d, want 64", r.Width)
		}
		if r.Height != 64 {
			t.Errorf("Height = %d, want 64", r.Height)
		}
		if r.BitsPerPixel != 16 {
			t.Errorf("BitsPerPixel = %d, want 16", r.BitsPerPixel)
		}
		if r.Flags != BitmapCompression {
			t.Errorf("Flags = 0x%04X, want 0x%04X", r.Flags, BitmapCompression)
		}
		if len(r.Data) != 2 || r.Data[0] != 0xDE || r.Data[1] != 0xAD {
			t.Errorf("Data = %X, want DEAD", r.Data)
		}
	})
}

func TestDecodeFastPathBitmapUpdate(t *testing.T) {
	buildRect := func(destLeft, destTop, destRight, destBottom, width, height, bpp, flags uint16, bmpData []byte) []byte {
		buf := make([]byte, 18+len(bmpData))
		binary.LittleEndian.PutUint16(buf[0:2], destLeft)
		binary.LittleEndian.PutUint16(buf[2:4], destTop)
		binary.LittleEndian.PutUint16(buf[4:6], destRight)
		binary.LittleEndian.PutUint16(buf[6:8], destBottom)
		binary.LittleEndian.PutUint16(buf[8:10], width)
		binary.LittleEndian.PutUint16(buf[10:12], height)
		binary.LittleEndian.PutUint16(buf[12:14], bpp)
		binary.LittleEndian.PutUint16(buf[14:16], flags)
		binary.LittleEndian.PutUint16(buf[16:18], uint16(len(bmpData)))
		copy(buf[18:], bmpData)
		return buf
	}

	t.Run("OneRect", func(t *testing.T) {
		rect := buildRect(10, 20, 73, 83, 64, 64, 16, BitmapCompression, []byte{0xDE, 0xAD})
		// Fast-path bitmapUpdateData is a TS_UPDATE_BITMAP_DATA:
		// updateType(u16) + numRects(u16) + rect data
		var data []byte
		data = appendU16(nil, 0x0001) // updateType = UPDATETYPE_BITMAP
		data = appendU16(data, 1)     // numRects
		data = append(data, rect...)

		rects, err := DecodeFastPathBitmapUpdate(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rects) != 1 {
			t.Fatalf("got %d rects, want 1", len(rects))
		}
		r := rects[0]
		if r.DestLeft != 10 || r.DestTop != 20 || r.DestRight != 73 || r.DestBottom != 83 {
			t.Errorf("rect bounds = (%d,%d,%d,%d), want (10,20,73,83)",
				r.DestLeft, r.DestTop, r.DestRight, r.DestBottom)
		}
		if r.Width != 64 || r.Height != 64 {
			t.Errorf("dimensions = %dx%d, want 64x64", r.Width, r.Height)
		}
		if r.BitsPerPixel != 16 {
			t.Errorf("bpp = %d, want 16", r.BitsPerPixel)
		}
		if r.Flags != BitmapCompression {
			t.Errorf("flags = 0x%04X, want 0x%04X", r.Flags, BitmapCompression)
		}
		if len(r.Data) != 2 || r.Data[0] != 0xDE || r.Data[1] != 0xAD {
			t.Errorf("data = %X, want DEAD", r.Data)
		}
	})

	t.Run("TwoRects", func(t *testing.T) {
		r1 := buildRect(0, 0, 31, 31, 32, 32, 16, 0, []byte{0x01, 0x02})
		r2 := buildRect(32, 0, 63, 31, 32, 32, 24, BitmapCompression, []byte{0x03})
		var data []byte
		data = appendU16(nil, 0x0001) // updateType
		data = appendU16(data, 2)
		data = append(data, r1...)
		data = append(data, r2...)

		rects, err := DecodeFastPathBitmapUpdate(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rects) != 2 {
			t.Fatalf("got %d rects, want 2", len(rects))
		}
		if rects[0].Width != 32 || rects[1].BitsPerPixel != 24 {
			t.Errorf("rect fields mismatch")
		}
	})

	t.Run("Truncated", func(t *testing.T) {
		_, err := DecodeFastPathBitmapUpdate([]byte{0x01, 0x00, 0x01})
		if err == nil {
			t.Fatal("expected error for truncated data")
		}
	})

	t.Run("TruncatedRect", func(t *testing.T) {
		// updateType + numRects=1 but no rect data
		var data []byte
		data = appendU16(nil, 0x0001) // updateType
		data = appendU16(data, 1)     // numRects
		_, err := DecodeFastPathBitmapUpdate(data)
		if err == nil {
			t.Fatal("expected error for truncated rect data")
		}
	})
}

func TestEncodeInputPDU(t *testing.T) {
	events := []byte{0x01, 0x02, 0x03, 0x04}
	data := EncodeInputPDU(events, 2)

	if len(data) != 8 {
		t.Fatalf("len = %d, want 8", len(data))
	}
	numEvents := binary.LittleEndian.Uint16(data[0:2])
	if numEvents != 2 {
		t.Errorf("numEvents = %d, want 2", numEvents)
	}
	pad := binary.LittleEndian.Uint16(data[2:4])
	if pad != 0 {
		t.Errorf("pad = %d, want 0", pad)
	}
	if data[4] != 0x01 || data[5] != 0x02 || data[6] != 0x03 || data[7] != 0x04 {
		t.Errorf("events = %X, want 01020304", data[4:8])
	}
}

func TestEncodeScancodeEvent(t *testing.T) {
	data := EncodeScancodeEvent(0x1E, KbdFlagsRelease|KbdFlagsExtended)

	if len(data) != 12 {
		t.Fatalf("len = %d, want 12", len(data))
	}
	eventTime := binary.LittleEndian.Uint32(data[0:4])
	if eventTime != 0 {
		t.Errorf("eventTime = %d, want 0", eventTime)
	}
	msgType := binary.LittleEndian.Uint16(data[4:6])
	if msgType != InputScancode {
		t.Errorf("messageType = 0x%04X, want 0x%04X", msgType, InputScancode)
	}
	flags := binary.LittleEndian.Uint16(data[6:8])
	wantFlags := KbdFlagsRelease | KbdFlagsExtended
	if flags != wantFlags {
		t.Errorf("flags = 0x%04X, want 0x%04X", flags, wantFlags)
	}
	keyCode := binary.LittleEndian.Uint16(data[8:10])
	if keyCode != 0x1E {
		t.Errorf("keyCode = 0x%04X, want 0x001E", keyCode)
	}
	pad := binary.LittleEndian.Uint16(data[10:12])
	if pad != 0 {
		t.Errorf("pad = %d, want 0", pad)
	}
}

func TestEncodeMouseEvent(t *testing.T) {
	data := EncodeMouseEvent(PtrFlagsMove|PtrFlagsButton1|PtrFlagsDown, 320, 240)

	if len(data) != 12 {
		t.Fatalf("len = %d, want 12", len(data))
	}
	eventTime := binary.LittleEndian.Uint32(data[0:4])
	if eventTime != 0 {
		t.Errorf("eventTime = %d, want 0", eventTime)
	}
	msgType := binary.LittleEndian.Uint16(data[4:6])
	if msgType != InputMouse {
		t.Errorf("messageType = 0x%04X, want 0x%04X", msgType, InputMouse)
	}
	flags := binary.LittleEndian.Uint16(data[6:8])
	wantFlags := PtrFlagsMove | PtrFlagsButton1 | PtrFlagsDown
	if flags != wantFlags {
		t.Errorf("flags = 0x%04X, want 0x%04X", flags, wantFlags)
	}
	xPos := binary.LittleEndian.Uint16(data[8:10])
	if xPos != 320 {
		t.Errorf("xPos = %d, want 320", xPos)
	}
	yPos := binary.LittleEndian.Uint16(data[10:12])
	if yPos != 240 {
		t.Errorf("yPos = %d, want 240", yPos)
	}
}

// Helper: build 6-byte data from three little-endian uint16 values
func leU16(a, b, c uint16) []byte {
	buf := make([]byte, 6)
	binary.LittleEndian.PutUint16(buf[0:2], a)
	binary.LittleEndian.PutUint16(buf[2:4], b)
	binary.LittleEndian.PutUint16(buf[4:6], c)
	return buf
}

func appendU16(b []byte, v uint16) []byte {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], v)
	return append(b, buf[:]...)
}

func appendU32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}
