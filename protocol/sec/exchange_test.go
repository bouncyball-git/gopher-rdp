package sec

import (
	"encoding/binary"
	"testing"
)

func TestEncodeSecurityExchange(t *testing.T) {
	encRandom := make([]byte, 72) // 64-byte encrypted random + 8 zero bytes
	encRandom[0] = 0xAA
	encRandom[63] = 0xBB

	data := EncodeSecurityExchange(encRandom)

	// Total: 4 (sec header) + 4 (length) + 72 (data) = 80
	if len(data) != 80 {
		t.Fatalf("length = %d, want 80", len(data))
	}

	// Check flags
	flags := binary.LittleEndian.Uint16(data[0:2])
	if flags != ExchangePkt {
		t.Errorf("flags = 0x%04X, want 0x%04X", flags, ExchangePkt)
	}

	flagsHi := binary.LittleEndian.Uint16(data[2:4])
	if flagsHi != 0 {
		t.Errorf("flagsHi = 0x%04X, want 0", flagsHi)
	}

	// Check length field
	length := binary.LittleEndian.Uint32(data[4:8])
	if length != 72 {
		t.Errorf("length field = %d, want 72", length)
	}

	// Check encrypted data starts at offset 8
	if data[8] != 0xAA {
		t.Errorf("data[8] = 0x%02X, want 0xAA", data[8])
	}
	if data[71] != 0xBB {
		t.Errorf("data[71] = 0x%02X, want 0xBB", data[71])
	}
}
