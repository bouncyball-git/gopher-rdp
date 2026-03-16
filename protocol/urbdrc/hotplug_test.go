package urbdrc

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// mockScanner implements deviceScanner for testing.
type mockScanner struct {
	mu      sync.Mutex
	devices []usbDeviceIdentity
}

func (m *mockScanner) scanDevices([]USBDeviceFilter) []usbDeviceIdentity {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]usbDeviceIdentity, len(m.devices))
	copy(result, m.devices)
	return result
}

func (m *mockScanner) set(devices []usbDeviceIdentity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices = devices
}

// mockUSBDevice implements USBDevice for testing.
type mockUSBDevice struct {
	desc    DeviceDescriptor
	path    string
	busNum  uint8
	devAddr uint8
	closed  bool
}

func (m *mockUSBDevice) Descriptor() DeviceDescriptor { return m.desc }
func (m *mockUSBDevice) Path() string                 { return m.path }
func (m *mockUSBDevice) BusAddr() (uint8, uint8)      { return m.busNum, m.devAddr }
func (m *mockUSBDevice) IsComposite() bool             { return false }
func (m *mockUSBDevice) DeviceText() string            { return "" }
func (m *mockUSBDevice) DetachKernelDriver() error     { return nil }
func (m *mockUSBDevice) SelectConfiguration(uint8) error { return nil }
func (m *mockUSBDevice) SelectInterface(uint8, uint8) error { return nil }
func (m *mockUSBDevice) ControlTransfer(uint8, uint8, uint16, uint16, []byte, uint32) (int, uint32) {
	return 0, 0
}
func (m *mockUSBDevice) BulkOrInterruptTransfer(uint8, []byte, uint32) (int, uint32) {
	return 0, 0
}
func (m *mockUSBDevice) IsochTransfer(uint8, uint32, uint32, []IsochPacket, []byte, uint32) ([]IsochPacketResult, []byte, uint32) {
	return nil, nil, 0
}
func (m *mockUSBDevice) ClearHalt(uint8) error           { return nil }
func (m *mockUSBDevice) CancelTransfer(uint32)            {}
func (m *mockUSBDevice) GetActiveConfig() *MSUSBConfig    { return nil }
func (m *mockUSBDevice) CompleteConfig(cfg *MSUSBConfig) *MSUSBConfig { return cfg }
func (m *mockUSBDevice) Close()                           { m.closed = true }

// newTestHandler creates a Handler wired up for testing with a mock dvcSend
// that captures sent data. Returns the handler and a slice pointer for sent PDUs.
func newTestHandler() (*Handler, *[]sentPDU) {
	var sent []sentPDU
	var mu sync.Mutex
	h := NewHandler(func(channelID uint32, data []byte) error {
		mu.Lock()
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, sentPDU{channelID: channelID, data: cp})
		mu.Unlock()
		return nil
	}, slog.New(slog.DiscardHandler))
	return h, &sent
}

type sentPDU struct {
	channelID uint32
	data      []byte
}

func TestPollDevices_Insert(t *testing.T) {
	h, sent := newTestHandler()

	scanner := &mockScanner{}
	h.scanner = scanner
	h.filters = []USBDeviceFilter{{VID: 0x1234, PID: 0x5678}}
	h.state = stateInitChannelOut
	h.controlChID = 42

	// Mock openDevice to return a test device.
	mockDev := &mockUSBDevice{
		desc:    DeviceDescriptor{IDVendor: 0x1234, IDProduct: 0x5678},
		path:    "1-4",
		busNum:  1,
		devAddr: 7,
	}
	h.openDevice = func(bus, addr uint8) (USBDevice, error) {
		return mockDev, nil
	}

	// Scanner discovers a new device.
	scanner.set([]usbDeviceIdentity{
		{BusNum: 1, DevAddr: 7, VID: 0x1234, PID: 0x5678, SysPath: "1-4"},
	})

	h.pollDevices()

	// Verify ADD_VIRTUAL_CHANNEL was sent on the control channel.
	if len(*sent) != 1 {
		t.Fatalf("expected 1 sent PDU, got %d", len(*sent))
	}
	pdu := (*sent)[0]
	if pdu.channelID != 42 {
		t.Errorf("sent on channel %d, want 42", pdu.channelID)
	}
	if len(pdu.data) < 12 {
		t.Fatalf("PDU too short: %d", len(pdu.data))
	}
	funcID := binary.LittleEndian.Uint32(pdu.data[8:12])
	if funcID != addVirtualChannelFn {
		t.Errorf("functionID = %08x, want %08x", funcID, addVirtualChannelFn)
	}

	// Verify device is in pendingDevices and knownDevices.
	if len(h.pendingDevices) != 1 {
		t.Errorf("pendingDevices length = %d, want 1", len(h.pendingDevices))
	}
	if _, ok := h.knownDevices["1-4"]; !ok {
		t.Error("device not in knownDevices")
	}
}

