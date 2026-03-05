package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	key1, err := DeriveKey("test-master-key", "purpose-a")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(key1))
	}

	key2, err := DeriveKey("test-master-key", "purpose-b")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if bytes.Equal(key1, key2) {
		t.Fatal("different purposes should produce different keys")
	}

	key3, err := DeriveKey("test-master-key", "purpose-a")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if !bytes.Equal(key1, key3) {
		t.Fatal("same inputs should produce same key")
	}
}

func TestDeriveKeyEmpty(t *testing.T) {
	_, err := DeriveKey("", "purpose")
	if err == nil {
		t.Fatal("expected error for empty master key")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key, err := DeriveKey("test-key", "test")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	plaintext := []byte("hello world, this is a secret P12 password")
	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(plaintext, ciphertext) {
		t.Fatal("ciphertext should not equal plaintext")
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted text does not match: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1, _ := DeriveKey("key-1", "test")
	key2, _ := DeriveKey("key-2", "test")

	plaintext := []byte("secret data")
	ciphertext, _ := Encrypt(key1, plaintext)

	_, err := Decrypt(key2, ciphertext)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key, _ := DeriveKey("key", "test")
	_, err := Decrypt(key, []byte("short"))
	if err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestEncryptDecryptFile(t *testing.T) {
	masterKey := "my-super-secret-key"
	purpose := "p12-encryption"
	data := []byte("fake p12 binary data here")

	encrypted, err := EncryptFile(masterKey, purpose, data)
	if err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}

	decrypted, err := DecryptFile(masterKey, purpose, encrypted)
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}

	if !bytes.Equal(data, decrypted) {
		t.Fatal("roundtrip failed")
	}
}

func TestFingerprintShort(t *testing.T) {
	fp := FingerprintShort([]byte("some data"))
	if len(fp) != 16 {
		t.Fatalf("expected 16 hex chars, got %d (%s)", len(fp), fp)
	}
	_, err := hex.DecodeString(fp)
	if err != nil {
		t.Fatalf("fingerprint is not valid hex: %v", err)
	}

	fp2 := FingerprintShort([]byte("some data"))
	if fp != fp2 {
		t.Fatal("same input should produce same fingerprint")
	}

	fp3 := FingerprintShort([]byte("other data"))
	if fp == fp3 {
		t.Fatal("different input should produce different fingerprint")
	}
}

func TestEphemeralEncryptDecrypt(t *testing.T) {
	processKey, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("GenerateRandomKey: %v", err)
	}

	password := []byte("my-p12-password")

	token, err := EncryptEphemeral(processKey, password)
	if err != nil {
		t.Fatalf("EncryptEphemeral: %v", err)
	}

	if token == string(password) {
		t.Fatal("token should not be plaintext")
	}

	decrypted, err := DecryptEphemeral(processKey, token)
	if err != nil {
		t.Fatalf("DecryptEphemeral: %v", err)
	}

	if !bytes.Equal(password, decrypted) {
		t.Fatalf("roundtrip failed: got %q, want %q", decrypted, password)
	}
}

func TestEphemeralDecryptWrongKey(t *testing.T) {
	key1, _ := GenerateRandomKey()
	key2, _ := GenerateRandomKey()

	token, _ := EncryptEphemeral(key1, []byte("secret"))
	_, err := DecryptEphemeral(key2, token)
	if err == nil {
		t.Fatal("expected error with wrong key")
	}
}
