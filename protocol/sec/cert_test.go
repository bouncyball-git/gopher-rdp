package sec

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"os"
	"testing"
	"time"
)

// Pre-generated keys and certs, initialized once in TestMain.
var (
	testKey1    *rsa.PrivateKey
	testKey2    *rsa.PrivateKey
	testCertDER1 []byte // self-signed cert using testKey1
	testCertDER2 []byte // self-signed cert using testKey2
)

func TestMain(m *testing.M) {
	var err error
	testKey1, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generating test key 1: " + err.Error())
	}
	testKey2, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generating test key 2: " + err.Error())
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "RDP Test Server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	testCertDER1, err = x509.CreateCertificate(rand.Reader, template, template, &testKey1.PublicKey, testKey1)
	if err != nil {
		panic("creating test cert 1: " + err.Error())
	}
	template.SerialNumber = big.NewInt(2)
	testCertDER2, err = x509.CreateCertificate(rand.Reader, template, template, &testKey2.PublicKey, testKey2)
	if err != nil {
		panic("creating test cert 2: " + err.Error())
	}

	os.Exit(m.Run())
}

// buildProprietaryCert builds a minimal proprietary certificate with a 512-bit RSA key.
func buildProprietaryCert(modulus []byte, pubExp uint32) []byte {
	keyLen := len(modulus) // includes 8-byte zero padding
	pkBlobLen := 20 + keyLen

	// Signature blob: minimal (we skip validation)
	sigBlob := make([]byte, 72) // typical signature size

	certLen := 12 + 4 + pkBlobLen + 4 + len(sigBlob)
	cert := make([]byte, certLen)
	off := 0

	// dwVersion = 1 (proprietary)
	binary.LittleEndian.PutUint32(cert[off:], certVersionProp)
	off += 4
	// dwSigAlgId
	binary.LittleEndian.PutUint32(cert[off:], sigAlgRSA)
	off += 4
	// dwKeyAlgId
	binary.LittleEndian.PutUint32(cert[off:], keyAlgRSA)
	off += 4
	// wPublicKeyBlobType + wPublicKeyBlobLen
	binary.LittleEndian.PutUint16(cert[off:], publicKeyBlobType)
	binary.LittleEndian.PutUint16(cert[off+2:], uint16(pkBlobLen))
	off += 4

	// PublicKeyBlob
	binary.LittleEndian.PutUint32(cert[off:], rsaKeyMagic)
	binary.LittleEndian.PutUint32(cert[off+4:], uint32(keyLen))
	binary.LittleEndian.PutUint32(cert[off+8:], uint32((keyLen-8)*8)) // bitLen
	binary.LittleEndian.PutUint32(cert[off+12:], uint32(keyLen-9))    // datalen
	binary.LittleEndian.PutUint32(cert[off+16:], pubExp)
	copy(cert[off+20:], modulus)
	off += pkBlobLen

	// wSignatureBlobType + wSignatureBlobLen
	binary.LittleEndian.PutUint16(cert[off:], signatureBlobType)
	binary.LittleEndian.PutUint16(cert[off+2:], uint16(len(sigBlob)))
	off += 4
	copy(cert[off:], sigBlob)

	return cert
}

// buildServerSecurityBlob builds a complete RawData blob for testing.
func buildServerSecurityBlob(serverRandom []byte, certData []byte) []byte {
	buf := make([]byte, 8+len(serverRandom)+len(certData))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(serverRandom)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(certData)))
	copy(buf[8:], serverRandom)
	copy(buf[8+len(serverRandom):], certData)
	return buf
}

// buildX509CertChain builds an X.509 certificate chain blob for testing.
// certs is a list of DER-encoded certificates; the last is the terminal server cert.
func buildX509CertChain(certs ...[]byte) []byte {
	// dwVersion(4) + numCerts(4) + for each: cbCert(4) + cert
	size := 8
	for _, c := range certs {
		size += 4 + len(c)
	}

	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], certVersionX509)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(certs)))

	off := 8
	for _, c := range certs {
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(len(c)))
		off += 4
		copy(buf[off:], c)
		off += len(c)
	}
	return buf
}

