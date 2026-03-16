package sec

import (
	"math/big"
	"testing"
)

func TestRSAEncrypt_KnownAnswer(t *testing.T) {
	// Small known key for deterministic testing.
	// n = 143 (0x8F), e = 7, so we operate mod 143.
	// Plaintext = 2 (LE byte: 0x02)
	// Expected: 2^7 mod 143 = 128 (0x80)

	// Modulus is little-endian: 143 = 0x8F (1 byte) + 8 bytes zero padding = 9 bytes
	modulus := make([]byte, 9)
	modulus[0] = 0x8F

	key := &RSAPublicKey{
		BitLen:  8, // 1 byte
		PubExp:  7,
		Modulus: modulus,
	}

	plaintext := []byte{0x02}
	result := RSAEncrypt(plaintext, key)

	// Result should be 1 byte (BitLen/8) + 8 zero bytes = 9 bytes
	if len(result) != 9 {
		t.Fatalf("result length = %d, want 9", len(result))
	}
	if result[0] != 0x80 {
		t.Errorf("result[0] = 0x%02X, want 0x80", result[0])
	}
	// Remaining 8 bytes should be zero
	for i := 1; i < 9; i++ {
		if result[i] != 0 {
			t.Errorf("result[%d] = 0x%02X, want 0x00", i, result[i])
		}
	}
}

func TestRSAEncrypt_512BitKey(t *testing.T) {
	// Generate a known 512-bit key pair for validation.
	// p=0xFFFFFFFFFFFFFFC5, q=0xFFFFFFFFFFFFFFBF (two primes near 2^64)
	// For simplicity, just verify output length and that it can be verified
	// by computing m^e mod n with big.Int directly.

	// Use a small but realistic test: 64-byte modulus
	modulus := make([]byte, 72) // 64 + 8 zero padding
	// Set modulus to a known value (little-endian)
	for i := 0; i < 64; i++ {
		modulus[i] = 0xFF
	}
	modulus[0] = 0xFD // Make it odd and specific

	key := &RSAPublicKey{
		BitLen:  512,
		PubExp:  65537,
		Modulus: modulus,
	}

	plaintext := make([]byte, 32) // 256-bit client random
	plaintext[0] = 0x42
	plaintext[31] = 0x01

	result := RSAEncrypt(plaintext, key)

	// Output should be 64 bytes (512/8) + 8 zero bytes = 72
	if len(result) != 72 {
		t.Fatalf("result length = %d, want 72", len(result))
	}

	// Last 8 bytes must be zero
	for i := 64; i < 72; i++ {
		if result[i] != 0 {
			t.Errorf("result[%d] = 0x%02X, want 0x00", i, result[i])
		}
	}

	// Verify: decrypt using big.Int directly
	n := new(big.Int).SetBytes(reverseBytes(modulus[:64]))
	e := big.NewInt(65537)
	c := new(big.Int).SetBytes(reverseBytes(result[:64]))
	m := new(big.Int).Exp(c, e, n) // "decrypt" with same exponent (for textbook RSA verification)

	// Re-encrypt original plaintext for comparison
	mOrig := new(big.Int).SetBytes(reverseBytes(plaintext))
	cExpected := new(big.Int).Exp(mOrig, e, n)

	if c.Cmp(cExpected) != 0 {
		t.Error("RSA encryption result does not match direct big.Int computation")
	}
	_ = m // suppress unused
}

func TestReverseBytes(t *testing.T) {
	tests := []struct {
		in   []byte
		want []byte
	}{
		{[]byte{1, 2, 3}, []byte{3, 2, 1}},
		{[]byte{0xFF}, []byte{0xFF}},
		{[]byte{}, []byte{}},
	}
	for _, tt := range tests {
		got := reverseBytes(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("reverseBytes(%X) length = %d, want %d", tt.in, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("reverseBytes(%X)[%d] = %02X, want %02X", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}