func TestPollDevices_Remove(t *testing.T) {
	h, _ := newTestHandler()

	scanner := &mockScanner{}
	h.scanner = scanner
	h.filters = []USBDeviceFilter{{VID: 0x1234, PID: 0x5678}}
	h.state = stateInitChannelOut
	h.controlChID = 42

	// Pre-populate a known device with an open channel.
	mockDev := &mockUSBDevice{path: "1-4"}
	ds := &deviceState{
		dev:         mockDev,
		usbDeviceID: 5,
		channelID:   100,
		channelOpen: true,
	}
	h.devices = append(h.devices, ds)
	h.devByUsbID[5] = ds
	h.devByChannel[100] = ds
	h.knownDevices["1-4"] = ds

	// Track dvcClose calls.
	var closedChannels []uint32
	h.dvcClose = func(channelID uint32) {
		closedChannels = append(closedChannels, channelID)
	}

	// Scanner returns empty — device was removed.
	scanner.set(nil)

	h.pollDevices()

	// Verify dvcClose was called.
	if len(closedChannels) != 1 || closedChannels[0] != 100 {
		t.Errorf("closedChannels = %v, want [100]", closedChannels)
	}

	// Verify device is cleaned up.
	if len(h.knownDevices) != 0 {
		t.Errorf("knownDevices not empty: %d", len(h.knownDevices))
	}
	if len(h.devices) != 0 {
		t.Errorf("devices not empty: %d", len(h.devices))
	}
	if _, ok := h.devByUsbID[5]; ok {
		t.Error("device still in devByUsbID")
	}
	if _, ok := h.devByChannel[100]; ok {
		t.Error("device still in devByChannel")
	}
	if !mockDev.closed {
		t.Error("device not closed")
	}
}

func TestPollDevices_NoChange(t *testing.T) {
	h, sent := newTestHandler()

	scanner := &mockScanner{}
	h.scanner = scanner
	h.filters = []USBDeviceFilter{{VID: 0x1234, PID: 0x5678}}
	h.state = stateInitChannelOut
	h.controlChID = 42

	// Pre-populate a known device.
	mockDev := &mockUSBDevice{path: "1-4"}
	ds := &deviceState{
		dev:         mockDev,
		usbDeviceID: 5,
	}
	h.devices = append(h.devices, ds)
	h.knownDevices["1-4"] = ds

	// Scanner returns the same device.
	scanner.set([]usbDeviceIdentity{
		{BusNum: 1, DevAddr: 7, VID: 0x1234, PID: 0x5678, SysPath: "1-4"},
	})

	h.pollDevices()

	// Verify nothing was sent.
	if len(*sent) != 0 {
		t.Errorf("expected no sent PDUs, got %d", len(*sent))
	}
	// Device still known.
	if _, ok := h.knownDevices["1-4"]; !ok {
		t.Error("device should still be in knownDevices")
	}
}

func TestHandleRIMCallRelease_PendingQueue(t *testing.T) {
	h, sent := newTestHandler()

	// Add two devices.
	dev1 := &mockUSBDevice{path: "1-1", desc: DeviceDescriptor{IDVendor: 0x1111}}
	dev2 := &mockUSBDevice{path: "1-2", desc: DeviceDescriptor{IDVendor: 0x2222}}
	h.AddDevice(dev1)
	h.AddDevice(dev2)

	// First RIMCALL_RELEASE establishes control channel.
	h.handleRIMCallRelease(10)

	if h.state != stateInitChannelOut {
		t.Fatalf("state = %d, want %d", h.state, stateInitChannelOut)
	}
	if h.controlChID != 10 {
		t.Errorf("controlChID = %d, want 10", h.controlChID)
	}

	// Two ADD_VIRTUAL_CHANNEL PDUs should have been sent.
	if len(*sent) != 2 {
		t.Fatalf("expected 2 ADD_VIRTUAL_CHANNEL PDUs, got %d", len(*sent))
	}

	// Two devices should be pending.
	if len(h.pendingDevices) != 2 {
		t.Fatalf("pendingDevices = %d, want 2", len(h.pendingDevices))
	}

	// Second RIMCALL_RELEASE assigns channel to first device (FIFO).
	h.handleRIMCallRelease(20)

	if len(h.pendingDevices) != 1 {
		t.Errorf("pendingDevices = %d, want 1", len(h.pendingDevices))
	}
	ds1 := h.devByChannel[20]
	if ds1 == nil {
		t.Fatal("device not assigned to channel 20")
	}
	if ds1.dev.Descriptor().IDVendor != 0x1111 {
		t.Errorf("first device VID = %04x, want 1111", ds1.dev.Descriptor().IDVendor)
	}

	// Third RIMCALL_RELEASE assigns channel to second device.
	h.handleRIMCallRelease(30)

	if len(h.pendingDevices) != 0 {
		t.Errorf("pendingDevices = %d, want 0", len(h.pendingDevices))
	}
	ds2 := h.devByChannel[30]
	if ds2 == nil {
		t.Fatal("device not assigned to channel 30")
	}
	if ds2.dev.Descriptor().IDVendor != 0x2222 {
		t.Errorf("second device VID = %04x, want 2222", ds2.dev.Descriptor().IDVendor)
	}
}

