package secretbox

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	box, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestEncryptedPayloadIntegrity(t *testing.T) {
	box := testBox(t)
	plain := []byte("temporary-token-never-store-in-clear")
	first, err := box.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := box.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, plain) {
		t.Fatal("ciphertext is not randomized")
	}
	decoded, err := box.Decrypt(first)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("roundtrip failed")
	}
	if _, err := testBox(t).Decrypt(first); err == nil {
		t.Fatal("wrong key accepted")
	}
	first[len(first)-1] ^= 1
	if _, err := box.Decrypt(first); err == nil {
		t.Fatal("tampered payload accepted")
	}
	if _, err := box.Decrypt([]byte("truncated")); err == nil {
		t.Fatal("truncated payload accepted")
	}
}
