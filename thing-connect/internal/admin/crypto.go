package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type encryptedEnvelope struct {
	Version    int    `json:"version"`
	KeyID      string `json:"key_id"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// Cipher encrypts admin-managed secrets using versioned AES-256-GCM
// envelopes. The caller supplies stable AAD for every storage location.
type Cipher struct {
	keyID string
	aead  cipher.AEAD
}

func NewCipher(keyID, encodedKey string) (*Cipher, error) {
	if keyID == "" {
		return nil, errors.New("config encryption key id is required")
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode config encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("config encryption key must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Cipher{keyID: keyID, aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte, aad string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plaintext, []byte(aad))
	raw, err := json.Marshal(encryptedEnvelope{
		Version:    1,
		KeyID:      c.keyID,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		return "", fmt.Errorf("marshal encrypted envelope: %w", err)
	}
	return string(raw), nil
}

func (c *Cipher) Decrypt(raw, aad string) ([]byte, error) {
	var envelope encryptedEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("parse encrypted envelope: %w", err)
	}
	if envelope.Version != 1 || envelope.KeyID != c.keyID {
		return nil, fmt.Errorf("unsupported encrypted envelope version/key: %d/%s", envelope.Version, envelope.KeyID)
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode encryption nonce: %w", err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, errors.New("decrypt secret: authentication failed")
	}
	return plaintext, nil
}

func RandomToken(size int) (string, error) {
	if size < 16 {
		return "", errors.New("random token size must be at least 16 bytes")
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
