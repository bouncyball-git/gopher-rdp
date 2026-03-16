package rdpdr

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestParallelDeviceInterface(t *testing.T) {
	dev := NewParallelDevice(7, "LPT1", "/dev/lp0", slog.New(slog.DiscardHandler))
	var d Device = dev // verify interface compliance

	if d.ID() != 7 {
		t.Errorf("ID() = %d, want 7", d.ID())
	}
	if d.Type() != DeviceTypeParallel {
		t.Errorf("Type() = 0x%08X, want 0x%08X", d.Type(), DeviceTypeParallel)
	}
	if d.Name() != "LPT1" {
		t.Errorf("Name() = %q, want LPT1", d.Name())
	}
}

func TestParallelUnsupportedIRP(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewParallelDevice(1, "LPT1", "/dev/null", slog.New(slog.DiscardHandler))

	// Send unsupported IRP (e.g. IRP_MJ_QUERY_INFORMATION)
	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 1,
		MajorFn:      IrpQueryInfo,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusNotSupported {
		t.Errorf("status = 0x%08X, want STATUS_NOT_SUPPORTED (0x%08X)", status, StatusNotSupported)
	}
}

func TestParallelDeviceControl(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewParallelDevice(1, "LPT1", "/dev/null", slog.New(slog.DiscardHandler))

	// Device control → always success with empty output
	payload := make([]byte, 32)
	binary.LittleEndian.PutUint32(payload[0:4], 0) // output buf len
	binary.LittleEndian.PutUint32(payload[4:8], 0) // input buf len
	binary.LittleEndian.PutUint32(payload[8:12], 0x12345678) // some random IOCTL

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 1,
		MajorFn:      IrpDeviceControl,
		Payload:      payload,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusSuccess {
		t.Errorf("status = 0x%08X, want SUCCESS", status)
	}
}

func TestParallelDeviceListAnnounce(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.serverCapGenVer = GeneralCapVersion2
	h.AddParallel(20, "LPT1", "/dev/lp0")

	h.sendDeviceListFiltered(true)

	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	numDevices := binary.LittleEndian.Uint32(pdu[4:8])
	if numDevices != 1 {
		t.Fatalf("device count = %d, want 1", numDevices)
	}

	// Device type should be parallel
	devType := binary.LittleEndian.Uint32(pdu[8:12])
	if devType != DeviceTypeParallel {
		t.Errorf("device type = 0x%08X, want 0x%08X", devType, DeviceTypeParallel)
	}

	// DeviceDataLength — port devices use null-terminated ASCII
	dataLen := binary.LittleEndian.Uint32(pdu[24:28])
	if dataLen != 5 { // "LPT1" + null = 5 bytes
		t.Errorf("deviceDataLength = %d, want 5", dataLen)
	}

	devData := pdu[28 : 28+dataLen]
	if string(devData) != "LPT1\x00" {
		t.Errorf("deviceData = %q, want LPT1\\x00", string(devData))
	}
}

func TestMixedDeviceList(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.serverCapGenVer = GeneralCapVersion2
	h.AddDrive(1, "C", t.TempDir(), false)
	h.AddSerial(2, "COM1", "/dev/ttyS0")
	h.AddParallel(3, "LPT1", "/dev/lp0")

	h.sendDeviceListFiltered(true)

	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	numDevices := binary.LittleEndian.Uint32(pdu[4:8])
	if numDevices != 3 {
		t.Errorf("device count = %d, want 3", numDevices)
	}
}
