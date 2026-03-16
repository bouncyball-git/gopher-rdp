package rdpdr

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

// mockScardBackend implements ScardBackend for testing.
type mockScardBackend struct {
	establishRC  uint32
	establishCtx uint32
	releaseRC    uint32
	isValidRC    uint32
	listReadersData []byte
	listReadersRC   uint32
	connectHandle   uint32
	connectProto    uint32
	connectRC       uint32
	disconnectRC    uint32
	transmitRecvPCI []byte
	transmitRecvBuf []byte
	transmitRC      uint32
	cancelRC        uint32
	statusReader    string
	statusState     uint32
	statusProto     uint32
	statusATR       []byte
	statusRC        uint32
	getAttribData   []byte
	getAttribRC     uint32

	// Track calls
	lastScope       uint32
	lastCtx         uint32
	lastHandle      uint32
	lastDisposition uint32
}

func (m *mockScardBackend) EstablishContext(scope uint32) (uint32, uint32) {
	m.lastScope = scope
	return m.establishCtx, m.establishRC
}

func (m *mockScardBackend) ReleaseContext(ctx uint32) uint32 {
	m.lastCtx = ctx
	return m.releaseRC
}

func (m *mockScardBackend) IsValidContext(ctx uint32) uint32 {
	m.lastCtx = ctx
	return m.isValidRC
}

func (m *mockScardBackend) ListReaders(ctx uint32, groups []byte) ([]byte, uint32) {
	return m.listReadersData, m.listReadersRC
}

func (m *mockScardBackend) GetStatusChange(ctx uint32, timeout uint32, states []ScardReaderState) ([]ScardReaderState, uint32) {
	for i := range states {
		states[i].EventState = states[i].CurrentState | ScardStateChanged
	}
	return states, ScardSuccess
}

func (m *mockScardBackend) Connect(ctx uint32, reader string, shareMode, preferredProtocol uint32) (uint32, uint32, uint32) {
	return m.connectHandle, m.connectProto, m.connectRC
}

func (m *mockScardBackend) Disconnect(handle uint32, disposition uint32) uint32 {
	m.lastHandle = handle
	m.lastDisposition = disposition
	return m.disconnectRC
}

func (m *mockScardBackend) Reconnect(handle uint32, shareMode, preferredProtocol, disposition uint32) (uint32, uint32) {
	return 0, ScardSuccess
}

func (m *mockScardBackend) BeginTransaction(handle uint32) uint32 {
	return ScardSuccess
}

func (m *mockScardBackend) EndTransaction(handle uint32, disposition uint32) uint32 {
	return ScardSuccess
}

func (m *mockScardBackend) Status(handle uint32) (string, uint32, uint32, []byte, uint32) {
	return m.statusReader, m.statusState, m.statusProto, m.statusATR, m.statusRC
}

func (m *mockScardBackend) Transmit(handle uint32, sendPCI, sendBuf []byte) ([]byte, []byte, uint32) {
	return m.transmitRecvPCI, m.transmitRecvBuf, m.transmitRC
}

func (m *mockScardBackend) Control(handle uint32, controlCode uint32, inBuf []byte) ([]byte, uint32) {
	return nil, ScardSuccess
}

func (m *mockScardBackend) GetAttrib(handle uint32, attrID uint32) ([]byte, uint32) {
	return m.getAttribData, m.getAttribRC
}

func (m *mockScardBackend) Cancel(ctx uint32) uint32 {
	m.lastCtx = ctx
	return m.cancelRC
}

func (m *mockScardBackend) Close() error { return nil }

// buildScardPayload builds a complete IOCTL payload for testing.
// ndrBody is the NDR body after the 16-byte type serialization headers.
func buildScardPayload(ioctl uint32, ndrBody []byte) []byte {
	// Pad body to 8 bytes
	if pad := len(ndrBody) % 8; pad != 0 {
		ndrBody = append(ndrBody, make([]byte, 8-pad)...)
	}
	ndrBuf := make([]byte, ndrHeaderLen+len(ndrBody))
	copy(ndrBuf[0:8], ndrCommonTypeHeader[:])
	binary.LittleEndian.PutUint32(ndrBuf[8:12], uint32(len(ndrBody)))
	copy(ndrBuf[ndrHeaderLen:], ndrBody)

	payload := make([]byte, 32+len(ndrBuf))
	binary.LittleEndian.PutUint32(payload[0:4], 2048)                  // outputBufLen
	binary.LittleEndian.PutUint32(payload[4:8], uint32(len(ndrBuf)))   // inputBufLen
	binary.LittleEndian.PutUint32(payload[8:12], ioctl)                // IoControlCode
	copy(payload[32:], ndrBuf)
	return payload
}

