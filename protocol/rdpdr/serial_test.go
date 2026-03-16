package rdpdr

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestSerialDeviceInterface(t *testing.T) {
	dev := NewSerialDevice(5, "COM3", "/dev/ttyUSB0", slog.New(slog.DiscardHandler))
	var d Device = dev // verify interface compliance

	if d.ID() != 5 {
		t.Errorf("ID() = %d, want 5", d.ID())
	}
	if d.Type() != DeviceTypeSerial {
		t.Errorf("Type() = 0x%08X, want 0x%08X", d.Type(), DeviceTypeSerial)
	}
	if d.Name() != "COM3" {
		t.Errorf("Name() = %q, want COM3", d.Name())
	}
}

func TestSerialBaudRateMapping(t *testing.T) {
	tests := []struct {
		baud  uint32
		speed uint32
	}{
		{9600, b9600},
		{19200, b19200},
		{38400, b38400},
		{57600, b57600},
		{115200, b115200},
		{230400, b230400},
		{460800, b460800},
		{300, b300},
		{1200, b1200},
		{2400, b2400},
		{4800, b4800},
		{50, b50},
		{99999, b9600}, // unknown → default 9600
	}

	for _, tc := range tests {
		speed := baudToSpeed(tc.baud)
		if speed != tc.speed {
			t.Errorf("baudToSpeed(%d) = 0x%X, want 0x%X", tc.baud, speed, tc.speed)
		}
	}
}

func TestSerialSpeedToBaudRoundTrip(t *testing.T) {
	bauds := []uint32{50, 75, 110, 134, 150, 200, 300, 600, 1200, 1800, 2400, 4800,
		9600, 19200, 38400, 57600, 115200, 230400, 460800}

	for _, baud := range bauds {
		speed := baudToSpeed(baud)
		got := speedToBaud(speed)
		if got != baud {
			t.Errorf("round-trip failed: baud %d → speed 0x%X → baud %d", baud, speed, got)
		}
	}
}

func TestSerialIoctlCodes(t *testing.T) {
	// Verify IOCTL codes match MS-RDPESP / ntddser.h
	tests := []struct {
		name string
		code uint32
		want uint32
	}{
		{"SET_BAUD_RATE", ioctlSerialSetBaudRate, 0x001B0004},
		{"GET_BAUD_RATE", ioctlSerialGetBaudRate, 0x001B0050},
		{"SET_LINE_CONTROL", ioctlSerialSetLineControl, 0x001B000C},
		{"GET_LINE_CONTROL", ioctlSerialGetLineControl, 0x001B0054},
		{"SET_TIMEOUTS", ioctlSerialSetTimeouts, 0x001B001C},
		{"GET_TIMEOUTS", ioctlSerialGetTimeouts, 0x001B0020},
		{"SET_CHARS", ioctlSerialSetChars, 0x001B005C},
		{"GET_CHARS", ioctlSerialGetChars, 0x001B0058},
		{"SET_HANDFLOW", ioctlSerialSetHandflow, 0x001B0064},
		{"GET_HANDFLOW", ioctlSerialGetHandflow, 0x001B0060},
		{"SET_QUEUE_SIZE", ioctlSerialSetQueueSize, 0x001B0008},
		{"SET_DTR", ioctlSerialSetDTR, 0x001B0024},
		{"CLR_DTR", ioctlSerialClrDTR, 0x001B0028},
		{"SET_RTS", ioctlSerialSetRTS, 0x001B0030},
		{"CLR_RTS", ioctlSerialClrRTS, 0x001B0034},
		{"SET_BREAK_ON", ioctlSerialSetBreakOn, 0x001B0010},
		{"SET_BREAK_OFF", ioctlSerialSetBreakOff, 0x001B0014},
		{"PURGE", ioctlSerialPurge, 0x001B004C},
		{"GET_WAIT_MASK", ioctlSerialGetWaitMask, 0x001B0040},
		{"SET_WAIT_MASK", ioctlSerialSetWaitMask, 0x001B0044},
		{"WAIT_ON_MASK", ioctlSerialWaitOnMask, 0x001B0048},
		{"GET_MODEMSTATUS", ioctlSerialGetModemStatus, 0x001B0068},
		{"GET_DTRRTS", ioctlSerialGetDTRRTS, 0x001B0078},
		{"GET_COMMSTATUS", ioctlSerialGetCommStatus, 0x001B006C},
		{"GET_PROPERTIES", ioctlSerialGetProperties, 0x001B0074},
		{"RESET_DEVICE", ioctlSerialResetDevice, 0x001B002C},
		{"IMMEDIATE_CHAR", ioctlSerialImmediateChar, 0x001B0018},
		{"CONFIG_SIZE", ioctlSerialConfigSize, 0x001B0080},
	}

	for _, tc := range tests {
		if tc.code != tc.want {
			t.Errorf("IOCTL %s = 0x%08X, want 0x%08X", tc.name, tc.code, tc.want)
		}
	}
}

