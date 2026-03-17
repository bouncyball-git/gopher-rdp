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

func TestParseAVC420Metablock(t *testing.T) {
	// Build a metablock with 2 regions + some NAL data
	numRegions := uint32(2)
	nalPayload := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB} // fake NAL with start code

	size := 4 + int(numRegions)*8 + int(numRegions)*2 + len(nalPayload)
	data := make([]byte, size)
	binary.LittleEndian.PutUint32(data[0:4], numRegions)

	off := 4
	// Region 0: left=10, top=20, right=100, bottom=200
	binary.LittleEndian.PutUint16(data[off:], 10)
	binary.LittleEndian.PutUint16(data[off+2:], 20)
	binary.LittleEndian.PutUint16(data[off+4:], 100)
	binary.LittleEndian.PutUint16(data[off+6:], 200)
	off += 8
	// Region 1: left=0, top=0, right=50, bottom=50
	binary.LittleEndian.PutUint16(data[off:], 0)
	binary.LittleEndian.PutUint16(data[off+2:], 0)
	binary.LittleEndian.PutUint16(data[off+4:], 50)
	binary.LittleEndian.PutUint16(data[off+6:], 50)
	off += 8
	// Quant/quality for region 0
	data[off] = 26   // qpVal
	data[off+1] = 85 // qualityVal
	off += 2
	// Quant/quality for region 1
	data[off] = 30
	data[off+1] = 90
	off += 2
	copy(data[off:], nalPayload)

	regions, nal, err := parseAVC420Metablock(data)
	if err != nil {
		t.Fatalf("parseAVC420Metablock error: %v", err)
	}
	if len(regions) != 2 {
		t.Fatalf("regions = %d, want 2", len(regions))
	}
	if regions[0].Left != 10 || regions[0].Top != 20 || regions[0].Right != 100 || regions[0].Bottom != 200 {
		t.Fatalf("region[0] = %+v, want 10,20,100,200", regions[0])
	}
	if regions[0].QPVal != 26 || regions[0].QualityVal != 85 {
		t.Fatalf("region[0] qp=%d quality=%d, want 26,85", regions[0].QPVal, regions[0].QualityVal)
	}
	if regions[1].Left != 0 || regions[1].Right != 50 {
		t.Fatalf("region[1] = %+v", regions[1])
	}
	if len(nal) != len(nalPayload) {
		t.Fatalf("NAL data = %d bytes, want %d", len(nal), len(nalPayload))
	}
	for i, b := range nalPayload {
		if nal[i] != b {
			t.Fatalf("NAL[%d] = 0x%02X, want 0x%02X", i, nal[i], b)
		}
	}
}

func TestSendCapsAdvertiseAVCEnabled(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	h.SetAVCEnabled(true)
	if err := h.SendCapsAdvertise(); err != nil {
		t.Fatalf("SendCapsAdvertise error: %v", err)
	}

	// Same structure: 9 capsets
	const numCaps = 9
	const wantLen = 8 + 2 + numCaps*12
	if len(sent) != wantLen {
		t.Fatalf("sent %d bytes, want %d", len(sent), wantLen)
	}

	// v10.x caps should have flags=0 (AVC enabled), v8.1 should have AVC420_ENABLED
	off := 10
	for i := 0; i < 7; i++ { // v10.7 through v10.0
		flags := binary.LittleEndian.Uint32(sent[off+8:])
		if flags != 0 {
			t.Fatalf("cap[%d] flags = 0x%08X, want 0 (AVC enabled)", i, flags)
		}
		off += 12
	}
	// v8.1: should have FlagAVC420Enabled
	flags81 := binary.LittleEndian.Uint32(sent[off+8:])
	if flags81 != FlagAVC420Enabled {
		t.Fatalf("v8.1 flags = 0x%08X, want 0x%08X (AVC420_ENABLED)", flags81, FlagAVC420Enabled)
	}
	off += 12
	// v8.0: flags=0
	flags80 := binary.LittleEndian.Uint32(sent[off+8:])
	if flags80 != 0 {
		t.Fatalf("v8.0 flags = 0x%08X, want 0", flags80)
	}
}

