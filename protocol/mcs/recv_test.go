package mcs

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestDecodeSendDataIndication(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantCh    uint16
		wantData  []byte
		wantErr   bool
	}{
		{
			name: "valid with 1-byte length",
			data: func() []byte {
				payload := []byte{0xAA, 0xBB, 0xCC}
				buf := make([]byte, 7+len(payload))
				buf[0] = DomainMCSPDUSendDataIndication // type 0x68
				binary.BigEndian.PutUint16(buf[1:3], 0) // initiator (offset)
				binary.BigEndian.PutUint16(buf[3:5], 1003) // channelID
				buf[5] = 0x70                            // dataPriority + segmentation
				buf[6] = byte(len(payload))              // PER length (1 byte)
				copy(buf[7:], payload)
				return buf
			}(),
			wantCh:   1003,
			wantData: []byte{0xAA, 0xBB, 0xCC},
		},
		{
			name: "valid with 2-byte length",
			data: func() []byte {
				// 200-byte payload to exercise 2-byte PER length
				payload := bytes.Repeat([]byte{0x42}, 200)
				buf := make([]byte, 8+len(payload))
				buf[0] = DomainMCSPDUSendDataIndication
				binary.BigEndian.PutUint16(buf[1:3], 5) // initiator offset
				binary.BigEndian.PutUint16(buf[3:5], 1003)
				buf[5] = 0x70
				// PER 2-byte length: 0x80 | (200 >> 8), 200 & 0xFF
				buf[6] = 0x80 | byte(200>>8)
				buf[7] = byte(200 & 0xFF)
				copy(buf[8:], payload)
				return buf
			}(),
			wantCh:   1003,
			wantData: bytes.Repeat([]byte{0x42}, 200),
		},
		{
			name:    "wrong PDU type",
			data:    []byte{DomainMCSPDUSendDataRequest, 0x00, 0x00, 0x03, 0xEB, 0x70, 0x01, 0xFF},
			wantErr: true,
		},
		{
			name:    "truncated data - too short for header",
			data:    []byte{DomainMCSPDUSendDataIndication, 0x00, 0x00},
			wantErr: true,
		},
		{
			name: "truncated data - payload shorter than length",
			data: func() []byte {
				buf := make([]byte, 7)
				buf[0] = DomainMCSPDUSendDataIndication
				binary.BigEndian.PutUint16(buf[1:3], 0)
				binary.BigEndian.PutUint16(buf[3:5], 1003)
				buf[5] = 0x70
				buf[6] = 0x10 // claims 16 bytes but nothing follows
				return buf
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, userData, err := DecodeSendDataIndication(slog.Default(), tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ch != tt.wantCh {
				t.Errorf("channelID = %d, want %d", ch, tt.wantCh)
			}
			if !bytes.Equal(userData, tt.wantData) {
				t.Errorf("userData = %X, want %X", userData, tt.wantData)
			}
		})
	}
}

func TestReadPERLength(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantLen  int
		wantRead int
		wantErr  bool
	}{
		{
			name:     "1-byte length 0",
			data:     []byte{0x00},
			wantLen:  0,
			wantRead: 1,
		},
		{
			name:     "1-byte length 127",
			data:     []byte{0x7F},
			wantLen:  127,
			wantRead: 1,
		},
		{
			name:     "2-byte length 128",
			data:     []byte{0x80, 0x80},
			wantLen:  128,
			wantRead: 2,
		},
		{
			name:     "2-byte length 1000",
			data:     []byte{0x83, 0xE8}, // (0x03 << 8) | 0xE8 = 1000
			wantLen:  1000,
			wantRead: 2,
		},
		{
			name:    "empty data",
			data:    []byte{},
			wantErr: true,
		},
		{
			name:    "2-byte length truncated",
			data:    []byte{0x80},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			length, bytesRead, err := readPERLength(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if length != tt.wantLen {
				t.Errorf("length = %d, want %d", length, tt.wantLen)
			}
			if bytesRead != tt.wantRead {
				t.Errorf("bytesRead = %d, want %d", bytesRead, tt.wantRead)
			}
		})
	}
}
