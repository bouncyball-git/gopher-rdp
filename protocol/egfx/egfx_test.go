package egfx

import (
	"encoding/binary"
	"log/slog"
	"testing"
	"time"
)

func TestSendCapsAdvertise(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	if err := h.SendCapsAdvertise(); err != nil {
		t.Fatalf("SendCapsAdvertise error: %v", err)
	}

	// 9 capsets: v10.7..v10.0 (AVC_DISABLED) + v8.1 + v8.0 (flags=0)
	const numCaps = 9
	const wantLen = 8 + 2 + numCaps*12 // 118
	if len(sent) != wantLen {
		t.Fatalf("sent %d bytes, want %d", len(sent), wantLen)
	}

	cmdId := binary.LittleEndian.Uint16(sent[0:2])
	if cmdId != CmdCapsAdvertise {
		t.Fatalf("cmdId = 0x%04X, want 0x%04X", cmdId, CmdCapsAdvertise)
	}

	pduLen := binary.LittleEndian.Uint32(sent[4:8])
	if pduLen != wantLen {
		t.Fatalf("pduLength = %d, want %d", pduLen, wantLen)
	}

	capsCount := binary.LittleEndian.Uint16(sent[8:10])
	if capsCount != numCaps {
		t.Fatalf("capsSetCount = %d, want %d", capsCount, numCaps)
	}

	// Verify each capset
	wantCaps := []struct {
		ver   uint32
		flags uint32
	}{
		{CapVersion107, CapsFlagAVCDisabled},
		{CapVersion106, CapsFlagAVCDisabled},
		{CapVersion105, CapsFlagAVCDisabled},
		{CapVersion104, CapsFlagAVCDisabled},
		{CapVersion103, CapsFlagAVCDisabled},
		{CapVersion102, CapsFlagAVCDisabled},
		{CapVersion10, CapsFlagAVCDisabled},
		{CapVersion81, 0},
		{CapVersion8, 0},
	}
	off := 10
	for i, want := range wantCaps {
		ver := binary.LittleEndian.Uint32(sent[off:])
		dataLen := binary.LittleEndian.Uint32(sent[off+4:])
		flags := binary.LittleEndian.Uint32(sent[off+8:])
		if ver != want.ver {
			t.Fatalf("cap[%d] version = 0x%08X, want 0x%08X", i, ver, want.ver)
		}
		if dataLen != 4 {
			t.Fatalf("cap[%d] capsDataLength = %d, want 4", i, dataLen)
		}
		if flags != want.flags {
			t.Fatalf("cap[%d] flags = 0x%08X, want 0x%08X", i, flags, want.flags)
		}
		off += 12
	}
}

func TestCreateAndDeleteSurface(t *testing.T) {
	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))

	// Create surface
	data := make([]byte, 7)
	binary.LittleEndian.PutUint16(data[0:2], 1)   // surfaceId
	binary.LittleEndian.PutUint16(data[2:4], 100)  // width
	binary.LittleEndian.PutUint16(data[4:6], 200)  // height
	data[6] = PixelFormatXRGB8888

	h.handleCreateSurface(data)

	surf := h.surfaces[1]
	if surf == nil {
		t.Fatal("surface not created")
	}
	if surf.Width != 100 || surf.Height != 200 {
		t.Fatalf("surface size = %dx%d, want 100x200", surf.Width, surf.Height)
	}
	if len(surf.Data) != 100*200*4 {
		t.Fatalf("surface data size = %d, want %d", len(surf.Data), 100*200*4)
	}

	// Map to output
	mapData := make([]byte, 12)
	binary.LittleEndian.PutUint16(mapData[0:2], 1)  // surfaceId
	binary.LittleEndian.PutUint32(mapData[4:8], 10)  // outputOriginX
	binary.LittleEndian.PutUint32(mapData[8:12], 20) // outputOriginY

	h.handleMapSurfaceToOutput(mapData)

	origin, ok := h.outputMap[1]
	if !ok {
		t.Fatal("output map not set")
	}
	if origin[0] != 10 || origin[1] != 20 {
		t.Fatalf("origin = (%d, %d), want (10, 20)", origin[0], origin[1])
	}

	// Delete surface
	delData := make([]byte, 2)
	binary.LittleEndian.PutUint16(delData[0:2], 1)
	h.handleDeleteSurface(delData)

	if _, ok := h.surfaces[1]; ok {
		t.Fatal("surface not deleted")
	}
	if _, ok := h.outputMap[1]; ok {
		t.Fatal("output map not cleared")
	}
}

