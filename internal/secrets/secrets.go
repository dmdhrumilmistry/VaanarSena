// Package secrets provides authenticated encryption for data at rest and
// helpers for random tokens.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
)

// Box encrypts and decrypts with AES-256-GCM under a key derived from the
// server secret.
type Box struct {
	aead cipher.AEAD
	key  []byte
}

// NewBox derives purpose-separated keys from secret.
func NewBox(secret string) (*Box, error) {
	encKey := Derive(secret, "vaanarsena/at-rest/v1")
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: Derive(secret, "vaanarsena/session/v1")}, nil
}

// SessionKey returns the key used to sign console sessions.
func (b *Box) SessionKey() []byte { return b.key }

// Seal encrypts plaintext. The nonce is prepended to the output.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts the output of Seal.
func (b *Box) Open(ciphertext []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(ciphertext) < n {
		return nil, errors.New("secrets: ciphertext too short")
	}
	return b.aead.Open(nil, ciphertext[:n], ciphertext[n:], nil)
}

// Derive returns HMAC-SHA256(secret, purpose), a 32-byte subkey.
func Derive(secret, purpose string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(purpose))
	return m.Sum(nil)
}

// Token returns a URL-safe random token with n bytes of entropy.
func Token(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hash returns the hex SHA-256 of a token, for storage and lookup. Tokens are
// high-entropy, so a fast hash is appropriate (unlike passwords).
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
