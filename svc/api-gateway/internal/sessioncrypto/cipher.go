// Package sessioncrypto encrypts provider credentials stored by API Gateway.
package sessioncrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

type Cipher struct{ aead cipher.AEAD }

func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("session encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, associatedData), nil
}

func (c *Cipher) Decrypt(envelope, associatedData []byte) ([]byte, error) {
	if len(envelope) < c.aead.NonceSize()+c.aead.Overhead() {
		return nil, errors.New("encrypted session envelope is truncated")
	}
	nonce := envelope[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, envelope[c.aead.NonceSize():], associatedData)
	if err != nil {
		return nil, errors.New("encrypted session envelope is invalid")
	}
	return plaintext, nil
}