// parseScardResponse extracts IoStatus, SCARD ReturnCode, and remaining NDR body
// from an IO completion PDU.
func parseScardResponse(t *testing.T, pdu []byte) (ioStatus, rc uint32, body []byte) {
	t.Helper()
	if len(pdu) < 20 {
		t.Fatalf("PDU too short: %d bytes", len(pdu))
	}
	ioStatus = binary.LittleEndian.Uint32(pdu[12:16])
	outBufLen := binary.LittleEndian.Uint32(pdu[16:20])
	if outBufLen == 0 {
		return ioStatus, 0, nil
	}
	ndrData := pdu[20 : 20+outBufLen]
	if len(ndrData) < ndrHeaderLen+4 {
		t.Fatalf("NDR data too short: %d bytes", len(ndrData))
	}
	ndrBody := ndrData[ndrHeaderLen:]
	rc = binary.LittleEndian.Uint32(ndrBody[0:4])
	return ioStatus, rc, ndrBody[4:]
}

func TestSmartcardDeviceInterface(t *testing.T) {
	mock := &mockScardBackend{}
	dev := NewSmartcardDevice(5, mock, slog.New(slog.DiscardHandler))
	var d Device = dev

	if d.ID() != 5 {
		t.Errorf("ID() = %d, want 5", d.ID())
	}
	if d.Type() != DeviceTypeSmartcard {
		t.Errorf("Type() = 0x%08X, want 0x%08X", d.Type(), DeviceTypeSmartcard)
	}
	if d.Name() != "SCARD" {
		t.Errorf("Name() = %q, want SCARD", d.Name())
	}
}

func TestSmartcardUnsupportedMajorFn(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	mock := &mockScardBackend{}
	dev := NewSmartcardDevice(1, mock, slog.New(slog.DiscardHandler))

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 1,
		MajorFn:      IrpCreate,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusNotSupported {
		t.Errorf("status = 0x%08X, want STATUS_NOT_SUPPORTED", status)
	}
}

func TestSmartcardAccessStartedEvent(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	mock := &mockScardBackend{}
	dev := NewSmartcardDevice(1, mock, slog.New(slog.DiscardHandler))

	// AccessStartedEvent has no meaningful input data
	payload := make([]byte, 32)
	binary.LittleEndian.PutUint32(payload[0:4], 256)
	binary.LittleEndian.PutUint32(payload[8:12], scardIOCTLAccessStarted)

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 42,
		MajorFn:      IrpDeviceControl,
		Payload:      payload,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}

	ioStatus, rc, _ := parseScardResponse(t, sent[0])
	if ioStatus != StatusSuccess {
		t.Errorf("IO status = 0x%08X, want SUCCESS", ioStatus)
	}
	if rc != ScardSuccess {
		t.Errorf("SCARD rc = 0x%08X, want SCARD_S_SUCCESS", rc)
	}
}

func TestSmartcardEstablishContext(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	mock := &mockScardBackend{
		establishCtx: 0xDEAD,
		establishRC:  ScardSuccess,
	}
	dev := NewSmartcardDevice(1, mock, slog.New(slog.DiscardHandler))

	// EstablishContext_Call NDR body: dwScope(4) + padding(4)
	var ndrBody [8]byte
	binary.LittleEndian.PutUint32(ndrBody[0:4], ScardScopeSystem)
	payload := buildScardPayload(scardIOCTLEstablishContext, ndrBody[:])

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 1,
		MajorFn:      IrpDeviceControl,
		Payload:      payload,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}

	ioStatus, rc, body := parseScardResponse(t, sent[0])
	if ioStatus != StatusSuccess {
		t.Fatalf("IO status = 0x%08X, want SUCCESS", ioStatus)
	}
	if rc != ScardSuccess {
		t.Fatalf("SCARD rc = 0x%08X, want SUCCESS", rc)
	}

	// EstablishContext_Return body (after ReturnCode):
	// cbContext(4) + refID(4) [inline] + MaxCount(4) + data(4) [deferred]
	if len(body) < 16 {
		t.Fatalf("body too short: %d bytes", len(body))
	}
	cbCtx := binary.LittleEndian.Uint32(body[0:4])
	if cbCtx != 4 {
		t.Errorf("cbContext = %d, want 4", cbCtx)
	}
	refID := binary.LittleEndian.Uint32(body[4:8])
	if refID == 0 {
		t.Error("context referent ID is NULL")
	}
	// Deferred: MaxCount(4) + context(4)
	maxCount := binary.LittleEndian.Uint32(body[8:12])
	if maxCount != 4 {
		t.Errorf("MaximumCount = %d, want 4", maxCount)
	}
	ctx := binary.LittleEndian.Uint32(body[12:16])
	if ctx != 0xDEAD {
		t.Errorf("context = 0x%X, want 0xDEAD", ctx)
	}

	if mock.lastScope != ScardScopeSystem {
		t.Errorf("scope = %d, want %d", mock.lastScope, ScardScopeSystem)
	}
}

