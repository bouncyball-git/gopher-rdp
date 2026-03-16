package nla

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/binary"
	"testing"
)

func TestTSRequestMarshalUnmarshal(t *testing.T) {
	req := tsRequest{
		Version:    6,
		NegoTokens: []negoToken{{Data: []byte("test-token")}},
	}

	data, err := asn1.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded tsRequest
	if _, err := asn1.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Version != 6 {
		t.Errorf("version = %d, want 6", decoded.Version)
	}
	if len(decoded.NegoTokens) != 1 {
		t.Fatalf("negoTokens count = %d, want 1", len(decoded.NegoTokens))
	}
	if string(decoded.NegoTokens[0].Data) != "test-token" {
		t.Errorf("token data = %q, want %q", decoded.NegoTokens[0].Data, "test-token")
	}
}

func TestTSRequestWithPubKeyAuth(t *testing.T) {
	req := tsRequest{
		Version:     6,
		PubKeyAuth:  []byte{1, 2, 3, 4},
		ClientNonce: []byte{5, 6, 7, 8},
	}

	data, err := asn1.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded tsRequest
	if _, err := asn1.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !bytes.Equal(decoded.PubKeyAuth, []byte{1, 2, 3, 4}) {
		t.Errorf("pubKeyAuth = %x, want 01020304", decoded.PubKeyAuth)
	}
	if !bytes.Equal(decoded.ClientNonce, []byte{5, 6, 7, 8}) {
		t.Errorf("clientNonce = %x, want 05060708", decoded.ClientNonce)
	}
}

func TestTSRequestWithAuthInfo(t *testing.T) {
	req := tsRequest{
		Version:  6,
		AuthInfo: []byte("sealed-credentials"),
	}

	data, err := asn1.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded tsRequest
	if _, err := asn1.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if string(decoded.AuthInfo) != "sealed-credentials" {
		t.Errorf("authInfo = %q, want %q", decoded.AuthInfo, "sealed-credentials")
	}
}

func TestTSCredentialsMarshal(t *testing.T) {
	passCreds := tsPasswordCreds{
		DomainName: encodeUTF16LE("Domain"),
		UserName:   encodeUTF16LE("User"),
		Password:   encodeUTF16LE("Password"),
	}
	passCredsBytes, err := asn1.Marshal(passCreds)
	if err != nil {
		t.Fatalf("marshal TSPasswordCreds: %v", err)
	}

	creds := tsCredentials{
		CredType:    1,
		Credentials: passCredsBytes,
	}
	credsBytes, err := asn1.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal TSCredentials: %v", err)
	}

	// Should be non-empty and decodable
	var decoded tsCredentials
	if _, err := asn1.Unmarshal(credsBytes, &decoded); err != nil {
		t.Fatalf("unmarshal TSCredentials: %v", err)
	}
	if decoded.CredType != 1 {
		t.Errorf("credType = %d, want 1", decoded.CredType)
	}

	var decodedPass tsPasswordCreds
	if _, err := asn1.Unmarshal(decoded.Credentials, &decodedPass); err != nil {
		t.Fatalf("unmarshal TSPasswordCreds: %v", err)
	}
	if !bytes.Equal(decodedPass.DomainName, encodeUTF16LE("Domain")) {
		t.Errorf("domain mismatch")
	}
	if !bytes.Equal(decodedPass.UserName, encodeUTF16LE("User")) {
		t.Errorf("username mismatch")
	}
}

func TestReadWriteTSRequest(t *testing.T) {
	req := &tsRequest{
		Version:    6,
		NegoTokens: []negoToken{{Data: []byte("hello")}},
	}

	// Write to buffer
	var buf bytes.Buffer
	if err := sendTSRequest(&buf, req); err != nil {
		t.Fatalf("send error: %v", err)
	}

	// Read back
	decoded, err := readTSRequest(&buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	if decoded.Version != 6 {
		t.Errorf("version = %d, want 6", decoded.Version)
	}
	if len(decoded.NegoTokens) != 1 {
		t.Fatalf("negoTokens count = %d, want 1", len(decoded.NegoTokens))
	}
	if string(decoded.NegoTokens[0].Data) != "hello" {
		t.Errorf("token = %q, want %q", decoded.NegoTokens[0].Data, "hello")
	}
}

func TestDERLengthEncoding(t *testing.T) {
	tests := []struct {
		length int
		want   []byte
	}{
		{0, []byte{0}},
		{127, []byte{127}},
		{128, []byte{0x81, 128}},
		{255, []byte{0x81, 255}},
		{256, []byte{0x82, 1, 0}},
		{1000, []byte{0x82, 3, 232}},
	}

	for _, tt := range tests {
		got := encodeDERLength(tt.length)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("encodeDERLength(%d) = %v, want %v", tt.length, got, tt.want)
		}
	}
}

func TestReadDERLength(t *testing.T) {
	tests := []struct {
		input  []byte
		want   int
	}{
		{[]byte{0}, 0},
		{[]byte{127}, 127},
		{[]byte{0x81, 128}, 128},
		{[]byte{0x81, 255}, 255},
		{[]byte{0x82, 1, 0}, 256},
		{[]byte{0x82, 3, 232}, 1000},
	}

	for _, tt := range tests {
		r := bytes.NewReader(tt.input)
		got, err := readDERLength(r)
		if err != nil {
			t.Errorf("readDERLength(%v) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("readDERLength(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestComputeChannelBindingsHash(t *testing.T) {
	fakeCert := []byte("test certificate DER")
	hash := computeChannelBindingsHash(fakeCert)

	// Hash should be 16 bytes (MD5) and non-zero
	allZero := true
	for _, b := range hash {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("channel bindings hash is all zeros")
	}

	// Same input should produce same output (deterministic)
	hash2 := computeChannelBindingsHash(fakeCert)
	if hash != hash2 {
		t.Error("channel bindings hash not deterministic")
	}

	// Different input should produce different output
	hash3 := computeChannelBindingsHash([]byte("different cert"))
	if hash == hash3 {
		t.Error("different certs produced same channel bindings hash")
	}

	// Verify the gss_channel_bindings_struct serialization (RFC 2743 §1.1.6):
	// MD5 input = 5 x LE32(0,0,0,0,appDataLen) + "tls-server-end-point:" + SHA256(cert)
	// NOT the full SEC_CHANNEL_BINDINGS struct (which has 8 fields including offsets)
	certHash := sha256.Sum256(fakeCert)
	prefix := []byte("tls-server-end-point:")
	appDataLen := uint32(len(prefix) + len(certHash))

	h := md5.New()
	var buf [4]byte
	for i := 0; i < 4; i++ { // 4 zero LE32 fields
		h.Write(buf[:])
	}
	binary.LittleEndian.PutUint32(buf[:], appDataLen)
	h.Write(buf[:])
	h.Write(prefix)
	h.Write(certHash[:])
	var expected [16]byte
	copy(expected[:], h.Sum(nil))

	if hash != expected {
		t.Errorf("channel bindings hash mismatch:\n  got:  %x\n  want: %x", hash, expected)
	}
}

func TestTSRequestErrorCode(t *testing.T) {
	req := tsRequest{
		Version:   6,
		ErrorCode: 0x80090308, // SEC_E_INVALID_TOKEN
	}

	data, err := asn1.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded tsRequest
	if _, err := asn1.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.ErrorCode != 0x80090308 {
		t.Errorf("errorCode = 0x%08X, want 0x80090308", decoded.ErrorCode)
	}
}
