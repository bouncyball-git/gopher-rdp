package mcs

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"testing"

	"github.com/bouncyball-git/gopher-rdp/protocol/ber"
)

func TestConnectInitialEncode(t *testing.T) {
	ci := &ConnectInitial{
		CallingDomainSelector: []byte{1},
		CalledDomainSelector:  []byte{1},
		UpwardFlag:            true,
		TargetParameters: ber.DomainParameters{
			MaxChannelIDs:   34,
			MaxUserIDs:      2,
			MaxTokenIDs:     0,
			NumPriorities:   1,
			MinThroughput:   0,
			MaxHeight:       1,
			MaxMCSPDUSize:   65535,
			ProtocolVersion: 2,
		},
		MinimumParameters: ber.DomainParameters{
			MaxChannelIDs:   1,
			MaxUserIDs:      1,
			MaxTokenIDs:     1,
			NumPriorities:   1,
			MinThroughput:   0,
			MaxHeight:       1,
			MaxMCSPDUSize:   1056,
			ProtocolVersion: 2,
		},
		MaximumParameters: ber.DomainParameters{
			MaxChannelIDs:   65535,
			MaxUserIDs:      64535,
			MaxTokenIDs:     65535,
			NumPriorities:   1,
			MinThroughput:   0,
			MaxHeight:       1,
			MaxMCSPDUSize:   65535,
			ProtocolVersion: 2,
		},
		UserData: []byte{0x04, 0x01, 0x01}, // dummy GCC data
	}

	data := ci.Encode()

	// First byte should be Application 101 Constructed tag (long form)
	// 0x7F = Application | Constructed | 0x1F (long form)
	if data[0] != 0x7F {
		t.Errorf("first byte = 0x%02X, want 0x7F", data[0])
	}
	// Second byte: tag number 101 = 0x65
	if data[1] != 0x65 {
		t.Errorf("tag number = 0x%02X, want 0x65 (101)", data[1])
	}

	// Verify we can parse back the tag
	r := bytes.NewReader(data)
	class, constructed, tag, err := ber.ReadTag(r)
	if err != nil {
		t.Fatalf("ReadTag error: %v", err)
	}
	if class != ber.ClassApplication || !constructed || tag != TagConnectInitial {
		t.Errorf("tag: class=0x%02X constructed=%v tag=%d, want Application/true/101",
			class, constructed, tag)
	}

	// Read the outer length
	_, err = ber.ReadLength(r)
	if err != nil {
		t.Fatalf("ReadLength error: %v", err)
	}
}

func TestConnectInitialEncodeNotEmpty(t *testing.T) {
	ci := &ConnectInitial{
		CallingDomainSelector: []byte{1},
		CalledDomainSelector:  []byte{1},
		UpwardFlag:            true,
		TargetParameters:      ber.DomainParameters{MaxMCSPDUSize: 65535},
		MinimumParameters:     ber.DomainParameters{MaxMCSPDUSize: 1056},
		MaximumParameters:     ber.DomainParameters{MaxMCSPDUSize: 65535},
		UserData:              []byte{0x01, 0x02, 0x03},
	}

	data := ci.Encode()
	if len(data) < 20 {
		t.Errorf("encoded data too short: %d bytes", len(data))
	}
}

func TestConnectResponseDecodeRoundTrip(t *testing.T) {
	// Build a synthetic Connect Response
	var content bytes.Buffer

	// Result (ENUMERATED, value 0 = rt-successful)
	ber.WriteEnumerated(&content, 0)

	// Called Connect ID (INTEGER, value 0)
	ber.WriteInteger(&content, 0)

	// Domain Parameters (SEQUENCE)
	ber.WriteDomainParameters(&content, ber.DomainParameters{
		MaxChannelIDs:   34,
		MaxUserIDs:      2,
		MaxTokenIDs:     0,
		NumPriorities:   1,
		MinThroughput:   0,
		MaxHeight:       1,
		MaxMCSPDUSize:   65535,
		ProtocolVersion: 2,
	})

	// User Data (OCTET STRING)
	userData := []byte{0x01, 0x02, 0x03, 0x04}
	ber.WriteOctetString(&content, userData)

	// Wrap with Application 102 tag
	var full bytes.Buffer
	ber.WriteTag(&full, ber.ClassApplication, ber.Constructed, TagConnectResponse)
	ber.WriteLength(&full, content.Len())
	full.Write(content.Bytes())

	cr, err := DecodeConnectResponse(slog.Default(), full.Bytes())
	if err != nil {
		t.Fatalf("DecodeConnectResponse error: %v", err)
	}
	if cr.Result != 0 {
		t.Errorf("result = %d, want 0", cr.Result)
	}
	if cr.CalledConnectID != 0 {
		t.Errorf("calledConnectID = %d, want 0", cr.CalledConnectID)
	}
	if cr.DomainParameters.MaxMCSPDUSize != 65535 {
		t.Errorf("maxMCSPDUSize = %d, want 65535", cr.DomainParameters.MaxMCSPDUSize)
	}
	if !bytes.Equal(cr.UserData, userData) {
		t.Errorf("userData = %X, want %X", cr.UserData, userData)
	}
}