func TestSerialTimeoutsRoundTrip(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewSerialDevice(1, "COM1", "/dev/null", slog.New(slog.DiscardHandler))

	// Set timeouts
	var input [20]byte
	binary.LittleEndian.PutUint32(input[0:4], 100)  // ReadIntervalTimeout
	binary.LittleEndian.PutUint32(input[4:8], 200)   // ReadTotalTimeoutMultiplier
	binary.LittleEndian.PutUint32(input[8:12], 300)  // ReadTotalTimeoutConstant
	binary.LittleEndian.PutUint32(input[12:16], 400) // WriteTotalTimeoutMultiplier
	binary.LittleEndian.PutUint32(input[16:20], 500) // WriteTotalTimeoutConstant

	dev.ioctlSetTimeouts(h, &IORequest{DeviceID: 1, CompletionID: 1}, input[:])

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusSuccess {
		t.Fatalf("set timeouts status = 0x%08X, want SUCCESS", status)
	}

	// Get timeouts
	sent = sent[:0]
	dev.ioctlGetTimeouts(h, &IORequest{DeviceID: 1, CompletionID: 2})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	resp := sent[0]
	respStatus := binary.LittleEndian.Uint32(resp[12:16])
	if respStatus != StatusSuccess {
		t.Fatalf("get timeouts status = 0x%08X, want SUCCESS", respStatus)
	}

	// Output: header(16) + OutputBufferLength(4) + data(20) = 40
	if len(resp) < 16+4+20 {
		t.Fatalf("response too short: %d", len(resp))
	}
	outLen := binary.LittleEndian.Uint32(resp[16:20])
	if outLen != 20 {
		t.Fatalf("output buffer length = %d, want 20", outLen)
	}

	data := resp[20:]
	if binary.LittleEndian.Uint32(data[0:4]) != 100 {
		t.Errorf("ReadIntervalTimeout = %d, want 100", binary.LittleEndian.Uint32(data[0:4]))
	}
	if binary.LittleEndian.Uint32(data[4:8]) != 200 {
		t.Errorf("ReadTotalTimeoutMultiplier = %d, want 200", binary.LittleEndian.Uint32(data[4:8]))
	}
	if binary.LittleEndian.Uint32(data[8:12]) != 300 {
		t.Errorf("ReadTotalTimeoutConstant = %d, want 300", binary.LittleEndian.Uint32(data[8:12]))
	}
	if binary.LittleEndian.Uint32(data[12:16]) != 400 {
		t.Errorf("WriteTotalTimeoutMultiplier = %d, want 400", binary.LittleEndian.Uint32(data[12:16]))
	}
	if binary.LittleEndian.Uint32(data[16:20]) != 500 {
		t.Errorf("WriteTotalTimeoutConstant = %d, want 500", binary.LittleEndian.Uint32(data[16:20]))
	}
}