func TestNDRReaderWriter(t *testing.T) {
	// Test NDR writer produces data that the NDR reader can parse
	w := ndrNewWriter()
	w.u32(0x42)           // ReturnCode
	w.writeContext(0xDEAD) // REDIR_SCARDCONTEXT

	ndrData := w.finish()

	r := ndrRead(ndrData)
	rc := r.u32()
	cbCtx := r.readContextInline()
	ctx := r.readContextDeferred(cbCtx)

	if !r.ok() {
		t.Fatal("NDR read failed")
	}
	if rc != 0x42 {
		t.Errorf("ReturnCode = 0x%X, want 0x42", rc)
	}
	if ctx != 0xDEAD {
		t.Errorf("context = 0x%X, want 0xDEAD", ctx)
	}
}

func TestNDRHandleRoundTrip(t *testing.T) {
	w := ndrNewWriter()
	w.writeHandle(0xAAAA, 0xBBBB)

	ndrData := w.finish()
	r := ndrRead(ndrData)
	cbCtx, cbHdl := r.readHandleInline()
	ctx, hdl := r.readHandleDeferred(cbCtx, cbHdl)

	if !r.ok() {
		t.Fatal("NDR read failed")
	}
	if ctx != 0xAAAA {
		t.Errorf("context = 0x%X, want 0xAAAA", ctx)
	}
	if hdl != 0xBBBB {
		t.Errorf("handle = 0x%X, want 0xBBBB", hdl)
	}
}

func TestNDRByteArrayRoundTrip(t *testing.T) {
	testData := []byte("hello\x00world\x00\x00")

	w := ndrNewWriter()
	w.writeByteArrayPtr(testData)
	ndrData := w.finish()

	r := ndrRead(ndrData)
	cBytes := r.u32()
	ref := r.u32()
	if ref == 0 {
		t.Fatal("expected non-NULL referent")
	}
	maxCount := r.u32()
	data := r.readBytes(cBytes)

	if !r.ok() {
		t.Fatal("NDR read failed")
	}
	if maxCount != cBytes {
		t.Errorf("MaximumCount = %d, want %d", maxCount, cBytes)
	}
	if string(data) != string(testData) {
		t.Errorf("data = %q, want %q", data, testData)
	}
}

func TestSmartcardDeviceListAnnounce(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.serverCapGenVer = GeneralCapVersion2
	mock := &mockScardBackend{}
	h.AddSmartcard(50, mock)

	h.sendDeviceListFiltered(false)
	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	numDevices := binary.LittleEndian.Uint32(pdu[4:8])
	if numDevices != 1 {
		t.Fatalf("pre-logon device count = %d, want 1", numDevices)
	}

	devType := binary.LittleEndian.Uint32(pdu[8:12])
	if devType != DeviceTypeSmartcard {
		t.Errorf("device type = 0x%08X, want 0x%08X", devType, DeviceTypeSmartcard)
	}

	dataLen := binary.LittleEndian.Uint32(pdu[24:28])
	if dataLen != 0 {
		t.Errorf("deviceDataLength = %d, want 0", dataLen)
	}

	dosName := pdu[16:24]
	if string(dosName[:5]) != "SCARD" {
		t.Errorf("dosName = %q, want SCARD...", string(dosName))
	}

	// Post-logon: smartcard should NOT be re-announced
	sent = sent[:0]
	h.sendDeviceListFiltered(true)
	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}
	pdu = sent[0]
	numDevices = binary.LittleEndian.Uint32(pdu[4:8])
	if numDevices != 0 {
		t.Errorf("post-logon device count = %d, want 0 (smartcard already announced)", numDevices)
	}
}

func TestSmartcardCapabilities(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.serverIOCode1 = 0x0000FFFF
	mock := &mockScardBackend{}
	h.AddSmartcard(1, mock)

	h.sendCoreCapResponse()

	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	numCaps := binary.LittleEndian.Uint16(pdu[4:6])
	if numCaps != 3 {
		t.Errorf("numCaps = %d, want 3", numCaps)
	}

	specialTypeCap := binary.LittleEndian.Uint32(pdu[48:52])
	if specialTypeCap != 1 {
		t.Errorf("specialTypeDeviceCap = %d, want 1", specialTypeCap)
	}

	off := 8
	foundSmartcard := false
	for i := 0; i < int(numCaps); i++ {
		capType := binary.LittleEndian.Uint16(pdu[off : off+2])
		capLen := binary.LittleEndian.Uint16(pdu[off+2 : off+4])
		if capType == CapSmartCard {
			foundSmartcard = true
		}
		off += int(capLen)
	}
	if !foundSmartcard {
		t.Error("SmartCard capability set not found in response")
	}
}
