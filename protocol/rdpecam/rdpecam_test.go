package rdpecam

import (
	"encoding/binary"
	"log/slog"
	"testing"
	"time"
)

const testChID = 1

func testSendFn(devSent *[][]byte) func([]byte) error {
	return func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		*devSent = append(*devSent, cp)
		return nil
	}
}

func TestSelectVersionAndDeviceAdded(t *testing.T) {
	var enumSent [][]byte
	h := NewHandler(slog.New(slog.DiscardHandler), "Test Camera")
	h.SetEnumSendFn(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		enumSent = append(enumSent, cp)
		return nil
	})

	h.EnumChannelOpened()

	if len(enumSent) != 1 {
		t.Fatalf("expected 1 enum message, got %d", len(enumSent))
	}
	if enumSent[0][0] != protoVersion {
		t.Fatalf("version = %d, want %d", enumSent[0][0], protoVersion)
	}
	if enumSent[0][1] != msgSelectVersionRequest {
		t.Fatalf("msgId = 0x%02X, want 0x%02X", enumSent[0][1], msgSelectVersionRequest)
	}

	resp := []byte{protoVersion, msgSelectVersionResponse}
	h.ProcessEnumPDU(resp)

	if len(enumSent) != 2 {
		t.Fatalf("expected 2 enum messages, got %d", len(enumSent))
	}
	if enumSent[1][1] != msgDeviceAddedNotify {
		t.Fatalf("msgId = 0x%02X, want 0x%02X (DeviceAdded)", enumSent[1][1], msgDeviceAddedNotify)
	}
}

func TestStreamListResponse(t *testing.T) {
	var devSent [][]byte
	h := NewHandler(slog.New(slog.DiscardHandler), "Test Camera")
	h.SetDevSendFn(testChID, testSendFn(&devSent))

	h.ProcessDevPDU(testChID, []byte{protoVersion, msgActivateDeviceRequest})
	if len(devSent) != 1 || devSent[0][1] != msgSuccessResponse {
		t.Fatal("expected SuccessResponse for ActivateDevice")
	}

	h.ProcessDevPDU(testChID, []byte{protoVersion, msgStreamListRequest})
	if len(devSent) != 2 {
		t.Fatalf("expected 2 dev messages, got %d", len(devSent))
	}
	resp := devSent[1]
	if resp[1] != msgStreamListResponse {
		t.Fatalf("msgId = 0x%02X, want 0x%02X", resp[1], msgStreamListResponse)
	}
	payload := resp[2:]
	if len(payload) != 5 {
		t.Fatalf("payload len = %d, want 5", len(payload))
	}
	srcType := binary.LittleEndian.Uint16(payload[0:2])
	if srcType != frameSourceColor {
		t.Fatalf("FrameSourceTypes = 0x%04X, want 0x%04X", srcType, frameSourceColor)
	}
}

func TestMediaTypeListResponse(t *testing.T) {
	var devSent [][]byte
	h := NewHandler(slog.New(slog.DiscardHandler), "Test Camera")
	h.SetDevSendFn(testChID, testSendFn(&devSent))

	h.ProcessDevPDU(testChID, []byte{protoVersion, msgMediaTypeListRequest, 0})
	if len(devSent) != 1 {
		t.Fatalf("expected 1 dev message, got %d", len(devSent))
	}
	resp := devSent[0]
	if resp[1] != msgMediaTypeListResponse {
		t.Fatalf("msgId = 0x%02X, want 0x%02X", resp[1], msgMediaTypeListResponse)
	}
	payload := resp[2:]
	if len(payload) != 26 {
		t.Fatalf("payload len = %d, want 26", len(payload))
	}
	if payload[0] != FormatH264 {
		t.Fatalf("format = %d, want %d (H264)", payload[0], FormatH264)
	}
	w := binary.LittleEndian.Uint32(payload[1:])
	h2 := binary.LittleEndian.Uint32(payload[5:])
	if w != 1280 || h2 != 720 {
		t.Fatalf("resolution = %dx%d, want 1280x720", w, h2)
	}
}

