package dvc

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestCbIdSize(t *testing.T) {
	tests := []struct {
		id   uint32
		want byte
	}{
		{0, 0},
		{0xFF, 0},
		{0x100, 1},
		{0xFFFF, 1},
		{0x10000, 2},
		{0xFFFFFFFF, 2},
	}
	for _, tt := range tests {
		got := cbIdSize(tt.id)
		if got != tt.want {
			t.Errorf("cbIdSize(0x%X) = %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestChannelIDRoundTrip(t *testing.T) {
	tests := []struct {
		id   uint32
		cbId byte
	}{
		{0, 0},
		{42, 0},
		{255, 0},
		{256, 1},
		{1000, 1},
		{65535, 1},
		{65536, 2},
		{0xDEADBEEF, 2},
	}
	for _, tt := range tests {
		buf := make([]byte, 4)
		n := putChannelID(buf, tt.cbId, tt.id)
		got, m := getChannelID(buf[:n], tt.cbId)
		if got != tt.id || m != n {
			t.Errorf("roundtrip(0x%X, cbId=%d): got 0x%X (read %d bytes, wrote %d)",
				tt.id, tt.cbId, got, m, n)
		}
	}
}

func TestHandleCaps(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	// Server caps PDU: cmd=Caps(0x50), pad, version=2
	pdu := []byte{CmdCaps << 4, 0x00, 0x02, 0x00}
	h.ProcessPDU(pdu)

	if h.version != 2 {
		t.Errorf("version = %d, want 2", h.version)
	}
	if sent == nil {
		t.Fatal("no response sent")
	}
	if len(sent) < 4 {
		t.Fatalf("response too short: %d bytes", len(sent))
	}
	respCmd := (sent[0] >> 4) & 0x0F
	if respCmd != CmdCaps {
		t.Errorf("response cmd = 0x%02X, want 0x%02X", respCmd, CmdCaps)
	}
	respVer := binary.LittleEndian.Uint16(sent[2:4])
	if respVer != 2 {
		t.Errorf("response version = %d, want 2", respVer)
	}
}

func TestHandleCreateAccept(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	var gotName string
	var gotCh *DynChannel
	h.OnChannel(func(name string, ch *DynChannel) {
		gotName = name
		gotCh = ch
		ch.SetHandler(func([]byte) {}) // accept the channel
	})

	// Create PDU: cmd=Create, cbId=0, channelID=5, name="TestChannel\0"
	name := "TestChannel"
	pdu := make([]byte, 1+1+len(name)+1)
	pdu[0] = (CmdCreate << 4) | 0 // cbId=0
	pdu[1] = 5                     // channelID (1 byte for cbId=0)
	copy(pdu[2:], name)
	pdu[2+len(name)] = 0 // null terminator

	h.ProcessPDU(pdu)

	if gotName != name {
		t.Errorf("channel name = %q, want %q", gotName, name)
	}
	if gotCh == nil {
		t.Fatal("channel not created")
	}
	if gotCh.ID != 5 {
		t.Errorf("channel ID = %d, want 5", gotCh.ID)
	}

	// Verify create response was sent with status=0 (success)
	if sent == nil {
		t.Fatal("no create response sent")
	}
	respCmd := (sent[0] >> 4) & 0x0F
	if respCmd != CmdCreate {
		t.Errorf("response cmd = 0x%02X, want 0x%02X", respCmd, CmdCreate)
	}
	status := binary.LittleEndian.Uint32(sent[2:6])
	if status != 0 {
		t.Errorf("create status = 0x%08X, want 0 (success)", status)
	}

	// Channel should be stored
	if _, ok := h.channels[5]; !ok {
		t.Error("channel 5 should be stored in handler")
	}
}

func TestHandleCreateReject(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	// Explicitly reject the channel in callback
	h.OnChannel(func(name string, ch *DynChannel) {
		ch.Reject()
	})

	name := "UnsupportedChannel"
	pdu := make([]byte, 1+1+len(name)+1)
	pdu[0] = (CmdCreate << 4) | 0
	pdu[1] = 8
	copy(pdu[2:], name)
	pdu[2+len(name)] = 0

	h.ProcessPDU(pdu)

	if sent == nil {
		t.Fatal("no create response sent")
	}
	status := binary.LittleEndian.Uint32(sent[2:6])
	if status != 0xC0000001 {
		t.Errorf("create status = 0x%08X, want 0xC0000001 (rejected)", status)
	}

	// Channel should NOT be stored
	if _, ok := h.channels[8]; ok {
		t.Error("rejected channel should not be stored")
	}
}

func TestHandleCreateDefaultAccept(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	// Callback does nothing — channel should still be accepted (default)
	h.OnChannel(func(name string, ch *DynChannel) {})

	name := "SomeChannel"
	pdu := make([]byte, 1+1+len(name)+1)
	pdu[0] = (CmdCreate << 4) | 0
	pdu[1] = 3
	copy(pdu[2:], name)
	pdu[2+len(name)] = 0

	h.ProcessPDU(pdu)

	if sent == nil {
		t.Fatal("no create response sent")
	}
	status := binary.LittleEndian.Uint32(sent[2:6])
	if status != 0 {
		t.Errorf("create status = 0x%08X, want 0 (accepted)", status)
	}
	if _, ok := h.channels[3]; !ok {
		t.Error("channel 3 should be stored")
	}
}

func TestHandleData(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler), 1600)

	var received []byte
	ch := &DynChannel{ID: 10, Name: "test"}
	ch.SetHandler(func(data []byte) {
		received = make([]byte, len(data))
		copy(received, data)
	})
	h.channels[10] = ch

	// Data PDU: cmd=Data, cbId=0, channelID=10, payload="hello"
	pdu := []byte{(CmdData << 4) | 0, 10}
	pdu = append(pdu, []byte("hello")...)

	h.ProcessPDU(pdu)

	if string(received) != "hello" {
		t.Errorf("received = %q, want %q", received, "hello")
	}
}

func TestHandleDataFirst(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler), 1600)

	var received []byte
	ch := &DynChannel{ID: 3, Name: "test"}
	ch.SetHandler(func(data []byte) {
		received = make([]byte, len(data))
		copy(received, data)
	})
	h.channels[3] = ch

	// DataFirst PDU: cmd=DataFirst, cbId=0, Sp=0, channelID=3, totalLen=10, first 5 bytes
	pdu := []byte{(CmdDataFirst << 4) | 0, 3, 10} // Sp=0 means 1-byte length
	pdu = append(pdu, []byte("hello")...)
	h.ProcessPDU(pdu)

	if received != nil {
		t.Error("should not have delivered yet (only 5 of 10 bytes)")
	}

	// Data PDU with remaining 5 bytes
	pdu2 := []byte{(CmdData << 4) | 0, 3}
	pdu2 = append(pdu2, []byte("world")...)
	h.ProcessPDU(pdu2)

	if string(received) != "helloworld" {
		t.Errorf("reassembled = %q, want %q", received, "helloworld")
	}
}

