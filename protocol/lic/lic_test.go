package lic

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestDecodePreamble(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		wantType    byte
		wantFlags   byte
		wantSize    uint16
		wantRestLen int
		wantErr     bool
	}{
		{
			name: "ERROR_ALERT preamble",
			data: func() []byte {
				buf := make([]byte, 16)
				buf[0] = ErrorAlert
				buf[1] = PreambleVersion30
				binary.LittleEndian.PutUint16(buf[2:4], 16)
				return buf
			}(),
			wantType:    ErrorAlert,
			wantFlags:   PreambleVersion30,
			wantSize:    16,
			wantRestLen: 12,
		},
		{
			name: "LICENSE_REQUEST preamble",
			data: func() []byte {
				buf := make([]byte, 8)
				buf[0] = LicenseRequest
				buf[1] = PreambleVersion30
				binary.LittleEndian.PutUint16(buf[2:4], 8)
				return buf
			}(),
			wantType:    LicenseRequest,
			wantFlags:   PreambleVersion30,
			wantSize:    8,
			wantRestLen: 4,
		},
		{
			name:    "too short - 2 bytes",
			data:    []byte{0xFF, 0x03},
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
			p, rest, err := DecodePreamble(slog.Default(), tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.MsgType != tt.wantType {
				t.Errorf("MsgType = 0x%02X, want 0x%02X", p.MsgType, tt.wantType)
			}
			if p.Flags != tt.wantFlags {
				t.Errorf("Flags = 0x%02X, want 0x%02X", p.Flags, tt.wantFlags)
			}
			if p.MsgSize != tt.wantSize {
				t.Errorf("MsgSize = %d, want %d", p.MsgSize, tt.wantSize)
			}
			if len(rest) != tt.wantRestLen {
				t.Errorf("rest len = %d, want %d", len(rest), tt.wantRestLen)
			}
		})
	}
}

