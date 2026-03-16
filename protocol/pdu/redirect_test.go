package pdu

import (
	"encoding/binary"
	"testing"
)

func TestDecodeRedirectPDU_Enhanced(t *testing.T) {
	// Build a synthetic enhanced redirect PDU payload (after share control header).
	// Layout: pad(2) + redirectIdentifier(u16) + totalLength(u16) + sessionID(u32) +
	//         flags(u32) + [flag-driven fields]

	server := encodeTestUTF16("192.168.1.50")
	domain := encodeTestUTF16("CORP")
	username := encodeTestUTF16("admin")
	lbInfo := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	flags := LBTargetNetAddress | LBLoadBalanceInfo | LBUsername | LBDomain

	var buf []byte
	buf = append(buf, 0, 0) // pad
	buf = leU16Append(buf, 0x0400)  // redirectIdentifier
	buf = leU16Append(buf, 0)       // totalLength (ignored by decoder)
	buf = leU32Append(buf, 42)      // sessionID
	buf = leU32Append(buf, flags)

	// TargetNetAddress
	buf = leU32Append(buf, uint32(len(server)))
	buf = append(buf, server...)

	// LoadBalanceInfo
	buf = leU32Append(buf, uint32(len(lbInfo)))
	buf = append(buf, lbInfo...)

	// Username
	buf = leU32Append(buf, uint32(len(username)))
	buf = append(buf, username...)

	// Domain
	buf = leU32Append(buf, uint32(len(domain)))
	buf = append(buf, domain...)

	ri, err := DecodeRedirectPDU(buf, true)
	if err != nil {
		t.Fatalf("DecodeRedirectPDU: %v", err)
	}

	if ri.SessionID != 42 {
		t.Errorf("SessionID = %d, want 42", ri.SessionID)
	}
	if ri.Server != "192.168.1.50" {
		t.Errorf("Server = %q, want %q", ri.Server, "192.168.1.50")
	}
	if ri.Domain != "CORP" {
		t.Errorf("Domain = %q, want %q", ri.Domain, "CORP")
	}
	if ri.Username != "admin" {
		t.Errorf("Username = %q, want %q", ri.Username, "admin")
	}
	if len(ri.LoadBalanceInfo) != 4 || ri.LoadBalanceInfo[0] != 0xDE {
		t.Errorf("LoadBalanceInfo = %x, want deadbeef", ri.LoadBalanceInfo)
	}
}

func TestDecodeRedirectPDU_Standard(t *testing.T) {
	server := encodeTestUTF16("10.0.0.1")
	flags := LBTargetNetAddress

	var buf []byte
	buf = append(buf, 0, 0) // pad
	buf = leU32Append(buf, flags)
	buf = leU32Append(buf, uint32(len(server)))
	buf = append(buf, server...)

	ri, err := DecodeRedirectPDU(buf, false)
	if err != nil {
		t.Fatalf("DecodeRedirectPDU: %v", err)
	}
	if ri.Server != "10.0.0.1" {
		t.Errorf("Server = %q, want %q", ri.Server, "10.0.0.1")
	}
	if ri.SessionID != 0 {
		t.Errorf("SessionID = %d, want 0", ri.SessionID)
	}
}

func TestDecodeRedirectPDU_FQDNOverridesNetAddress(t *testing.T) {
	netAddr := encodeTestUTF16("10.0.0.1")
	fqdn := encodeTestUTF16("rdsh01.corp.local")
	flags := LBTargetNetAddress | LBTargetFQDN

	var buf []byte
	buf = append(buf, 0, 0)
	buf = leU32Append(buf, flags)
	buf = leU32Append(buf, uint32(len(netAddr)))
	buf = append(buf, netAddr...)
	buf = leU32Append(buf, uint32(len(fqdn)))
	buf = append(buf, fqdn...)

	ri, err := DecodeRedirectPDU(buf, false)
	if err != nil {
		t.Fatalf("DecodeRedirectPDU: %v", err)
	}
	if ri.Server != "rdsh01.corp.local" {
		t.Errorf("Server = %q, want FQDN override", ri.Server)
	}
}

func TestDecodeRedirectPDU_NoRedirect(t *testing.T) {
	flags := LBNoRedirect

	var buf []byte
	buf = append(buf, 0, 0)
	buf = leU32Append(buf, flags)

	ri, err := DecodeRedirectPDU(buf, false)
	if err != nil {
		t.Fatalf("DecodeRedirectPDU: %v", err)
	}
	if ri.Server != "" {
		t.Errorf("Server = %q, want empty for LB_NOREDIRECT", ri.Server)
	}
}

func TestDecodeRedirectPDU_Truncated(t *testing.T) {
	// Too short for flags
	_, err := DecodeRedirectPDU([]byte{0, 0}, false)
	if err == nil {
		t.Error("expected error for truncated PDU")
	}
}

// encodeTestUTF16 encodes a string as null-terminated UTF-16LE bytes.
func encodeTestUTF16(s string) []byte {
	runes := []rune(s)
	buf := make([]byte, (len(runes)+1)*2) // +1 for null terminator
	for i, r := range runes {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(r))
	}
	// Last 2 bytes are already 0 (null terminator)
	return buf
}

func leU16Append(b []byte, v uint16) []byte {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	return append(b, tmp[:]...)
}

func leU32Append(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}
