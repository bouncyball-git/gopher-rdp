package nla

import (
	"log/slog"
	"encoding/hex"
	"testing"
)

func TestNTOWFv2(t *testing.T) {
	// MS-NLMP section 4.2.4.1.1
	// User="User", Domain="Domain", Password="Password"
	got := ntowfv2("Password", "User", "Domain")
	want := "0c868a403bfd7a93a3001ef22ef02e3f"
	gotHex := hex.EncodeToString(got[:])
	if gotHex != want {
		t.Errorf("ntowfv2 = %s, want %s", gotHex, want)
	}
}

func TestNegotiateMessage(t *testing.T) {
	n := &ntlmClient{
		log:      slog.Default(),
		domain:   "Domain",
		username: "User",
		password: "Password",
	}
	msg := n.negotiate()

	// Check signature
	if string(msg[0:8]) != "NTLMSSP\x00" {
		t.Errorf("bad signature: %x", msg[0:8])
	}

	// Check message type = 1 (Negotiate)
	if msg[8] != 1 || msg[9] != 0 || msg[10] != 0 || msg[11] != 0 {
		t.Errorf("bad message type: %x", msg[8:12])
	}

	// Check length
	if len(msg) != 40 {
		t.Errorf("negotiate length = %d, want 40", len(msg))
	}

	// Check that negotiate message is saved
	if len(n.negotiateMsg) != 40 {
		t.Errorf("negotiateMsg not saved")
	}
}

func TestChallengeParseAndAuthenticate(t *testing.T) {
	// Build CHALLENGE_MESSAGE from MS-NLMP section 4.2.4.3 using raw bytes
	challengeBytes2 := make([]byte, 0, 104)
	// Signature
	challengeBytes2 = append(challengeBytes2, 'N', 'T', 'L', 'M', 'S', 'S', 'P', 0)
	// MessageType = 2
	challengeBytes2 = append(challengeBytes2, 0x02, 0x00, 0x00, 0x00)
	// TargetNameFields: Len=12, MaxLen=12, Offset=56
	challengeBytes2 = append(challengeBytes2, 0x0c, 0x00, 0x0c, 0x00, 0x38, 0x00, 0x00, 0x00)
	// NegotiateFlags
	challengeBytes2 = append(challengeBytes2, 0x33, 0x82, 0x8a, 0xe2)
	// ServerChallenge
	challengeBytes2 = append(challengeBytes2, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef)
	// Reserved
	challengeBytes2 = append(challengeBytes2, 0, 0, 0, 0, 0, 0, 0, 0)
	// TargetInfoFields: Len=36, MaxLen=36, Offset=68
	challengeBytes2 = append(challengeBytes2, 0x24, 0x00, 0x24, 0x00, 0x44, 0x00, 0x00, 0x00)
	// Version: 6.0.6000 Rev15
	challengeBytes2 = append(challengeBytes2, 0x06, 0x00, 0x70, 0x17, 0x00, 0x00, 0x00, 0x0f)
	// TargetName "Server" (UTF-16LE)
	challengeBytes2 = append(challengeBytes2, 0x53, 0x00, 0x65, 0x00, 0x72, 0x00, 0x76, 0x00, 0x65, 0x00, 0x72, 0x00)
	// TargetInfo AV_PAIRs
	// MsvAvNbDomainName = "Domain"
	challengeBytes2 = append(challengeBytes2, 0x02, 0x00, 0x0c, 0x00)
	challengeBytes2 = append(challengeBytes2, 0x44, 0x00, 0x6f, 0x00, 0x6d, 0x00, 0x61, 0x00, 0x69, 0x00, 0x6e, 0x00)
	// MsvAvNbComputerName = "Server"
	challengeBytes2 = append(challengeBytes2, 0x01, 0x00, 0x0c, 0x00)
	challengeBytes2 = append(challengeBytes2, 0x53, 0x00, 0x65, 0x00, 0x72, 0x00, 0x76, 0x00, 0x65, 0x00, 0x72, 0x00)
	// MsvAvEOL
	challengeBytes2 = append(challengeBytes2, 0x00, 0x00, 0x00, 0x00)

	n := &ntlmClient{
		log:      slog.Default(),
		domain:   "Domain",
		username: "User",
		password: "Password",
	}
	n.negotiate()

	authMsg, err := n.authenticate(challengeBytes2)
	if err != nil {
		t.Fatalf("authenticate error: %v", err)
	}

	// Verify the message is a valid Type 3
	if string(authMsg[0:8]) != "NTLMSSP\x00" {
		t.Errorf("bad signature: %x", authMsg[0:8])
	}
	if authMsg[8] != 3 {
		t.Errorf("message type = %d, want 3", authMsg[8])
	}

	// Verify server challenge was extracted correctly
	wantChallenge := "0123456789abcdef"
	gotChallenge := hex.EncodeToString(n.serverChallenge[:])
	if gotChallenge != wantChallenge {
		t.Errorf("serverChallenge = %s, want %s", gotChallenge, wantChallenge)
	}

	// Verify exported session key is 16 bytes and non-zero
	allZero := true
	for _, b := range n.exportedSessionKey {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("exportedSessionKey is all zeros")
	}

	// Verify MIC is present (bytes 72..88 should be non-zero)
	micAllZero := true
	for _, b := range authMsg[72:88] {
		if b != 0 {
			micAllZero = false
			break
		}
	}
	if micAllZero {
		t.Error("MIC field is all zeros — should have been computed")
	}
}