func TestWireToSurface1AVC420(t *testing.T) {
	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))

	var gotFrame *H264Frame
	h.OnH264Frame(func(f *H264Frame) {
		gotFrame = f
	})

	// Create 100x100 surface
	createData := make([]byte, 7)
	binary.LittleEndian.PutUint16(createData[0:2], 1)
	binary.LittleEndian.PutUint16(createData[2:4], 100)
	binary.LittleEndian.PutUint16(createData[4:6], 100)
	createData[6] = PixelFormatXRGB8888
	h.handleCreateSurface(createData)

	// Build AVC420 metablock: 1 region + fake NAL
	numRegions := uint32(1)
	nalPayload := []byte{0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x28} // SPS NAL
	metaSize := 4 + 8 + 2 + len(nalPayload)
	meta := make([]byte, metaSize)
	binary.LittleEndian.PutUint32(meta[0:4], numRegions)
	binary.LittleEndian.PutUint16(meta[4:], 0)   // left
	binary.LittleEndian.PutUint16(meta[6:], 0)   // top
	binary.LittleEndian.PutUint16(meta[8:], 100) // right
	binary.LittleEndian.PutUint16(meta[10:], 100) // bottom
	meta[12] = 26 // qpVal
	meta[13] = 85 // qualityVal
	copy(meta[14:], nalPayload)

	// Build WireToSurface1 PDU
	wire := make([]byte, 17+len(meta))
	binary.LittleEndian.PutUint16(wire[0:2], 1)          // surfaceId
	binary.LittleEndian.PutUint16(wire[2:4], CodecAVC420) // codecId
	wire[4] = PixelFormatXRGB8888
	binary.LittleEndian.PutUint16(wire[5:7], 0)   // left
	binary.LittleEndian.PutUint16(wire[7:9], 0)   // top
	binary.LittleEndian.PutUint16(wire[9:11], 100) // right
	binary.LittleEndian.PutUint16(wire[11:13], 100) // bottom
	binary.LittleEndian.PutUint32(wire[13:17], uint32(len(meta)))
	copy(wire[17:], meta)

	h.handleWireToSurface1(wire)

	if gotFrame == nil {
		t.Fatal("H264Frame callback not called")
	}
	if gotFrame.SurfaceID != 1 {
		t.Fatalf("SurfaceID = %d, want 1", gotFrame.SurfaceID)
	}
	if gotFrame.CodecMode != 0 {
		t.Fatalf("CodecMode = %d, want 0 (AVC420)", gotFrame.CodecMode)
	}
	if gotFrame.Left != 0 || gotFrame.Top != 0 || gotFrame.Right != 100 || gotFrame.Bottom != 100 {
		t.Fatalf("destRect = %d,%d,%d,%d want 0,0,100,100", gotFrame.Left, gotFrame.Top, gotFrame.Right, gotFrame.Bottom)
	}
	if len(gotFrame.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(gotFrame.Regions))
	}
	if len(gotFrame.NALData) != len(nalPayload) {
		t.Fatalf("NALData = %d bytes, want %d", len(gotFrame.NALData), len(nalPayload))
	}

	// Verify surface pixels are UNCHANGED (H.264 doesn't write to surface)
	surf := h.surfaces[1]
	// Surfaces are initialized to 0xFF (opaque white)
	if surf.Data[0] != 0xFF || surf.Data[1] != 0xFF || surf.Data[2] != 0xFF {
		t.Fatalf("surface pixel was modified by AVC420 — expected unchanged (0xFF)")
	}
}

func TestAVC420FrameBatching(t *testing.T) {
	h := NewHandler(func([]byte) error { return nil }, slog.New(slog.DiscardHandler))

	var frames []*H264Frame
	h.OnH264Frame(func(f *H264Frame) {
		cp := *f
		cp.NALData = make([]byte, len(f.NALData))
		copy(cp.NALData, f.NALData)
		frames = append(frames, &cp)
	})

	// Create surface
	createData := make([]byte, 7)
	binary.LittleEndian.PutUint16(createData[0:2], 1)
	binary.LittleEndian.PutUint16(createData[2:4], 100)
	binary.LittleEndian.PutUint16(createData[4:6], 100)
	createData[6] = PixelFormatXRGB8888
	h.handleCreateSurface(createData)

	// Start frame
	startData := make([]byte, 8)
	binary.LittleEndian.PutUint32(startData[4:8], 1) // frameId
	h.handleStartFrame(startData)

	// Send two AVC420 WTS1 PDUs within the frame
	for i := 0; i < 2; i++ {
		meta := make([]byte, 4+8+2+4) // 1 region + 4 bytes fake NAL
		binary.LittleEndian.PutUint32(meta[0:4], 1)
		binary.LittleEndian.PutUint16(meta[4:], 0)
		binary.LittleEndian.PutUint16(meta[6:], 0)
		binary.LittleEndian.PutUint16(meta[8:], 50)
		binary.LittleEndian.PutUint16(meta[10:], 50)
		meta[12] = 26
		meta[13] = 85
		meta[14] = 0x00
		meta[15] = 0x00
		meta[16] = 0x01
		meta[17] = byte(0x65 + i) // different NAL type per frame

		wire := make([]byte, 17+len(meta))
		binary.LittleEndian.PutUint16(wire[0:2], 1)
		binary.LittleEndian.PutUint16(wire[2:4], CodecAVC420)
		wire[4] = PixelFormatXRGB8888
		binary.LittleEndian.PutUint16(wire[5:7], 0)
		binary.LittleEndian.PutUint16(wire[7:9], 0)
		binary.LittleEndian.PutUint16(wire[9:11], 50)
		binary.LittleEndian.PutUint16(wire[11:13], 50)
		binary.LittleEndian.PutUint32(wire[13:17], uint32(len(meta)))
		copy(wire[17:], meta)
		h.handleWireToSurface1(wire)
	}

	// Frames should NOT have been delivered yet (batched)
	if len(frames) != 0 {
		t.Fatalf("frames delivered before EndFrame: %d", len(frames))
	}

	// End frame
	endData := make([]byte, 4)
	binary.LittleEndian.PutUint32(endData[0:4], 1)
	h.handleEndFrame(endData)
	time.Sleep(50 * time.Millisecond) // allow async ACK

	// Now both frames should have been delivered
	if len(frames) != 2 {
		t.Fatalf("frames after EndFrame = %d, want 2", len(frames))
	}
	if frames[0].NALData[3] != 0x65 {
		t.Fatalf("frame[0] NAL type = 0x%02X, want 0x65", frames[0].NALData[3])
	}
	if frames[1].NALData[3] != 0x66 {
		t.Fatalf("frame[1] NAL type = 0x%02X, want 0x66", frames[1].NALData[3])
	}
}