func TestFrameAcknowledge(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	// Start frame
	startData := make([]byte, 8)
	binary.LittleEndian.PutUint32(startData[0:4], 0)  // timestamp
	binary.LittleEndian.PutUint32(startData[4:8], 42)  // frameId
	h.handleStartFrame(startData)

	// End frame — ACK is sent asynchronously via ackCh goroutine
	endData := make([]byte, 4)
	binary.LittleEndian.PutUint32(endData[0:4], 42) // frameId
	h.handleEndFrame(endData)
	time.Sleep(50 * time.Millisecond)

	if len(sent) != 20 {
		t.Fatalf("frame ack = %d bytes, want 20", len(sent))
	}

	cmdId := binary.LittleEndian.Uint16(sent[0:2])
	if cmdId != CmdFrameAcknowledge {
		t.Fatalf("cmdId = 0x%04X, want 0x%04X", cmdId, CmdFrameAcknowledge)
	}

	queueDepth := binary.LittleEndian.Uint32(sent[8:12])
	if queueDepth != 0xFFFFFFFF { // QUEUE_DEPTH_UNAVAILABLE
		t.Fatalf("queueDepth = %d, want 0xFFFFFFFF (QUEUE_DEPTH_UNAVAILABLE)", queueDepth)
	}

	frameId := binary.LittleEndian.Uint32(sent[12:16])
	if frameId != 42 {
		t.Fatalf("frameId = %d, want 42", frameId)
	}

	totalDecoded := binary.LittleEndian.Uint32(sent[16:20])
	if totalDecoded != 1 {
		t.Fatalf("totalDecoded = %d, want 1", totalDecoded)
	}
}

func TestSolidFill(t *testing.T) {
	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))

	// Create a 10x10 surface
	createData := make([]byte, 7)
	binary.LittleEndian.PutUint16(createData[0:2], 1)
	binary.LittleEndian.PutUint16(createData[2:4], 10)
	binary.LittleEndian.PutUint16(createData[4:6], 10)
	createData[6] = PixelFormatXRGB8888
	h.handleCreateSurface(createData)

	// Solid fill: blue(0xFF) green(0x80) red(0x00) alpha(0xFF), rect 2,3→5,7
	fillData := make([]byte, 16)
	binary.LittleEndian.PutUint16(fillData[0:2], 1) // surfaceId
	fillData[2] = 0xFF // B
	fillData[3] = 0x80 // G
	fillData[4] = 0x00 // R
	fillData[5] = 0xFF // A
	binary.LittleEndian.PutUint16(fillData[6:8], 1) // rectCount
	// rect: left=2, top=3, right=5, bottom=7
	binary.LittleEndian.PutUint16(fillData[8:10], 2)
	binary.LittleEndian.PutUint16(fillData[10:12], 3)
	binary.LittleEndian.PutUint16(fillData[12:14], 5)
	binary.LittleEndian.PutUint16(fillData[14:16], 7)

	h.handleSolidFill(fillData)

	surf := h.surfaces[1]
	// Check a pixel at (3, 4) — should be filled
	stride := 10 * 4
	off := 4*stride + 3*4
	// Wire: B=0xFF, G=0x80, R=0x00 → RGBA surface: R=0x00, G=0x80, B=0xFF
	if surf.Data[off] != 0x00 || surf.Data[off+1] != 0x80 || surf.Data[off+2] != 0xFF {
		t.Fatalf("pixel at (3,4) = (%d,%d,%d), want (0,128,255)",
			surf.Data[off], surf.Data[off+1], surf.Data[off+2])
	}

	// Check a pixel at (0, 0) — should be opaque white (surfaces init to 0xFF).
	if surf.Data[0] != 0xFF || surf.Data[1] != 0xFF || surf.Data[2] != 0xFF || surf.Data[3] != 0xFF {
		t.Fatalf("pixel at (0,0) should be opaque white, got (%d,%d,%d,%d)",
			surf.Data[0], surf.Data[1], surf.Data[2], surf.Data[3])
	}
}

func TestResetGraphics(t *testing.T) {
	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))

	// Create a surface first
	createData := make([]byte, 7)
	binary.LittleEndian.PutUint16(createData[0:2], 1)
	binary.LittleEndian.PutUint16(createData[2:4], 100)
	binary.LittleEndian.PutUint16(createData[4:6], 100)
	createData[6] = PixelFormatXRGB8888
	h.handleCreateSurface(createData)

	var resetW, resetH int
	h.OnResetGraphics(func(w, hh int) {
		resetW = w
		resetH = hh
	})

	resetData := make([]byte, 12)
	binary.LittleEndian.PutUint32(resetData[0:4], 1920)
	binary.LittleEndian.PutUint32(resetData[4:8], 1080)
	binary.LittleEndian.PutUint32(resetData[8:12], 1) // monitorCount
	h.handleResetGraphics(resetData)

	if resetW != 1920 || resetH != 1080 {
		t.Fatalf("reset dimensions = %dx%d, want 1920x1080", resetW, resetH)
	}

	if len(h.surfaces) != 0 {
		t.Fatalf("surfaces not cleared after reset: %d remain", len(h.surfaces))
	}
}

