// Package crypto provides AES-256-GCM encryption for secrets at rest (bot
// tokens). The key comes from ENCRYPTION_KEY (base64 of 32 raw bytes).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/kweezl/spacecraft-cadet/internal/config"
	"go.uber.org/fx"
)

// Config is this module's env config.
type Config struct {
	Key string `env:"ENCRYPTION_KEY,required"`
}

// Cipher encrypts/decrypts short secrets with AES-256-GCM.
type Cipher struct {
	gcm cipher.AEAD
}

// NewCipher builds a Cipher from a base64-encoded 32-byte key.
func NewCipher(keyB64 string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

// Encrypt returns base64(nonce || ciphertext).
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt reverses Encrypt.
func (c *Cipher) Decrypt(enc string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := c.gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := data[:ns], data[ns:]
	pt, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func provide(cfg Config) (*Cipher, error) { return NewCipher(cfg.Key) }

// Module provides a *Cipher built from ENCRYPTION_KEY.
var Module = fx.Module("crypto",
	fx.Provide(config.Parse[Config]),
	fx.Provide(provide),
)
