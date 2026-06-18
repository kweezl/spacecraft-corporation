package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestRoundTrip(t *testing.T) {
	c, err := NewCipher(testKey(t))
	require.NoError(t, err)

	enc, err := c.Encrypt("super-secret-token")
	require.NoError(t, err)
	assert.NotEqual(t, "super-secret-token", enc)

	dec, err := c.Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, "super-secret-token", dec)
}

func TestNewCipher_BadKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	_, err := NewCipher(short)
	require.Error(t, err)
}

func TestDecrypt_TooShort(t *testing.T) {
	c, err := NewCipher(testKey(t))
	require.NoError(t, err)
	_, err = c.Decrypt(base64.StdEncoding.EncodeToString([]byte("x")))
	require.Error(t, err)
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	c1, _ := NewCipher(testKey(t))
	c2, _ := NewCipher(testKey(t))
	enc, err := c1.Encrypt("hello")
	require.NoError(t, err)
	_, err = c2.Decrypt(enc)
	require.Error(t, err)
}
