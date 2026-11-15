// Package secret encrypts settings at rest with AES-256-GCM.
// Ciphertext is "v1:" plus base64 of a 12-byte nonce and the GCM payload.
// The setting name is additional data, so a value cannot be moved to another key.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const prefix = "v1:"

// ErrDecrypt means the ciphertext is not v1, was tampered with, or the key is wrong.
var ErrDecrypt = errors.New("decrypt failed")

// Box seals and opens setting values. The key is 32 bytes.
type Box struct {
	key [32]byte
}

// NewBox wraps a 32-byte key.
func NewBox(key [32]byte) *Box {
	return &Box{key: key}
}

// Key returns a copy of the raw key. Callers use it for rotation.
func (b *Box) Key() [32]byte {
	if b == nil {
		return [32]byte{}
	}
	return b.key
}

// Encrypt seals plaintext for the named setting.
func (b *Box) Encrypt(name, plain string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("nil cipher")
	}
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), []byte(name))
	buf := make([]byte, 0, len(nonce)+len(sealed))
	buf = append(buf, nonce...)
	buf = append(buf, sealed...)
	return prefix + base64.RawStdEncoding.EncodeToString(buf), nil
}

// Decrypt opens a v1 ciphertext for the named setting.
func (b *Box) Decrypt(name, stored string) (string, error) {
	if b == nil {
		return "", ErrDecrypt
	}
	stored = strings.TrimSpace(stored)
	if !strings.HasPrefix(stored, prefix) {
		return "", ErrDecrypt
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", ErrDecrypt
	}
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return "", ErrDecrypt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrDecrypt
	}
	ns := gcm.NonceSize()
	if len(raw) < ns+gcm.Overhead() {
		return "", ErrDecrypt
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], []byte(name))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// Redact replaces secret strings in msg. Values shorter than 4 characters are left alone
// so a short token does not punch holes in ordinary words.
func Redact(msg string, secrets ...string) string {
	for _, s := range secrets {
		if len(s) < 4 {
			continue
		}
		msg = strings.ReplaceAll(msg, s, "[redacted]")
	}
	return msg
}