func TestDecodeConnectResponseBadTag(t *testing.T) {
	// Use wrong tag (Application 101 instead of 102)
	var buf bytes.Buffer
	ber.WriteTag(&buf, ber.ClassApplication, ber.Constructed, TagConnectInitial)
	ber.WriteLength(&buf, 0)

	_, err := DecodeConnectResponse(slog.Default(), buf.Bytes())
	if err == nil {
		t.Error("expected error for wrong tag")
	}
}

func TestErectDomainRequest(t *testing.T) {
	data := EncodeErectDomainRequest()
	if len(data) != 5 {
		t.Fatalf("expected 5 bytes, got %d", len(data))
	}
	if data[0] != DomainMCSPDUErectDomainRequest {
		t.Errorf("type = 0x%02X, want 0x%02X", data[0], DomainMCSPDUErectDomainRequest)
	}
}

func TestAttachUserRequest(t *testing.T) {
	data := EncodeAttachUserRequest()
	if len(data) != 1 {
		t.Fatalf("expected 1 byte, got %d", len(data))
	}
	if data[0] != DomainMCSPDUAttachUserRequest {
		t.Errorf("type = 0x%02X, want 0x%02X", data[0], DomainMCSPDUAttachUserRequest)
	}
}

func TestDecodeAttachUserConfirm(t *testing.T) {
	// Build a valid Attach User Confirm
	// Byte 0: 0x2E = 001011 10 (type bits + result present + userID present)
	// Byte 1: 0x00 = result success
	// Bytes 2-3: user ID offset (0 => actual ID = 1001)
	data := []byte{0x2E, 0x00, 0x00, 0x00}
	userID, err := DecodeAttachUserConfirm(slog.Default(), data)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if userID != 1001 {
		t.Errorf("userID = %d, want 1001", userID)
	}
}

func TestDecodeAttachUserConfirmWithOffset(t *testing.T) {
	// User ID offset = 5, so actual = 1006
	data := []byte{0x2E, 0x00, 0x00, 0x05}
	userID, err := DecodeAttachUserConfirm(slog.Default(), data)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if userID != 1006 {
		t.Errorf("userID = %d, want 1006", userID)
	}
}

func TestDecodeAttachUserConfirmFailure(t *testing.T) {
	data := []byte{0x2E, 0x01, 0x00, 0x00} // result = 1 (failure)
	_, err := DecodeAttachUserConfirm(slog.Default(), data)
	if err == nil {
		t.Error("expected error for non-zero result")
	}
}

func TestDecodeAttachUserConfirmTooShort(t *testing.T) {
	_, err := DecodeAttachUserConfirm(slog.Default(), []byte{0x2E, 0x00})
	if err == nil {
		t.Error("expected error for too-short data")
	}
}

func TestDecodeAttachUserConfirmWrongType(t *testing.T) {
	_, err := DecodeAttachUserConfirm(slog.Default(), []byte{0x04, 0x00, 0x00, 0x00})
	if err == nil {
		t.Error("expected error for wrong PDU type")
	}
}

func TestChannelJoinRequestEncode(t *testing.T) {
	data := EncodeChannelJoinRequest(1001, 1003)
	if len(data) != 5 {
		t.Fatalf("expected 5 bytes, got %d", len(data))
	}
	if data[0] != DomainMCSPDUChannelJoinRequest {
		t.Errorf("type = 0x%02X, want 0x%02X", data[0], DomainMCSPDUChannelJoinRequest)
	}
	// User ID offset = 1001 - 1001 = 0
	userOffset := binary.BigEndian.Uint16(data[1:3])
	if userOffset != 0 {
		t.Errorf("user offset = %d, want 0", userOffset)
	}
	// Channel ID
	channelID := binary.BigEndian.Uint16(data[3:5])
	if channelID != 1003 {
		t.Errorf("channel ID = %d, want 1003", channelID)
	}
}