func TestSurfaceToSurfaceOverlap(t *testing.T) {
	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))

	// Create a 10x1 surface with pixels [0,1,2,3,4,5,6,7,8,9]
	createData := make([]byte, 7)
	binary.LittleEndian.PutUint16(createData[0:2], 1)
	binary.LittleEndian.PutUint16(createData[2:4], 10)
	binary.LittleEndian.PutUint16(createData[4:6], 1)
	createData[6] = PixelFormatXRGB8888
	h.handleCreateSurface(createData)

	surf := h.surfaces[1]
	for i := 0; i < 10; i++ {
		surf.Data[i*4] = byte(i)
		surf.Data[i*4+1] = byte(i)
		surf.Data[i*4+2] = byte(i)
		surf.Data[i*4+3] = 0xFF
	}

	// Copy pixels [0..4] to [2..6] on same surface (overlapping right-shift)
	// Without overlap handling, this would smear: pixel[2]=pixel[0], pixel[3]=pixel[1](corrupted), ...
	s2sData := make([]byte, 18) // srcSurf(2)+dstSurf(2)+srcRect(8)+destPtsCount(2)+destPt(4)
	binary.LittleEndian.PutUint16(s2sData[0:2], 1)  // srcSurfId
	binary.LittleEndian.PutUint16(s2sData[2:4], 1)  // dstSurfId (same)
	binary.LittleEndian.PutUint16(s2sData[4:6], 0)  // srcLeft
	binary.LittleEndian.PutUint16(s2sData[6:8], 0)  // srcTop
	binary.LittleEndian.PutUint16(s2sData[8:10], 5) // srcRight
	binary.LittleEndian.PutUint16(s2sData[10:12], 1) // srcBottom
	binary.LittleEndian.PutUint16(s2sData[12:14], 1) // destPtsCount
	binary.LittleEndian.PutUint16(s2sData[14:16], 2) // destX
	binary.LittleEndian.PutUint16(s2sData[16:18], 0) // destY

	h.handleSurfaceToSurface(s2sData)

	// After the copy, pixels at x=2..6 should be the ORIGINAL values 0..4
	for x := 2; x < 7; x++ {
		expected := byte(x - 2)
		got := surf.Data[x*4]
		if got != expected {
			t.Fatalf("pixel[%d] = %d, want %d (overlap corruption)", x, got, expected)
		}
	}
}

func TestWireToSurface1Uncompressed(t *testing.T) {
	var gotX, gotY, gotW, gotH int
	var gotData []byte

	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))
	h.OnBitmap(func(_ uint16, x, y, w, hh int, data []byte) {
		gotX = x
		gotY = y
		gotW = w
		gotH = hh
		gotData = data
	})

	// Create 10x10 surface mapped at (100, 200)
	createData := make([]byte, 7)
	binary.LittleEndian.PutUint16(createData[0:2], 5)
	binary.LittleEndian.PutUint16(createData[2:4], 10)
	binary.LittleEndian.PutUint16(createData[4:6], 10)
	createData[6] = PixelFormatXRGB8888
	h.handleCreateSurface(createData)

	mapData := make([]byte, 12)
	binary.LittleEndian.PutUint16(mapData[0:2], 5)
	binary.LittleEndian.PutUint32(mapData[4:8], 100)
	binary.LittleEndian.PutUint32(mapData[8:12], 200)
	h.handleMapSurfaceToOutput(mapData)

	// WireToSurface1: uncompressed 2x2 rect at (3,4)
	w, hh := 2, 2
	pixels := make([]byte, w*hh*4)
	for i := range pixels {
		pixels[i] = byte(i)
	}

	wire := make([]byte, 17+len(pixels))
	binary.LittleEndian.PutUint16(wire[0:2], 5)                // surfaceId
	binary.LittleEndian.PutUint16(wire[2:4], CodecUncompressed) // codecId
	wire[4] = PixelFormatXRGB8888                                // pixelFormat
	binary.LittleEndian.PutUint16(wire[5:7], 3)                 // left
	binary.LittleEndian.PutUint16(wire[7:9], 4)                 // top
	binary.LittleEndian.PutUint16(wire[9:11], 5)                // right
	binary.LittleEndian.PutUint16(wire[11:13], 6)               // bottom
	binary.LittleEndian.PutUint32(wire[13:17], uint32(len(pixels)))
	copy(wire[17:], pixels)

	h.handleWireToSurface1(wire)

	if gotX != 103 || gotY != 204 {
		t.Fatalf("bitmap pos = (%d, %d), want (103, 204)", gotX, gotY)
	}
	if gotW != 2 || gotH != 2 {
		t.Fatalf("bitmap size = %dx%d, want 2x2", gotW, gotH)
	}
	if len(gotData) != 2*2*4 {
		t.Fatalf("bitmap data = %d bytes, want %d", len(gotData), 2*2*4)
	}
}
