package lic

import (
	"bytes"
	"testing"
)

func TestDeriveLicenseKeys(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0x01}, 48)
	clientRandom := bytes.Repeat([]byte{0x02}, 32)
	serverRandom := bytes.Repeat([]byte{0x03}, 32)

	lc := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)

	if len(lc.MACSaltKey) != 16 {
		t.Errorf("MACSaltKey len = %d, want 16", len(lc.MACSaltKey))
	}
	if len(lc.LicensingEncryptKey) != 16 {
		t.Errorf("LicensingEncryptKey len = %d, want 16", len(lc.LicensingEncryptKey))
	}

	// Keys should not be all zeros
	allZero := true
	for _, b := range lc.MACSaltKey {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("MACSaltKey is all zeros")
	}

	allZero = true
	for _, b := range lc.LicensingEncryptKey {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("LicensingEncryptKey is all zeros")
	}
}

func TestDeriveLicenseKeys_Deterministic(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0xAA}, 48)
	clientRandom := bytes.Repeat([]byte{0xBB}, 32)
	serverRandom := bytes.Repeat([]byte{0xCC}, 32)

	lc1 := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)
	lc2 := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)

	if !bytes.Equal(lc1.MACSaltKey, lc2.MACSaltKey) {
		t.Error("MACSaltKey not deterministic")
	}
	if !bytes.Equal(lc1.LicensingEncryptKey, lc2.LicensingEncryptKey) {
		t.Error("LicensingEncryptKey not deterministic")
	}
}

func TestDeriveLicenseKeys_RandomOrderMatters(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0x01}, 48)
	rand1 := bytes.Repeat([]byte{0x02}, 32)
	rand2 := bytes.Repeat([]byte{0x03}, 32)

	// Swapping clientRandom and serverRandom should produce different keys
	lc1 := DeriveLicenseKeys(preMaster, rand1, rand2)
	lc2 := DeriveLicenseKeys(preMaster, rand2, rand1)

	if bytes.Equal(lc1.MACSaltKey, lc2.MACSaltKey) {
		t.Error("swapping randoms produced same MACSaltKey")
	}
	if bytes.Equal(lc1.LicensingEncryptKey, lc2.LicensingEncryptKey) {
		t.Error("swapping randoms produced same LicensingEncryptKey")
	}
}

func TestMAC(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0x01}, 48)
	clientRandom := bytes.Repeat([]byte{0x02}, 32)
	serverRandom := bytes.Repeat([]byte{0x03}, 32)

	lc := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)

	data := []byte("hello licensing")
	mac1 := lc.MAC(data)
	mac2 := lc.MAC(data)

	if len(mac1) != 16 {
		t.Errorf("MAC len = %d, want 16", len(mac1))
	}

	// Same data should produce same MAC
	if !bytes.Equal(mac1, mac2) {
		t.Error("MAC not deterministic")
	}

	// Different data should produce different MAC
	mac3 := lc.MAC([]byte("different data"))
	if bytes.Equal(mac1, mac3) {
		t.Error("different data produced same MAC")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0x01}, 48)
	clientRandom := bytes.Repeat([]byte{0x02}, 32)
	serverRandom := bytes.Repeat([]byte{0x03}, 32)

	lc := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)

	plaintext := []byte("the quick brown fox jumps over the lazy dog")
	encrypted := lc.Encrypt(plaintext)

	// Encrypted should differ from plaintext
	if bytes.Equal(encrypted, plaintext) {
		t.Error("encrypted equals plaintext")
	}

	decrypted := lc.Decrypt(encrypted)
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %x, want %x", decrypted, plaintext)
	}
}

func TestEncryptDecrypt_EmptyData(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0x01}, 48)
	clientRandom := bytes.Repeat([]byte{0x02}, 32)
	serverRandom := bytes.Repeat([]byte{0x03}, 32)

	lc := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)

	encrypted := lc.Encrypt([]byte{})
	decrypted := lc.Decrypt(encrypted)
	if len(decrypted) != 0 {
		t.Errorf("decrypted len = %d, want 0", len(decrypted))
	}
}

func TestEncrypt_FreshCipherPerOperation(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0x01}, 48)
	clientRandom := bytes.Repeat([]byte{0x02}, 32)
	serverRandom := bytes.Repeat([]byte{0x03}, 32)

	lc := DeriveLicenseKeys(preMaster, clientRandom, serverRandom)

	plaintext := []byte("same input")

	// Multiple encryptions of the same data should produce the same output
	// because we use a fresh cipher each time (RC4 with same key = same keystream)
	enc1 := lc.Encrypt(plaintext)
	enc2 := lc.Encrypt(plaintext)

	if !bytes.Equal(enc1, enc2) {
		t.Error("same plaintext with fresh cipher should produce same ciphertext")
	}
}