func TestChannelJoinRequestEncodeWithOffset(t *testing.T) {
	data := EncodeChannelJoinRequest(1005, 2000)
	userOffset := binary.BigEndian.Uint16(data[1:3])
	if userOffset != 4 { // 1005 - 1001
		t.Errorf("user offset = %d, want 4", userOffset)
	}
	channelID := binary.BigEndian.Uint16(data[3:5])
	if channelID != 2000 {
		t.Errorf("channel ID = %d, want 2000", channelID)
	}
}

func TestDecodeChannelJoinConfirm(t *testing.T) {
	// Build: type(1) + result(1) + initiator(2) + requested(2) + channelID(2)
	data := []byte{
		0x3E,       // Channel Join Confirm (001111 10)
		0x00,       // result = success
		0x00, 0x00, // initiator
		0x03, 0xEB, // requested = 1003
		0x03, 0xEB, // channelID = 1003
	}
	channelID, err := DecodeChannelJoinConfirm(slog.Default(), data)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if channelID != 1003 {
		t.Errorf("channelID = %d, want 1003", channelID)
	}
}

func TestDecodeChannelJoinConfirmFailure(t *testing.T) {
	data := []byte{0x3E, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	_, err := DecodeChannelJoinConfirm(slog.Default(), data)
	if err == nil {
		t.Error("expected error for non-zero result")
	}
}

func TestDecodeChannelJoinConfirmTooShort(t *testing.T) {
	_, err := DecodeChannelJoinConfirm(slog.Default(), []byte{0x3E, 0x00, 0x00})
	if err == nil {
		t.Error("expected error for too-short data")
	}
}

func TestGCCConferenceCreateRequest(t *testing.T) {
	clientData := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	result := EncodeGCCConferenceCreateRequest(clientData)

	// Should start with the T.124 PER header
	if result[0] != 0x00 || result[1] != 0x05 {
		t.Errorf("PER header start: got %02X %02X, want 00 05", result[0], result[1])
	}

	// Should contain "Duca" key
	if !bytes.Contains(result, gccH221ClientKey) {
		t.Error("missing Duca key in GCC request")
	}

	// Should contain the client data at the end
	if !bytes.HasSuffix(result, clientData) {
		t.Error("client data not at end of GCC request")
	}
}

func TestGCCConferenceCreateResponseDecode(t *testing.T) {
	// Build a minimal GCC response with "McDn" key
	var buf bytes.Buffer
	// Some PER header bytes (simplified)
	buf.Write([]byte{0x00, 0x05, 0x00, 0x14, 0x7C, 0x00, 0x01, 0x2A, 0x14, 0x76, 0x0A, 0x01, 0x01, 0x00, 0x01, 0xC0, 0x00})
	buf.Write(gccH221ServerKey) // "McDn"
	// PER length determinant (1 byte for values < 128)
	buf.WriteByte(4)
	buf.Write([]byte{0x01, 0x02, 0x03, 0x04})

	userData, err := DecodeGCCConferenceCreateResponse(buf.Bytes())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := []byte{0x01, 0x02, 0x03, 0x04}
	if !bytes.Equal(userData, want) {
		t.Errorf("userData = %X, want %X", userData, want)
	}
}

func TestGCCConferenceCreateResponseNoMcDn(t *testing.T) {
	_, err := DecodeGCCConferenceCreateResponse([]byte{0x00, 0x05, 0x00, 0x14, 0x7C, 0x00, 0x01, 0x2A, 0x14})
	if err == nil {
		t.Error("expected error when McDn key is missing")
	}
}

func TestGCCConferenceCreateResponseTooShort(t *testing.T) {
	_, err := DecodeGCCConferenceCreateResponse([]byte{0x01, 0x02})
	if err == nil {
		t.Error("expected error for too-short response")
	}
}

func TestEncodeSendDataRequest(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	data := EncodeSendDataRequest(1001, 1003, payload)

	if data[0] != DomainMCSPDUSendDataRequest {
		t.Errorf("type = 0x%02X, want 0x%02X", data[0], DomainMCSPDUSendDataRequest)
	}

	// Initiator = 1001 - 1001 = 0
	initiator := binary.BigEndian.Uint16(data[1:3])
	if initiator != 0 {
		t.Errorf("initiator = %d, want 0", initiator)
	}

	// Channel = 1003
	channel := binary.BigEndian.Uint16(data[3:5])
	if channel != 1003 {
		t.Errorf("channel = %d, want 1003", channel)
	}

	// Payload should be at the end
	if !bytes.HasSuffix(data, payload) {
		t.Error("payload not found at end of send data request")
	}
}