func TestStartStreamsAndSampleRequest(t *testing.T) {
	var devSent [][]byte
	var startW, startH, startFPS int
	h := NewHandler(slog.New(slog.DiscardHandler), "Test Camera")
	h.SetDevSendFn(testChID, testSendFn(&devSent))
	h.OnStartCapture(func(w, hh, fps int) {
		startW = w
		startH = hh
		startFPS = fps
	})

	pdu := make([]byte, 2+1+26)
	pdu[0] = protoVersion
	pdu[1] = msgStartStreamsRequest
	pdu[2] = 0 // StreamIndex
	mt := MediaType{Format: FormatH264, Width: 640, Height: 480, FPSNum: 15, FPSDenom: 1, PARNum: 1, PARDenom: 1, Flags: 0x01}
	mt.marshal(pdu[3:29])

	h.ProcessDevPDU(testChID, pdu)

	if len(devSent) != 1 || devSent[0][1] != msgSuccessResponse {
		t.Fatal("expected SuccessResponse for StartStreams")
	}
	if startW != 640 || startH != 480 || startFPS != 15 {
		t.Fatalf("start capture = %dx%d@%d, want 640x480@15", startW, startH, startFPS)
	}

	// SampleRequest — handler blocks in goroutine until sample arrives
	devSent = devSent[:0]
	h.ProcessDevPDU(testChID, []byte{protoVersion, msgSampleRequest, 0})

	// Deliver a sample from "browser" — unblocks the goroutine
	nalData := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x28}
	h.SendSample(nalData)
	time.Sleep(50 * time.Millisecond)

	if len(devSent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(devSent))
	}
	if devSent[0][1] != msgSampleResponse {
		t.Fatalf("msgId = 0x%02X, want 0x%02X (SampleResponse)", devSent[0][1], msgSampleResponse)
	}
	payload := devSent[0][2:]
	if payload[0] != 0 {
		t.Fatalf("streamIndex = %d, want 0", payload[0])
	}
	if len(payload)-1 != len(nalData) {
		t.Fatalf("nal data len = %d, want %d", len(payload)-1, len(nalData))
	}
}

func TestSampleRequestWaitsForData(t *testing.T) {
	var devSent [][]byte
	h := NewHandler(slog.New(slog.DiscardHandler), "Test Camera")
	h.SetDevSendFn(testChID, testSendFn(&devSent))

	// SampleRequest with no data — should NOT send error, just wait
	h.ProcessDevPDU(testChID, []byte{protoVersion, msgSampleRequest, 0})
	time.Sleep(20 * time.Millisecond)

	if len(devSent) != 0 {
		t.Fatalf("expected 0 messages (waiting), got %d", len(devSent))
	}

	// Now deliver data — goroutine should send SampleResponse
	h.SendSample([]byte{0x00, 0x00, 0x01, 0x65})
	time.Sleep(50 * time.Millisecond)

	if len(devSent) != 1 {
		t.Fatalf("expected 1 message after sample, got %d", len(devSent))
	}
	if devSent[0][1] != msgSampleResponse {
		t.Fatalf("msgId = 0x%02X, want 0x%02X", devSent[0][1], msgSampleResponse)
	}
}

func TestStopStreams(t *testing.T) {
	var stopped bool
	h := NewHandler(slog.New(slog.DiscardHandler), "Test Camera")
	h.SetDevSendFn(testChID, func([]byte) error { return nil })
	h.OnStopCapture(func() { stopped = true })

	pdu := make([]byte, 2+1+26)
	pdu[0] = protoVersion
	pdu[1] = msgStartStreamsRequest
	pdu[2] = 0 // StreamIndex
	mt := MediaType{Format: FormatH264, Width: 640, Height: 480, FPSNum: 30, FPSDenom: 1, PARNum: 1, PARDenom: 1, Flags: 0x01}
	mt.marshal(pdu[3:29])
	h.ProcessDevPDU(testChID, pdu)

	h.ProcessDevPDU(testChID, []byte{protoVersion, msgStopStreamsRequest})
	if !stopped {
		t.Fatal("onStopCapture not called")
	}
}
