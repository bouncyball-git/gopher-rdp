package sec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDecodeBasicSecurityHeader(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantFlags uint16
		wantHi    uint16
		wantRest  []byte
		wantErr   bool
	}{
		{
			name: "SEC_LICENSE_PKT",
			data: func() []byte {
				buf := make([]byte, 8)
				binary.LittleEndian.PutUint16(buf[0:2], LicensePkt)
				binary.LittleEndian.PutUint16(buf[2:4], 0)
				// 4 bytes of remaining data
				copy(buf[4:], []byte{0xAA, 0xBB, 0xCC, 0xDD})
				return buf
			}(),
			wantFlags: LicensePkt,
			wantHi:    0,
			wantRest:  []byte{0xAA, 0xBB, 0xCC, 0xDD},
		},
		{
			name: "SEC_INFO_PKT",
			data: func() []byte {
				buf := make([]byte, 4)
				binary.LittleEndian.PutUint16(buf[0:2], InfoPkt)
				binary.LittleEndian.PutUint16(buf[2:4], 0)
				return buf
			}(),
			wantFlags: InfoPkt,
			wantHi:    0,
			wantRest:  []byte{},
		},
		{
			name: "combined flags",
			data: func() []byte {
				buf := make([]byte, 6)
				binary.LittleEndian.PutUint16(buf[0:2], LicensePkt|Encrypt)
				binary.LittleEndian.PutUint16(buf[2:4], FlagshiValid)
				buf[4] = 0xFF
				buf[5] = 0xEE
				return buf
			}(),
			wantFlags: LicensePkt | Encrypt,
			wantHi:    FlagshiValid,
			wantRest:  []byte{0xFF, 0xEE},
		},
		{
			name:    "too short",
			data:    []byte{0x80, 0x00},
			wantErr: true,
		},
		{
			name:    "empty",
			data:    []byte{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr, rest, err := DecodeBasicSecurityHeader(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hdr.Flags != tt.wantFlags {
				t.Errorf("Flags = 0x%04X, want 0x%04X", hdr.Flags, tt.wantFlags)
			}
			if hdr.FlagsHi != tt.wantHi {
				t.Errorf("FlagsHi = 0x%04X, want 0x%04X", hdr.FlagsHi, tt.wantHi)
			}
			if !bytes.Equal(rest, tt.wantRest) {
				t.Errorf("rest = %X, want %X", rest, tt.wantRest)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := BasicSecurityHeader{
		Flags:   LicensePkt | SecureChecksum,
		FlagsHi: FlagshiValid,
	}
	encoded := EncodeBasicSecurityHeader(original)
	decoded, rest, err := DecodeBasicSecurityHeader(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Flags != original.Flags {
		t.Errorf("Flags = 0x%04X, want 0x%04X", decoded.Flags, original.Flags)
	}
	if decoded.FlagsHi != original.FlagsHi {
		t.Errorf("FlagsHi = 0x%04X, want 0x%04X", decoded.FlagsHi, original.FlagsHi)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %X, want empty", rest)
	}
}