func TestHandleClose(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler), 1600)
	h.channels[7] = &DynChannel{ID: 7, Name: "test"}

	pdu := []byte{(CmdClose << 4) | 0, 7}
	h.ProcessPDU(pdu)

	if _, ok := h.channels[7]; ok {
		t.Error("channel should have been removed")
	}
}

func TestSendData(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	err := h.SendData(42, []byte("payload"))
	if err != nil {
		t.Fatalf("SendData error: %v", err)
	}
	if sent == nil {
		t.Fatal("nothing sent")
	}

	// Parse header
	cmd := (sent[0] >> 4) & 0x0F
	cbId := sent[0] & 0x03
	if cmd != CmdData {
		t.Errorf("cmd = 0x%02X, want 0x%02X", cmd, CmdData)
	}
	if cbId != 0 {
		t.Errorf("cbId = %d, want 0 (channelID=42 fits in 1 byte)", cbId)
	}
	if sent[1] != 42 {
		t.Errorf("channelID = %d, want 42", sent[1])
	}
	if string(sent[2:]) != "payload" {
		t.Errorf("payload = %q, want %q", sent[2:], "payload")
	}
}

func TestSendData2ByteID(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	err := h.SendData(300, []byte("x"))
	if err != nil {
		t.Fatalf("SendData error: %v", err)
	}

	cbId := sent[0] & 0x03
	if cbId != 1 {
		t.Errorf("cbId = %d, want 1 (channelID=300 needs 2 bytes)", cbId)
	}
	id := binary.LittleEndian.Uint16(sent[1:3])
	if id != 300 {
		t.Errorf("channelID = %d, want 300", id)
	}
}

func TestSendData4ByteID(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	err := h.SendData(100000, []byte("y"))
	if err != nil {
		t.Fatalf("SendData error: %v", err)
	}

	cbId := sent[0] & 0x03
	if cbId != 2 {
		t.Errorf("cbId = %d, want 2 (channelID=100000 needs 4 bytes)", cbId)
	}
	id := binary.LittleEndian.Uint32(sent[1:5])
	if id != 100000 {
		t.Errorf("channelID = %d, want 100000", id)
	}
}

func TestCapsVersionClamp(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler), 1600)

	// Server sends version 10 — we should clamp to 3
	pdu := []byte{CmdCaps << 4, 0x00, 0x0A, 0x00}
	h.ProcessPDU(pdu)

	if h.version != 10 {
		t.Errorf("stored version = %d, want 10", h.version)
	}
	respVer := binary.LittleEndian.Uint16(sent[2:4])
	if respVer != 3 {
		t.Errorf("response version = %d, want 3 (clamped)", respVer)
	}
}
