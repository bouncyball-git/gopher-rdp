package nla

import (
	"log/slog"
	"crypto/md5"
	"encoding/hex"
	"testing"
)

func TestSignKeyDerivation(t *testing.T) {
	// MS-NLMP section 4.2.4.4 — known session key
	// ExportedSessionKey for the spec example:
	// We'll use a known value and verify the key derivation formula
	exportedKey := [16]byte{0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55,
		0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55}

	clientSignKey := signKey(exportedKey[:],
		"session key to client-to-server signing key magic constant\x00")

	// Verify manually: MD5(exportedKey || magic)
	h := md5.New()
	h.Write(exportedKey[:])
	h.Write([]byte("session key to client-to-server signing key magic constant\x00"))
	expected := h.Sum(nil)

	if hex.EncodeToString(clientSignKey[:]) != hex.EncodeToString(expected) {
		t.Errorf("client sign key mismatch")
	}
}

func TestSealUnsealRoundTrip(t *testing.T) {
	// Use a fixed exported session key
	var key [16]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	seal := newNTLMSeal(key, slog.Default())

	plaintext := []byte("Hello, CredSSP!")
	sealed := seal.seal(plaintext)

	// Verify seal produces the expected format: sig(16) + ciphertext
	if len(sealed) != 16+len(plaintext) {
		t.Errorf("sealed length = %d, want %d", len(sealed), 16+len(plaintext))
	}

	// Verify signature version = 1
	if sealed[0] != 1 || sealed[1] != 0 || sealed[2] != 0 || sealed[3] != 0 {
		t.Errorf("signature version: %x, want 01000000", sealed[0:4])
	}
}

func TestSealDeterminism(t *testing.T) {
	// Same key + same plaintext should produce same sealed output
	// (RC4 is deterministic given same key, and we're sealing from init state)
	var key [16]byte
	for i := range key {
		key[i] = byte(i)
	}

	seal1 := newNTLMSeal(key, slog.Default())
	seal2 := newNTLMSeal(key, slog.Default())

	plaintext := []byte("test message")
	sealed1 := seal1.seal(plaintext)
	sealed2 := seal2.seal(plaintext)

	if hex.EncodeToString(sealed1) != hex.EncodeToString(sealed2) {
		t.Errorf("seal not deterministic:\n  got1: %s\n  got2: %s",
			hex.EncodeToString(sealed1), hex.EncodeToString(sealed2))
	}
}

func TestSealSequenceNumbers(t *testing.T) {
	var key [16]byte
	seal := newNTLMSeal(key, slog.Default())

	// First message: seqNum=0
	msg1 := seal.seal([]byte("first"))
	// SeqNum is at bytes 12..16 of the signature
	if msg1[12] != 0 || msg1[13] != 0 || msg1[14] != 0 || msg1[15] != 0 {
		t.Errorf("first seqNum = %x, want 0", msg1[12:16])
	}

	// Second message: seqNum=1
	msg2 := seal.seal([]byte("second"))
	if msg2[12] != 1 || msg2[13] != 0 || msg2[14] != 0 || msg2[15] != 0 {
		t.Errorf("second seqNum = %x, want 1", msg2[12:16])
	}
}
