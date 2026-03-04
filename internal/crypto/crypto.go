package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	keyLen   = 32 // AES-256
	nonceLen = 12 // GCM standard nonce
)

// DeriveKey derives an AES-256 key from a master key using HKDF-SHA256.
func DeriveKey(masterKey string, purpose string) ([]byte, error) {
	if masterKey == "" {
		return nil, errors.New("master key is empty")
	}
	salt := sha256.Sum256([]byte("signy-v1"))
	hkdfReader := hkdf.New(sha256.New, []byte(masterKey), salt[:], []byte(purpose))
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("hkdf derive: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with a derived key.
// Returns nonce + ciphertext concatenated.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce generation: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts nonce+ciphertext using AES-256-GCM with a derived key.
func Decrypt(key, data []byte) ([]byte, error) {
	if len(data) < nonceLen {
		return nil, errors.New("ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := data[:nonceLen]
	ciphertext := data[nonceLen:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// EncryptFile encrypts file content and returns encrypted bytes.
func EncryptFile(masterKey, purpose string, plaintext []byte) ([]byte, error) {
	key, err := DeriveKey(masterKey, purpose)
	if err != nil {
		return nil, err
	}
	return Encrypt(key, plaintext)
}

// DecryptFile decrypts file content.
func DecryptFile(masterKey, purpose string, ciphertext []byte) ([]byte, error) {
	key, err := DeriveKey(masterKey, purpose)
	if err != nil {
		return nil, err
	}
	return Decrypt(key, ciphertext)
}

// FingerprintShort computes a short hex fingerprint (first 8 bytes of SHA-256).
func FingerprintShort(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// GenerateRandomKey generates a random 32-byte key for ephemeral encryption.
func GenerateRandomKey() ([]byte, error) {
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("random key generation: %w", err)
	}
	return key, nil
}

// EncryptEphemeral encrypts data with a random process key for short-lived tokens.
func EncryptEphemeral(processKey, plaintext []byte) (string, error) {
	ct, err := Encrypt(processKey, plaintext)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ct), nil
}

// DecryptEphemeral decrypts a hex-encoded ephemeral token.
func DecryptEphemeral(processKey []byte, token string) ([]byte, error) {
	data, err := hex.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	return Decrypt(processKey, data)
}