func TestMonitorLifecycle(t *testing.T) {
	h, _ := newTestHandler()

	scanner := &mockScanner{}
	h.scanner = scanner
	h.filters = []USBDeviceFilter{{VID: 0x1234}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.ctx = ctx

	// Start monitor via handleRIMCallRelease (stateInitChannelIn → stateInitChannelOut).
	h.handleRIMCallRelease(10)

	// Give the goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	h.mu.Lock()
	running := h.monitorCancel != nil
	h.mu.Unlock()
	if !running {
		t.Error("monitor should be running after control channel established")
	}

	// Stop monitor.
	h.StopMonitor()

	h.mu.Lock()
	stopped := h.monitorCancel == nil
	h.mu.Unlock()
	if !stopped {
		t.Error("monitor should be stopped after StopMonitor")
	}
}

func TestPollDevices_ClassExclusion(t *testing.T) {
	h, sent := newTestHandler()

	scanner := &mockScanner{}
	h.scanner = scanner
	// Wildcard filter (auto mode).
	h.filters = []USBDeviceFilter{{VID: 0, PID: 0}}
	h.excludeClasses = []uint8{0x03, 0x09} // HID, Hub
	h.state = stateInitChannelOut
	h.controlChID = 42

	devIdx := 0
	h.openDevice = func(bus, addr uint8) (USBDevice, error) {
		devIdx++
		return &mockUSBDevice{
			path:    fmt.Sprintf("1-%d", devIdx),
			busNum:  bus,
			devAddr: addr,
		}, nil
	}

	// Scanner returns: mass storage (0x08), HID (0x03), hub (0x09), printer (0x07).
	scanner.set([]usbDeviceIdentity{
		{BusNum: 1, DevAddr: 1, VID: 0xAAAA, PID: 0x0001, DeviceClass: 0x08, SysPath: "1-1"},
		{BusNum: 1, DevAddr: 2, VID: 0xBBBB, PID: 0x0002, DeviceClass: 0x03, SysPath: "1-2"},
		{BusNum: 1, DevAddr: 3, VID: 0xCCCC, PID: 0x0003, DeviceClass: 0x09, SysPath: "1-3"},
		{BusNum: 1, DevAddr: 4, VID: 0xDDDD, PID: 0x0004, DeviceClass: 0x07, SysPath: "1-4"},
	})

	h.pollDevices()

	// Only mass storage (0x08) and printer (0x07) should be added.
	// HID (0x03) and hub (0x09) should be excluded.
	if len(*sent) != 2 {
		t.Fatalf("expected 2 ADD_VIRTUAL_CHANNEL PDUs (mass storage + printer), got %d", len(*sent))
	}
	if len(h.pendingDevices) != 2 {
		t.Errorf("pendingDevices = %d, want 2", len(h.pendingDevices))
	}
	if _, ok := h.knownDevices["1-2"]; ok {
		t.Error("HID device (1-2) should have been excluded")
	}
	if _, ok := h.knownDevices["1-3"]; ok {
		t.Error("Hub device (1-3) should have been excluded")
	}
}

func TestPollDevices_ExplicitVIDPIDBypassesClassExclusion(t *testing.T) {
	h, sent := newTestHandler()

	scanner := &mockScanner{}
	h.scanner = scanner
	// Both wildcard and explicit filter for the HID device.
	h.filters = []USBDeviceFilter{
		{VID: 0, PID: 0},          // wildcard
		{VID: 0xBBBB, PID: 0x0002}, // explicit match for the HID device
	}
	h.excludeClasses = []uint8{0x03}
	h.state = stateInitChannelOut
	h.controlChID = 42

	h.openDevice = func(bus, addr uint8) (USBDevice, error) {
		return &mockUSBDevice{
			path:    fmt.Sprintf("1-%d", addr),
			busNum:  bus,
			devAddr: addr,
		}, nil
	}

	// HID device that matches the explicit filter — should NOT be excluded.
	scanner.set([]usbDeviceIdentity{
		{BusNum: 1, DevAddr: 2, VID: 0xBBBB, PID: 0x0002, DeviceClass: 0x03, SysPath: "1-2"},
	})

	h.pollDevices()

	// Device should be added despite class 0x03, because explicit filter matches.
	if len(*sent) != 1 {
		t.Fatalf("expected 1 ADD_VIRTUAL_CHANNEL (explicit filter bypasses class exclusion), got %d", len(*sent))
	}
	if _, ok := h.knownDevices["1-2"]; !ok {
		t.Error("explicitly-filtered HID device should be in knownDevices")
	}
}
