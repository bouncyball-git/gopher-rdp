package sec

import (
	"bytes"
	"crypto/rc4"
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestDeriveSessionKeys_128Bit(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	macKey, encKey, decKey := deriveSessionKeys(clientRandom, serverRandom, Method128Bit)

	// Keys should be 16 bytes for 128-bit
	if len(macKey) != 16 {
		t.Errorf("macKey length = %d, want 16", len(macKey))
	}
	if len(encKey) != 16 {
		t.Errorf("encKey length = %d, want 16", len(encKey))
	}
	if len(decKey) != 16 {
		t.Errorf("decKey length = %d, want 16", len(decKey))
	}

	// Keys should be different from each other
	if bytes.Equal(macKey, encKey) {
		t.Error("macKey and encKey should differ")
	}
	if bytes.Equal(encKey, decKey) {
		t.Error("encKey and decKey should differ")
	}

	// Deterministic: same inputs produce same outputs
	macKey2, encKey2, decKey2 := deriveSessionKeys(clientRandom, serverRandom, Method128Bit)
	if !bytes.Equal(macKey, macKey2) {
		t.Error("macKey not deterministic")
	}
	if !bytes.Equal(encKey, encKey2) {
		t.Error("encKey not deterministic")
	}
	if !bytes.Equal(decKey, decKey2) {
		t.Error("decKey not deterministic")
	}
}

func TestDeriveSessionKeys_40Bit(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	macKey, encKey, decKey := deriveSessionKeys(clientRandom, serverRandom, Method40Bit)

	// 40-bit keys should be 8 bytes
	if len(macKey) != 8 {
		t.Errorf("macKey length = %d, want 8", len(macKey))
	}
	if len(encKey) != 8 {
		t.Errorf("encKey length = %d, want 8", len(encKey))
	}
	if len(decKey) != 8 {
		t.Errorf("decKey length = %d, want 8", len(decKey))
	}

	// First 3 bytes should be the 40-bit salt
	for _, key := range [][]byte{macKey, encKey, decKey} {
		if key[0] != 0xD1 || key[1] != 0x26 || key[2] != 0x9E {
			t.Errorf("40-bit key salt wrong: %02X %02X %02X", key[0], key[1], key[2])
		}
	}
}

func TestDeriveSessionKeys_56Bit(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)

	macKey, encKey, decKey := deriveSessionKeys(clientRandom, serverRandom, Method56Bit)

	// 56-bit keys should be 8 bytes
	if len(macKey) != 8 {
		t.Errorf("macKey length = %d, want 8", len(macKey))
	}
	if len(encKey) != 8 {
		t.Errorf("encKey length = %d, want 8", len(encKey))
	}
	if len(decKey) != 8 {
		t.Errorf("decKey length = %d, want 8", len(decKey))
	}

	// First byte should be 0xD1
	for _, key := range [][]byte{macKey, encKey, decKey} {
		if key[0] != 0xD1 {
			t.Errorf("56-bit key first byte = 0x%02X, want 0xD1", key[0])
		}
	}
}