func TestIsValidClientError(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "STATUS_VALID_CLIENT",
			data: func() []byte {
				buf := make([]byte, 16)
				buf[0] = ErrorAlert
				buf[1] = PreambleVersion30
				binary.LittleEndian.PutUint16(buf[2:4], 16)
				binary.LittleEndian.PutUint32(buf[4:8], StatusValidClient)
				binary.LittleEndian.PutUint32(buf[8:12], STNoTransition)
				// blob: type=0, len=0
				return buf
			}(),
			want: true,
		},
		{
			name: "ERR_NO_LICENSE",
			data: func() []byte {
				buf := make([]byte, 16)
				buf[0] = ErrorAlert
				buf[1] = PreambleVersion30
				binary.LittleEndian.PutUint16(buf[2:4], 16)
				binary.LittleEndian.PutUint32(buf[4:8], ErrNoLicense)
				binary.LittleEndian.PutUint32(buf[8:12], STTotalAbort)
				return buf
			}(),
			want: false,
		},
		{
			name: "LICENSE_REQUEST is not error alert",
			data: func() []byte {
				buf := make([]byte, 12)
				buf[0] = LicenseRequest
				buf[1] = PreambleVersion30
				binary.LittleEndian.PutUint16(buf[2:4], 12)
				binary.LittleEndian.PutUint32(buf[4:8], StatusValidClient) // wrong type though
				return buf
			}(),
			want: false,
		},
		{
			name: "too short",
			data: []byte{ErrorAlert, PreambleVersion30, 0x04, 0x00},
			want: false,
		},
		{
			name: "empty",
			data: []byte{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidClientError(tt.data)
			if got != tt.want {
				t.Errorf("IsValidClientError = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBlobRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		blob Blob
	}{
		{
			name: "empty data blob",
			blob: Blob{Type: BBDataBlob, Data: nil},
		},
		{
			name: "random blob with data",
			blob: Blob{Type: BBRandomBlob, Data: []byte{0x01, 0x02, 0x03, 0x04}},
		},
		{
			name: "certificate blob",
			blob: Blob{Type: BBCertificateBlob, Data: bytes.Repeat([]byte{0xAB}, 128)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeBlob(tt.blob)
			decoded, rest, err := DecodeBlob(encoded)
			if err != nil {
				t.Fatalf("DecodeBlob: %v", err)
			}
			if len(rest) != 0 {
				t.Errorf("rest len = %d, want 0", len(rest))
			}
			if decoded.Type != tt.blob.Type {
				t.Errorf("Type = 0x%04X, want 0x%04X", decoded.Type, tt.blob.Type)
			}
			if !bytes.Equal(decoded.Data, tt.blob.Data) {
				t.Errorf("Data mismatch")
			}
		})
	}
}

func TestDecodeBlob_Errors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too short", []byte{0x01, 0x00}},
		{"truncated data", func() []byte {
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint16(buf[0:2], BBDataBlob)
			binary.LittleEndian.PutUint16(buf[2:4], 10) // claims 10 bytes but none follow
			return buf
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := DecodeBlob(tt.data)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestEncodePreambleRoundTrip(t *testing.T) {
	encoded := EncodePreamble(NewLicenseRequest, PreambleVersion30, 100)
	p, rest, err := DecodePreamble(slog.Default(), encoded)
	if err != nil {
		t.Fatalf("DecodePreamble: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("rest len = %d, want 0", len(rest))
	}
	if p.MsgType != NewLicenseRequest {
		t.Errorf("MsgType = 0x%02X, want 0x%02X", p.MsgType, NewLicenseRequest)
	}
	if p.Flags != PreambleVersion30 {
		t.Errorf("Flags = 0x%02X, want 0x%02X", p.Flags, PreambleVersion30)
	}
	if p.MsgSize != 100 {
		t.Errorf("MsgSize = %d, want 100", p.MsgSize)
	}
}

// buildSyntheticLicenseRequest builds a minimal SERVER_LICENSE_REQUEST for testing.
func buildSyntheticLicenseRequest() []byte {
	var buf []byte

	// ServerRandom (32 bytes)
	serverRandom := bytes.Repeat([]byte{0xAA}, 32)
	buf = append(buf, serverRandom...)

	// ProductInfo: dwVersion(4) + cbCompanyName(4) + Company + cbProductId(4) + Product
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], 0x00030000) // version
	buf = append(buf, tmp[:]...)
	company := []byte("T\x00e\x00s\x00t\x00\x00\x00") // UTF-16LE "Test\0"
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(company)))
	buf = append(buf, tmp[:]...)
	buf = append(buf, company...)
	product := []byte("P\x00r\x00o\x00d\x00\x00\x00") // UTF-16LE "Prod\0"
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(product)))
	buf = append(buf, tmp[:]...)
	buf = append(buf, product...)

	// KeyExchangeList blob
	keyExchData := make([]byte, 4)
	binary.LittleEndian.PutUint32(keyExchData, KeyExchAlgRSA)
	buf = append(buf, EncodeBlob(Blob{Type: BBKeyExchAlgBlob, Data: keyExchData})...)

	// ServerCertificate blob (empty — just enough to parse)
	buf = append(buf, EncodeBlob(Blob{Type: BBCertificateBlob, Data: []byte{0x01, 0x00, 0x00, 0x00}})...)

	// ScopeList: count=1 + 1 scope blob
	binary.LittleEndian.PutUint32(tmp[:], 1)
	buf = append(buf, tmp[:]...)
	buf = append(buf, EncodeBlob(Blob{Type: BBScopeBlob, Data: []byte("scope1")})...)

	return buf
}

func TestDecodeLicenseRequest(t *testing.T) {
	data := buildSyntheticLicenseRequest()
	lr, err := DecodeLicenseRequest(data)
	if err != nil {
		t.Fatalf("DecodeLicenseRequest: %v", err)
	}

	if len(lr.ServerRandom) != 32 {
		t.Errorf("ServerRandom len = %d, want 32", len(lr.ServerRandom))
	}
	for _, b := range lr.ServerRandom {
		if b != 0xAA {
			t.Errorf("ServerRandom byte = 0x%02X, want 0xAA", b)
			break
		}
	}

	if lr.ProductInfo.Version != 0x00030000 {
		t.Errorf("ProductInfo.Version = 0x%08X, want 0x00030000", lr.ProductInfo.Version)
	}

	if lr.KeyExchangeList.Type != BBKeyExchAlgBlob {
		t.Errorf("KeyExchangeList.Type = 0x%04X, want 0x%04X", lr.KeyExchangeList.Type, BBKeyExchAlgBlob)
	}

	if len(lr.ScopeList) != 1 {
		t.Fatalf("ScopeList len = %d, want 1", len(lr.ScopeList))
	}
	if !bytes.Equal(lr.ScopeList[0].Data, []byte("scope1")) {
		t.Errorf("ScopeList[0].Data = %q, want %q", lr.ScopeList[0].Data, "scope1")
	}
}

