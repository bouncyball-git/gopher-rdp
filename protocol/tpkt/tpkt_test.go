package tpkt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// mockConn implements net.Conn for testing
type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	readErr  error
	writeErr error
}

func newMockConn() *mockConn {
	return &mockConn{
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
}

func (m *mockConn) Read(b []byte) (int, error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	return m.writeBuf.Write(b)
}

func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestReadValidPacket(t *testing.T) {
	mock := newMockConn()
	// TPKT: version=3, reserved=0, length=7 (4 header + 3 payload)
	mock.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x07, 0xAA, 0xBB, 0xCC})

	c := NewConn(mock, slog.Default())
	payload, err := c.Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	want := []byte{0xAA, 0xBB, 0xCC}
	if !bytes.Equal(payload, want) {
		t.Errorf("payload = %X, want %X", payload, want)
	}
}

func TestReadEmptyPayload(t *testing.T) {
	mock := newMockConn()
	// length=4 means header only, no payload
	mock.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x04})

	c := NewConn(mock, slog.Default())
	payload, err := c.Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestReadInvalidVersion(t *testing.T) {
	mock := newMockConn()
	mock.readBuf.Write([]byte{0x02, 0x00, 0x00, 0x05, 0xFF})

	c := NewConn(mock, slog.Default())
	_, err := c.Read()
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestReadLengthTooSmall(t *testing.T) {
	mock := newMockConn()
	// Length 3 is less than HeaderSize (4)
	mock.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x03})

	c := NewConn(mock, slog.Default())
	_, err := c.Read()
	if err == nil {
		t.Fatal("expected error for length < header size")
	}
}

func TestReadTruncatedHeader(t *testing.T) {
	mock := newMockConn()
	mock.readBuf.Write([]byte{0x03, 0x00}) // Only 2 bytes of header

	c := NewConn(mock, slog.Default())
	_, err := c.Read()
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestReadTruncatedPayload(t *testing.T) {
	mock := newMockConn()
	// Header says 10 bytes total, but only 2 bytes of payload
	mock.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x0A, 0x01, 0x02})

	c := NewConn(mock, slog.Default())
	_, err := c.Read()
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestWriteValidData(t *testing.T) {
	mock := newMockConn()
	c := NewConn(mock, slog.Default())

	data := []byte{0x01, 0x02, 0x03}
	if err := c.Write(data); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	written := mock.writeBuf.Bytes()
	if len(written) != 7 {
		t.Fatalf("expected 7 bytes written, got %d", len(written))
	}
	if written[0] != Version {
		t.Errorf("version = %d, want %d", written[0], Version)
	}
	if written[1] != 0 {
		t.Errorf("reserved = %d, want 0", written[1])
	}
	length := binary.BigEndian.Uint16(written[2:4])
	if length != 7 {
		t.Errorf("length = %d, want 7", length)
	}
	if !bytes.Equal(written[4:], data) {
		t.Errorf("payload = %X, want %X", written[4:], data)
	}
}

func TestWriteEmptyPayload(t *testing.T) {
	mock := newMockConn()
	c := NewConn(mock, slog.Default())

	if err := c.Write([]byte{}); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	written := mock.writeBuf.Bytes()
	if len(written) != 4 {
		t.Fatalf("expected 4 bytes (header only), got %d", len(written))
	}
	length := binary.BigEndian.Uint16(written[2:4])
	if length != 4 {
		t.Errorf("length = %d, want 4", length)
	}
}

func TestWriteOversizedPacket(t *testing.T) {
	mock := newMockConn()
	c := NewConn(mock, slog.Default())

	// MaxPacketSize is 65535, header is 4, so max payload is 65531
	data := make([]byte, MaxPacketSize)
	if err := c.Write(data); err == nil {
		t.Fatal("expected error for oversized packet")
	}
}

func TestWriteError(t *testing.T) {
	mock := newMockConn()
	mock.writeErr = errors.New("write failed")
	c := NewConn(mock, slog.Default())

	err := c.Write([]byte{0x01})
	if err == nil {
		t.Fatal("expected error on write failure")
	}
}

func TestReadError(t *testing.T) {
	mock := newMockConn()
	mock.readErr = errors.New("read failed")
	c := NewConn(mock, slog.Default())

	_, err := c.Read()
	if err == nil {
		t.Fatal("expected error on read failure")
	}
}

func TestRoundTrip(t *testing.T) {
	// Use a pipe for a real read/write round-trip
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writer := NewConn(client, slog.Default())
	reader := NewConn(server, slog.Default())

	data := []byte("hello, TPKT!")

	errCh := make(chan error, 1)
	go func() {
		errCh <- writer.Write(data)
	}()

	payload, err := reader.Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Write error: %v", err)
	}

	if !bytes.Equal(payload, data) {
		t.Errorf("round-trip: got %q, want %q", payload, data)
	}
}

