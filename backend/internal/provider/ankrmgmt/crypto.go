package ankrmgmt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

func newAEAD(secret string) (cipher.AEAD, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("management encryption key is not configured")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func seal(aead cipher.AEAD, plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return aead.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func open(aead cipher.AEAD, ciphertext, nonce []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("encrypted credential is unavailable")
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}

func randomURLSafe(size int) (string, error) {
	b := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sha256URLSafe(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