func TestDecodeServerSecurityBlob_Valid512(t *testing.T) {
	// 512-bit key: 64 bytes modulus + 8 bytes zero padding = 72 bytes
	modulus := make([]byte, 72)
	for i := range modulus[:64] {
		modulus[i] = byte(i + 1) // non-zero modulus bytes
	}
	// last 8 bytes are zero padding

	certData := buildProprietaryCert(modulus, 65537)
	serverRandom := make([]byte, 32)
	for i := range serverRandom {
		serverRandom[i] = byte(i)
	}

	rawData := buildServerSecurityBlob(serverRandom, certData)
	blob, err := DecodeServerSecurityBlob(rawData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(blob.ServerRandom) != 32 {
		t.Errorf("ServerRandom length = %d, want 32", len(blob.ServerRandom))
	}
	for i, b := range blob.ServerRandom {
		if b != byte(i) {
			t.Errorf("ServerRandom[%d] = %02X, want %02X", i, b, byte(i))
			break
		}
	}

	pk := blob.Certificate.PublicKey
	if pk.PubExp != 65537 {
		t.Errorf("PubExp = %d, want 65537", pk.PubExp)
	}
	if pk.BitLen != 512 {
		t.Errorf("BitLen = %d, want 512", pk.BitLen)
	}
	if len(pk.Modulus) != 72 {
		t.Errorf("Modulus length = %d, want 72", len(pk.Modulus))
	}
}

func TestDecodeServerSecurityBlob_ShortData(t *testing.T) {
	_, err := DecodeServerSecurityBlob([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestDecodeServerSecurityBlob_TruncatedPayload(t *testing.T) {
	// serverRandomLen=32, serverCertLen=100, but only provide 40 total bytes
	buf := make([]byte, 40)
	binary.LittleEndian.PutUint32(buf[0:4], 32)
	binary.LittleEndian.PutUint32(buf[4:8], 100)
	_, err := DecodeServerSecurityBlob(buf)
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestDecodeServerSecurityBlob_BadMagic(t *testing.T) {
	modulus := make([]byte, 72)
	certData := buildProprietaryCert(modulus, 65537)
	// Corrupt the RSA magic
	// magic is at offset 12 (version) + 4 + 4 + 4 (blob header) = 16 in certData
	binary.LittleEndian.PutUint32(certData[16:], 0xDEADBEEF)

	rawData := buildServerSecurityBlob(make([]byte, 32), certData)
	_, err := DecodeServerSecurityBlob(rawData)
	if err == nil {
		t.Fatal("expected error for bad RSA magic")
	}
}

func TestDecodeServerSecurityBlob_UnsupportedVersion(t *testing.T) {
	// Version 3 doesn't exist — should error
	certData := make([]byte, 40)
	binary.LittleEndian.PutUint32(certData[0:4], 3)
	rawData := buildServerSecurityBlob(make([]byte, 32), certData)
	_, err := DecodeServerSecurityBlob(rawData)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestDecodeX509CertChain_SingleCert(t *testing.T) {
	certData := buildX509CertChain(testCertDER1)
	rawData := buildServerSecurityBlob(make([]byte, 32), certData)

	blob, err := DecodeServerSecurityBlob(rawData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pk := blob.Certificate.PublicKey
	if pk.BitLen != 2048 {
		t.Errorf("BitLen = %d, want 2048", pk.BitLen)
	}
	if pk.PubExp != uint32(testKey1.PublicKey.E) {
		t.Errorf("PubExp = %d, want %d", pk.PubExp, testKey1.PublicKey.E)
	}

	// Verify modulus: should be little-endian + 8 zero bytes
	wantModBytes := testKey1.PublicKey.N.Bytes() // big-endian
	wantModLen := len(wantModBytes)
	if len(pk.Modulus) != wantModLen+8 {
		t.Fatalf("Modulus length = %d, want %d", len(pk.Modulus), wantModLen+8)
	}
	// Check LE reversal
	for i := 0; i < wantModLen; i++ {
		if pk.Modulus[i] != wantModBytes[wantModLen-1-i] {
			t.Errorf("Modulus[%d] = %02X, want %02X", i, pk.Modulus[i], wantModBytes[wantModLen-1-i])
			break
		}
	}
	// Check 8-byte zero padding
	for i := wantModLen; i < wantModLen+8; i++ {
		if pk.Modulus[i] != 0 {
			t.Errorf("Modulus padding[%d] = %02X, want 0", i, pk.Modulus[i])
		}
	}
}

func TestDecodeX509CertChain_MultipleCerts(t *testing.T) {
	// Chain: key1 cert first (as "CA"), key2 cert last (terminal server)
	certData := buildX509CertChain(testCertDER1, testCertDER2)
	rawData := buildServerSecurityBlob(make([]byte, 32), certData)

	blob, err := DecodeServerSecurityBlob(rawData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should extract key2 (last cert), not key1
	pk := blob.Certificate.PublicKey
	if pk.PubExp != uint32(testKey2.PublicKey.E) {
		t.Errorf("got wrong key: PubExp = %d", pk.PubExp)
	}
	if pk.BitLen != uint32(testKey2.PublicKey.N.BitLen()) {
		t.Errorf("BitLen = %d, want %d", pk.BitLen, testKey2.PublicKey.N.BitLen())
	}
}

func TestDecodeX509CertChain_NoCerts(t *testing.T) {
	// numCerts = 0
	certData := make([]byte, 8)
	binary.LittleEndian.PutUint32(certData[0:4], certVersionX509)
	binary.LittleEndian.PutUint32(certData[4:8], 0)

	rawData := buildServerSecurityBlob(make([]byte, 32), certData)
	_, err := DecodeServerSecurityBlob(rawData)
	if err == nil {
		t.Fatal("expected error for zero certificates")
	}
}

func TestDecodeX509CertChain_TruncatedBlob(t *testing.T) {
	// Claim 1 cert of 500 bytes but only provide 10
	certData := make([]byte, 22) // 4+4+4+10
	binary.LittleEndian.PutUint32(certData[0:4], certVersionX509)
	binary.LittleEndian.PutUint32(certData[4:8], 1)
	binary.LittleEndian.PutUint32(certData[8:12], 500) // cbCert = 500

	rawData := buildServerSecurityBlob(make([]byte, 32), certData)
	_, err := DecodeServerSecurityBlob(rawData)
	if err == nil {
		t.Fatal("expected error for truncated cert blob")
	}
}

func TestDecodeX509CertChain_InvalidDER(t *testing.T) {
	// Valid structure but garbage DER data
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03}
	certData := buildX509CertChain(garbage)
	rawData := buildServerSecurityBlob(make([]byte, 32), certData)

	_, err := DecodeServerSecurityBlob(rawData)
	if err == nil {
		t.Fatal("expected error for invalid DER")
	}
}

func TestDecodeX509CertChain_RSAEncryptRoundtrip(t *testing.T) {
	certData := buildX509CertChain(testCertDER1)
	rawData := buildServerSecurityBlob(make([]byte, 32), certData)

	blob, err := DecodeServerSecurityBlob(rawData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Encrypt a 32-byte client random
	clientRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
	}
	encrypted := RSAEncrypt(clientRandom, &blob.Certificate.PublicKey)
	if len(encrypted) == 0 {
		t.Fatal("RSAEncrypt returned empty result")
	}

	// Verify the ciphertext length: BitLen/8 + 8 zero bytes
	expectedLen := int(blob.Certificate.PublicKey.BitLen/8) + 8
	if len(encrypted) != expectedLen {
		t.Errorf("encrypted length = %d, want %d", len(encrypted), expectedLen)
	}
}