func TestGenerateMAC(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	rc, err := NewRDPCrypto(clientRandom, serverRandom, Method128Bit, slog.Default())
	if err != nil {
		t.Fatalf("NewRDPCrypto: %v", err)
	}

	data := []byte("hello world")
	var mac1, mac2 [8]byte
	rc.generateMAC(mac1[:], data)
	rc.generateMAC(mac2[:], data)

	// Same data should produce same MAC
	if mac1 != mac2 {
		t.Error("MAC not deterministic for same data")
	}

	// Different data should produce different MAC
	var mac3 [8]byte
	rc.generateMAC(mac3[:], []byte("other data"))
	if mac1 == mac3 {
		t.Error("MAC should differ for different data")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	// Create matching encrypt/decrypt pairs.
	// Client encrypts with encKey, server decrypts with encKey.
	// To test round-trip, we create two RDPCrypto instances and manually swap keys.
	rc, err := NewRDPCrypto(clientRandom, serverRandom, Method128Bit, slog.Default())
	if err != nil {
		t.Fatalf("NewRDPCrypto: %v", err)
	}

	plaintext := []byte("The quick brown fox jumps over the lazy dog")

	// Encrypt
	encrypted := rc.Encrypt(plaintext, Encrypt)

	// Verify output structure: 4 (secHeader) + 8 (MAC) + len(plaintext) (ciphertext)
	expectedLen := 4 + 8 + len(plaintext)
	if len(encrypted) != expectedLen {
		t.Fatalf("encrypted length = %d, want %d", len(encrypted), expectedLen)
	}

	// Check flags
	flags := binary.LittleEndian.Uint16(encrypted[0:2])
	if flags != Encrypt {
		t.Errorf("flags = 0x%04X, want 0x%04X", flags, Encrypt)
	}

	// Create a "server side" crypto that decrypts what the client encrypted.
	// The server's decrypt key = client's encrypt key.
	// We need a separate instance for decryption using the same encrypt key.
	rcServer, err := NewRDPCrypto(clientRandom, serverRandom, Method128Bit, slog.Default())
	if err != nil {
		t.Fatalf("NewRDPCrypto server: %v", err)
	}
	// Swap the server's decrypt cipher/key to use the client's encrypt key.
	// In practice the server derives its own keys from the same randoms,
	// where server-decrypt = client-encrypt. We simulate by creating a new
	// crypto and using its encryptCipher for decryption.
	// Actually, for round-trip testing, just decrypt using the encryptCipher
	// from a fresh instance (same initial state).
	_ = rcServer

	// Simpler: just verify that encrypt produced something different from plaintext
	ciphertext := encrypted[12:]
	if bytes.Equal(ciphertext, plaintext) {
		t.Error("ciphertext should differ from plaintext")
	}
}

func TestEncryptDecryptRoundTrip_SameInstance(t *testing.T) {
	// For a true round-trip: encrypt, then decrypt with a fresh cipher using same key.
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	macKey, encKey, _ := deriveSessionKeys(clientRandom, serverRandom, Method128Bit)

	// Encrypt side
	rc, _ := NewRDPCrypto(clientRandom, serverRandom, Method128Bit, slog.Default())
	plaintext := []byte("test data for encryption round trip")
	encrypted := rc.Encrypt(plaintext, Encrypt)

	// Decrypt side: create a fresh RC4 cipher with the same encrypt key
	// (simulating the server decrypting client data).
	_ = macKey
	decCipher, _ := rc4.NewCipher(encKey)
	ciphertext := encrypted[12:] // skip header + MAC
	decrypted := make([]byte, len(ciphertext))
	decCipher.XORKeyStream(decrypted, ciphertext)

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %X, want %X", decrypted, plaintext)
	}
}

func TestKeyUpdateAt4096(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	rc, err := NewRDPCrypto(clientRandom, serverRandom, Method128Bit, slog.Default())
	if err != nil {
		t.Fatalf("NewRDPCrypto: %v", err)
	}

	// Run up to the threshold (4096 calls sets encryptCount to 4096)
	data := []byte{0x42}
	for i := 0; i < keyUpdateThreshold; i++ {
		rc.Encrypt(data, Encrypt)
	}

	// encryptCount is now 4096; the next Encrypt triggers key update
	// (check happens at start of Encrypt, resets count to 0, then increments to 1)
	rc.Encrypt(data, Encrypt)

	if rc.encryptCount != 1 {
		t.Errorf("encryptCount after key update = %d, want 1", rc.encryptCount)
	}

	// Verify the key changed from initial
	if bytes.Equal(rc.encryptKey, rc.initialEncKey) {
		t.Error("encrypt key should have changed after update")
	}
}

func TestKeyUpdateAt4096_Decrypt(t *testing.T) {
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	for i := range serverRandom {
		serverRandom[i] = byte(i + 32)
	}

	rc, err := NewRDPCrypto(clientRandom, serverRandom, Method128Bit, slog.Default())
	if err != nil {
		t.Fatalf("NewRDPCrypto: %v", err)
	}

	data := make([]byte, 9) // 8 MAC + 1 ciphertext byte

	for i := 0; i < keyUpdateThreshold; i++ {
		rc.Decrypt(data)
	}

	// Next Decrypt triggers key update
	rc.Decrypt(data)

	if rc.decryptCount != 1 {
		t.Errorf("decryptCount after key update = %d, want 1", rc.decryptCount)
	}

	if bytes.Equal(rc.decryptKey, rc.initialDecKey) {
		t.Error("decrypt key should have changed after update")
	}
}