func TestSerialWaitMaskRoundTrip(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewSerialDevice(1, "COM1", "/dev/null", slog.New(slog.DiscardHandler))
	dev.fd = 0 // fake open fd for IOCTL dispatch

	// Build IOCTL payload for SET_WAIT_MASK
	payload := make([]byte, 36) // OutputBufLen(4)+InputBufLen(4)+IoCtl(4)+Padding(20)+Input(4)
	binary.LittleEndian.PutUint32(payload[0:4], 0)  // output buf len
	binary.LittleEndian.PutUint32(payload[4:8], 4)   // input buf len
	binary.LittleEndian.PutUint32(payload[8:12], ioctlSerialSetWaitMask)
	// padding [12:32]
	binary.LittleEndian.PutUint32(payload[32:36], 0xFF) // wait mask

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 1,
		MajorFn:      IrpDeviceControl,
		Payload:      payload,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}

	// Verify GET_WAIT_MASK
	sent = sent[:0]
	payload2 := make([]byte, 32)
	binary.LittleEndian.PutUint32(payload2[0:4], 4)
	binary.LittleEndian.PutUint32(payload2[4:8], 0)
	binary.LittleEndian.PutUint32(payload2[8:12], ioctlSerialGetWaitMask)

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 2,
		MajorFn:      IrpDeviceControl,
		Payload:      payload2,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	resp := sent[0]
	mask := binary.LittleEndian.Uint32(resp[20:24])
	if mask != 0xFF {
		t.Errorf("wait mask = 0x%X, want 0xFF", mask)
	}
}

func TestSerialGetProperties(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewSerialDevice(1, "COM1", "/dev/null", slog.New(slog.DiscardHandler))

	dev.ioctlGetProperties(h, &IORequest{DeviceID: 1, CompletionID: 1})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	resp := sent[0]
	status := binary.LittleEndian.Uint32(resp[12:16])
	if status != StatusSuccess {
		t.Fatalf("status = 0x%08X, want SUCCESS", status)
	}

	// Output data should be 4 (OutputBufferLength) + 64 (COMMPROP) = 68 bytes in output
	outLen := binary.LittleEndian.Uint32(resp[16:20])
	if outLen != 64 {
		t.Errorf("OutputBufferLength = %d, want 64", outLen)
	}

	// Verify PacketLength field in COMMPROP
	packetLen := binary.LittleEndian.Uint16(resp[20:22])
	if packetLen != 64 {
		t.Errorf("COMMPROP.PacketLength = %d, want 64", packetLen)
	}
}

func TestSerialDeviceListAnnounce(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.serverCapGenVer = GeneralCapVersion2
	h.AddSerial(10, "COM3", "/dev/ttyUSB0")

	h.sendDeviceListFiltered(true)

	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	numDevices := binary.LittleEndian.Uint32(pdu[4:8])
	if numDevices != 1 {
		t.Fatalf("device count = %d, want 1", numDevices)
	}

	// Device type should be serial
	devType := binary.LittleEndian.Uint32(pdu[8:12])
	if devType != DeviceTypeSerial {
		t.Errorf("device type = 0x%08X, want 0x%08X", devType, DeviceTypeSerial)
	}

	// Device ID
	devID := binary.LittleEndian.Uint32(pdu[12:16])
	if devID != 10 {
		t.Errorf("device ID = %d, want 10", devID)
	}

	// DeviceDataLength — port devices use null-terminated ASCII
	dataLen := binary.LittleEndian.Uint32(pdu[24:28])
	if dataLen != 5 { // "COM3" + null = 5 bytes
		t.Errorf("deviceDataLength = %d, want 5", dataLen)
	}

	// DeviceData should be "COM3\x00"
	devData := pdu[28 : 28+dataLen]
	if string(devData) != "COM3\x00" {
		t.Errorf("deviceData = %q, want COM3\\x00", string(devData))
	}
}

func TestSerialCapPortAdvertised(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.AddSerial(1, "COM1", "/dev/ttyS0")

	h.sendCoreCapResponse()

	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	numCaps := binary.LittleEndian.Uint16(pdu[4:6])
	if numCaps != 3 {
		t.Errorf("numCaps = %d, want 3 (general + drive + port)", numCaps)
	}

	// Walk capabilities and find CapPortType
	off := 8
	foundPort := false
	for i := 0; i < int(numCaps) && off+4 <= len(pdu); i++ {
		capType := binary.LittleEndian.Uint16(pdu[off : off+2])
		capLen := binary.LittleEndian.Uint16(pdu[off+2 : off+4])
		if capType == CapPortType {
			foundPort = true
		}
		off += int(capLen)
	}
	if !foundPort {
		t.Error("CapPortType not found in capability response")
	}
}
