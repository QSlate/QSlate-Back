// Package secretbox encrypts temporary auth payloads using the auth signing key.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
)

type Box struct{ cipher cipher.AEAD }

func New(path string) (*Box, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid auth private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("Ed25519 key required")
	}
	sum := sha256.Sum256(append([]byte("qslate-auth-temporary-secrets-v1:"), private.Seed()...))
	blockCipher, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(blockCipher)
	if err != nil {
		return nil, err
	}
	return &Box{cipher: aead}, nil
}
func (b *Box) Encrypt(data []byte) ([]byte, error) {
	return b.cipher.Seal(nil, nil, data, []byte("qslate-auth-v1")), nil
}
func (b *Box) Decrypt(data []byte) ([]byte, error) {
	return b.cipher.Open(nil, nil, data, []byte("qslate-auth-v1"))
}