func TestRoundTripMultiplePackets(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writer := NewConn(client, slog.Default())
	reader := NewConn(server, slog.Default())

	packets := [][]byte{
		{0x01},
		{0x02, 0x03, 0x04},
		bytes.Repeat([]byte{0xFF}, 1000),
	}

	go func() {
		for _, p := range packets {
			writer.Write(p)
		}
	}()

	for i, want := range packets {
		got, err := reader.Read()
		if err != nil {
			t.Fatalf("Read packet %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("packet %d: got %d bytes, want %d bytes", i, len(got), len(want))
		}
	}
}

func TestReadPacketTPKT(t *testing.T) {
	mock := newMockConn()
	// TPKT: version=3, reserved=0, length=7 (4 header + 3 payload)
	mock.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x07, 0xAA, 0xBB, 0xCC})

	c := NewConn(mock, slog.Default())
	pktType, action, payload, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if pktType != PacketTPKT {
		t.Errorf("type = %d, want PacketTPKT", pktType)
	}
	if action != Version {
		t.Errorf("action = 0x%02X, want 0x%02X", action, Version)
	}
	if !bytes.Equal(payload, []byte{0xAA, 0xBB, 0xCC}) {
		t.Errorf("payload = %X, want AABBCC", payload)
	}
}

func TestReadPacketTPKTEmptyPayload(t *testing.T) {
	mock := newMockConn()
	mock.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x04})

	c := NewConn(mock, slog.Default())
	pktType, _, payload, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if pktType != PacketTPKT {
		t.Errorf("type = %d, want PacketTPKT", pktType)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestReadPacketFastPath1ByteLength(t *testing.T) {
	mock := newMockConn()
	// Fast-path: action=0x00, length=5 (total PDU), payload = 3 bytes
	mock.readBuf.Write([]byte{0x00, 0x05, 0xDE, 0xAD, 0xBE})

	c := NewConn(mock, slog.Default())
	pktType, action, payload, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if pktType != PacketFastPath {
		t.Errorf("type = %d, want PacketFastPath", pktType)
	}
	if action != 0x00 {
		t.Errorf("action = 0x%02X, want 0x00", action)
	}
	if !bytes.Equal(payload, []byte{0xDE, 0xAD, 0xBE}) {
		t.Errorf("payload = %X, want DEADBE", payload)
	}
}

func TestReadPacketFastPath2ByteLength(t *testing.T) {
	mock := newMockConn()
	// Fast-path: action=0x04, 2-byte length: 0x80|0x01, 0x00 = 256 total
	// consumed = 3, remaining = 253
	payload253 := make([]byte, 253)
	for i := range payload253 {
		payload253[i] = byte(i)
	}
	mock.readBuf.Write([]byte{0x04, 0x81, 0x00})
	mock.readBuf.Write(payload253)

	c := NewConn(mock, slog.Default())
	pktType, action, payload, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if pktType != PacketFastPath {
		t.Errorf("type = %d, want PacketFastPath", pktType)
	}
	if action != 0x04 {
		t.Errorf("action = 0x%02X, want 0x04", action)
	}
	if len(payload) != 253 {
		t.Errorf("payload len = %d, want 253", len(payload))
	}
	if !bytes.Equal(payload, payload253) {
		t.Errorf("payload mismatch")
	}
}

func TestReadPacketFastPathZeroPayload(t *testing.T) {
	mock := newMockConn()
	// Fast-path: action=0x00, length=2 (pduLen == consumed, no remaining)
	mock.readBuf.Write([]byte{0x00, 0x02})

	c := NewConn(mock, slog.Default())
	pktType, _, payload, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if pktType != PacketFastPath {
		t.Errorf("type = %d, want PacketFastPath", pktType)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestReadPacketTruncated(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"EmptyInput", nil},
		{"TPKTTruncatedHeader", []byte{0x03, 0x00}},
		{"TPKTTruncatedPayload", []byte{0x03, 0x00, 0x00, 0x0A, 0x01}},
		{"FastPathTruncatedLength", []byte{0x00}},
		{"FastPathTruncatedLength2", []byte{0x00, 0x80}},
		{"FastPathTruncatedPayload", []byte{0x00, 0x0A, 0x01}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockConn()
			mock.readBuf.Write(tt.data)
			c := NewConn(mock, slog.Default())
			_, _, _, err := c.ReadPacket()
			if err == nil {
				t.Fatal("expected error for truncated data")
			}
		})
	}
}

func TestSetTCPConn(t *testing.T) {
	mock1 := newMockConn()
	mock2 := newMockConn()

	c := NewConn(mock1, slog.Default())
	c.SetTCPConn(mock2)

	// Write should go to mock2 now
	mock2.readBuf.Write([]byte{0x03, 0x00, 0x00, 0x05, 0xAB})
	payload, err := c.Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if !bytes.Equal(payload, []byte{0xAB}) {
		t.Errorf("got %X, want AB", payload)
	}
}

func TestMaxSizePacket(t *testing.T) {
	mock := newMockConn()
	c := NewConn(mock, slog.Default())

	// Write maximum allowed payload (65535 - 4 = 65531 bytes)
	maxPayload := make([]byte, MaxPacketSize-HeaderSize)
	if err := c.Write(maxPayload); err != nil {
		t.Fatalf("Write max size packet error: %v", err)
	}

	// Verify header
	written := mock.writeBuf.Bytes()
	length := binary.BigEndian.Uint16(written[2:4])
	if length != MaxPacketSize {
		t.Errorf("length = %d, want %d", length, MaxPacketSize)
	}
}

func TestReadEOF(t *testing.T) {
	mock := newMockConn()
	mock.readErr = io.EOF
	c := NewConn(mock, slog.Default())

	_, err := c.Read()
	if err == nil {
		t.Fatal("expected error on EOF")
	}
}