func TestModifyTargetInfo(t *testing.T) {
	// Build a simple AV_PAIR list: NbDomainName + NbComputerName + EOL
	avPairs := []byte{
		0x02, 0x00, 0x0c, 0x00, // MsvAvNbDomainName, len=12
		0x44, 0x00, 0x6f, 0x00, 0x6d, 0x00, 0x61, 0x00, 0x69, 0x00, 0x6e, 0x00,
		0x01, 0x00, 0x0c, 0x00, // MsvAvNbComputerName, len=12
		0x53, 0x00, 0x65, 0x00, 0x72, 0x00, 0x76, 0x00, 0x65, 0x00, 0x72, 0x00,
		0x00, 0x00, 0x00, 0x00, // MsvAvEOL
	}

	cbHash := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	modified := modifyTargetInfo(avPairs, cbHash, "TERMSRV/testhost")

	// Should have MsvAvFlags=0x02 inserted before EOL
	// Find the MsvAvFlags AV_PAIR
	foundFlags := false
	foundCB := false
	for off := 0; off+4 <= len(modified); {
		avID := uint16(modified[off]) | uint16(modified[off+1])<<8
		avLen := uint16(modified[off+2]) | uint16(modified[off+3])<<8
		if avID == avFlags {
			foundFlags = true
			if avLen != 4 {
				t.Errorf("MsvAvFlags length = %d, want 4", avLen)
			}
			val := uint32(modified[off+4]) | uint32(modified[off+5])<<8 | uint32(modified[off+6])<<16 | uint32(modified[off+7])<<24
			if val != 0x02 {
				t.Errorf("MsvAvFlags value = 0x%08X, want 0x00000002", val)
			}
		}
		if avID == avChannelBindings {
			foundCB = true
			if avLen != 16 {
				t.Errorf("MsvAvChannelBindings length = %d, want 16", avLen)
			}
		}
		if avID == avEOL {
			break
		}
		off += 4 + int(avLen)
	}
	if !foundFlags {
		t.Error("MsvAvFlags not found in modified TargetInfo")
	}
	if !foundCB {
		t.Error("MsvAvChannelBindings not found in modified TargetInfo")
	}
}

func TestGetAVTimestamp(t *testing.T) {
	// AV_PAIR list with timestamp
	avPairs := []byte{
		0x07, 0x00, 0x08, 0x00, // MsvAvTimestamp, len=8
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x00, 0x00, 0x00, 0x00, // MsvAvEOL
	}

	ts := getAVTimestamp(avPairs)
	want := [8]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if ts != want {
		t.Errorf("timestamp = %x, want %x", ts, want)
	}
}

func TestEncodeUTF16LE(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"A", "4100"},
		{"User", "5500730065007200"}, // U=0x0055, s=0x0073, e=0x0065, r=0x0072
	}
	for _, tt := range tests {
		got := encodeUTF16LE(tt.input)
		gotHex := hex.EncodeToString(got)
		if gotHex != tt.want {
			t.Errorf("encodeUTF16LE(%q) = %s, want %s", tt.input, gotHex, tt.want)
		}
	}
}

func TestEncodeUTF16LE_User(t *testing.T) {
	// Verify the exact bytes from MS-NLMP 4.2.1
	got := encodeUTF16LE("User")
	want := []byte{0x55, 0x00, 0x73, 0x00, 0x65, 0x00, 0x72, 0x00}
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("byte %d: got 0x%02x, want 0x%02x", i, got[i], want[i])
		}
	}
}