func TestDecodeLicenseRequest_TooShort(t *testing.T) {
	_, err := DecodeLicenseRequest([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestEncodeNewLicenseRequest(t *testing.T) {
	clientRandom := bytes.Repeat([]byte{0xBB}, 32)
	encPreMaster := bytes.Repeat([]byte{0xCC}, 64)

	data := EncodeNewLicenseRequest(clientRandom, encPreMaster, "user", "host")

	// Verify preamble
	if data[0] != NewLicenseRequest {
		t.Errorf("MsgType = 0x%02X, want 0x%02X", data[0], NewLicenseRequest)
	}
	if data[1] != PreambleVersion30 {
		t.Errorf("Flags = 0x%02X, want 0x%02X", data[1], PreambleVersion30)
	}
	msgSize := binary.LittleEndian.Uint16(data[2:4])
	if int(msgSize) != len(data) {
		t.Errorf("MsgSize = %d, actual len = %d", msgSize, len(data))
	}

	// Verify KeyExchangeAlg
	off := 4
	alg := binary.LittleEndian.Uint32(data[off : off+4])
	if alg != KeyExchAlgRSA {
		t.Errorf("KeyExchAlg = 0x%08X, want 0x%08X", alg, KeyExchAlgRSA)
	}

	// Verify ClientRandom at offset 12
	off = 12
	if !bytes.Equal(data[off:off+32], clientRandom) {
		t.Error("ClientRandom mismatch")
	}
}

func TestDecodePlatformChallenge(t *testing.T) {
	// Build synthetic PLATFORM_CHALLENGE
	var buf []byte

	// ConnectFlags
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], 0x00000001)
	buf = append(buf, tmp[:]...)

	// EncryptedChallenge blob
	challenge := bytes.Repeat([]byte{0xDD}, 16)
	buf = append(buf, EncodeBlob(Blob{Type: BBEncryptedBlob, Data: challenge})...)

	// MACData (16 bytes)
	mac := bytes.Repeat([]byte{0xEE}, 16)
	buf = append(buf, mac...)

	pc, err := DecodePlatformChallenge(buf)
	if err != nil {
		t.Fatalf("DecodePlatformChallenge: %v", err)
	}

	if pc.ConnectFlags != 1 {
		t.Errorf("ConnectFlags = %d, want 1", pc.ConnectFlags)
	}
	if !bytes.Equal(pc.EncryptedChallenge.Data, challenge) {
		t.Error("EncryptedChallenge data mismatch")
	}
	if !bytes.Equal(pc.MACData, mac) {
		t.Error("MACData mismatch")
	}
}

func TestDecodeErrorAlert(t *testing.T) {
	tests := []struct {
		name       string
		errorCode  uint32
		transition uint32
	}{
		{"STATUS_VALID_CLIENT", StatusValidClient, STNoTransition},
		{"ERR_NO_LICENSE", ErrNoLicense, STTotalAbort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 12) // errorCode + transition + empty blob(4)
			binary.LittleEndian.PutUint32(buf[0:4], tt.errorCode)
			binary.LittleEndian.PutUint32(buf[4:8], tt.transition)
			// Empty blob: type=0, len=0
			binary.LittleEndian.PutUint16(buf[8:10], 0)
			binary.LittleEndian.PutUint16(buf[10:12], 0)

			ea, err := DecodeErrorAlert(buf)
			if err != nil {
				t.Fatalf("DecodeErrorAlert: %v", err)
			}
			if ea.ErrorCode != tt.errorCode {
				t.Errorf("ErrorCode = 0x%08X, want 0x%08X", ea.ErrorCode, tt.errorCode)
			}
			if ea.StateTransition != tt.transition {
				t.Errorf("StateTransition = 0x%08X, want 0x%08X", ea.StateTransition, tt.transition)
			}
		})
	}
}

func TestEncodePlatformChallengeResponse(t *testing.T) {
	encResp := []byte{0x01, 0x02, 0x03, 0x04}
	encHWID := bytes.Repeat([]byte{0xAA}, 20)
	mac := bytes.Repeat([]byte{0xBB}, 16)

	data := EncodePlatformChallengeResponse(encResp, encHWID, mac)

	// Verify preamble
	if data[0] != PlatformChallengeResponse {
		t.Errorf("MsgType = 0x%02X, want 0x%02X", data[0], PlatformChallengeResponse)
	}
	msgSize := binary.LittleEndian.Uint16(data[2:4])
	if int(msgSize) != len(data) {
		t.Errorf("MsgSize = %d, actual len = %d", msgSize, len(data))
	}

	// Verify MAC is at the end
	if !bytes.Equal(data[len(data)-16:], mac) {
		t.Error("MACData at end mismatch")
	}
}
