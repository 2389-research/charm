// ABOUTME: Unit tests for the crypt package, focusing on EncryptLookupField and DecryptLookupField functions.
// ABOUTME: Tests cover empty strings, roundtrip encryption, determinism, and wrong key scenarios.
package crypt

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	charm "github.com/charmbracelet/charm/proto"
)

// createTestCrypt creates a Crypt instance with a test key for testing purposes.
func createTestCrypt(t *testing.T) *Crypt {
	t.Helper()
	// Generate a 32-byte random key for testing
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	encryptKey := &charm.EncryptKey{
		ID:  "test-key-1",
		Key: hex.EncodeToString(key),
	}

	return &Crypt{
		keys: []*charm.EncryptKey{encryptKey},
	}
}

// createTestCryptWithKey creates a Crypt instance with a specific key.
func createTestCryptWithKey(t *testing.T, keyHex string) *Crypt {
	t.Helper()
	encryptKey := &charm.EncryptKey{
		ID:  "test-key",
		Key: keyHex,
	}

	return &Crypt{
		keys: []*charm.EncryptKey{encryptKey},
	}
}

func TestEncryptLookupField_EmptyString(t *testing.T) {
	cr := createTestCrypt(t)

	encrypted, err := cr.EncryptLookupField("")
	if err != nil {
		t.Errorf("EncryptLookupField(\"\") returned error: %v, want nil", err)
	}
	if encrypted != "" {
		t.Errorf("EncryptLookupField(\"\") = %q, want empty string", encrypted)
	}
}

func TestDecryptLookupField_EmptyString(t *testing.T) {
	cr := createTestCrypt(t)

	decrypted, err := cr.DecryptLookupField("")
	if err != nil {
		t.Errorf("DecryptLookupField(\"\") returned error: %v, want nil", err)
	}
	if decrypted != "" {
		t.Errorf("DecryptLookupField(\"\") = %q, want empty string", decrypted)
	}
}

func TestEncryptDecryptLookupField_Roundtrip(t *testing.T) {
	cr := createTestCrypt(t)

	testCases := []struct {
		name      string
		plaintext string
	}{
		{
			name:      "simple string",
			plaintext: "hello world",
		},
		{
			name:      "email address",
			plaintext: "user@example.com",
		},
		{
			name:      "unicode characters",
			plaintext: "Hello 世界 🌍",
		},
		{
			name:      "special characters",
			plaintext: "!@#$%^&*()_+-=[]{}|;:',.<>?/",
		},
		{
			name:      "long string",
			plaintext: "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encrypt
			encrypted, err := cr.EncryptLookupField(tc.plaintext)
			if err != nil {
				t.Fatalf("EncryptLookupField(%q) returned error: %v", tc.plaintext, err)
			}

			if encrypted == "" {
				t.Fatalf("EncryptLookupField(%q) returned empty string", tc.plaintext)
			}

			// Ensure it's different from plaintext
			if encrypted == tc.plaintext {
				t.Errorf("EncryptLookupField(%q) = %q, ciphertext should differ from plaintext", tc.plaintext, encrypted)
			}

			// Decrypt
			decrypted, err := cr.DecryptLookupField(encrypted)
			if err != nil {
				t.Fatalf("DecryptLookupField(%q) returned error: %v", encrypted, err)
			}

			// Verify roundtrip
			if decrypted != tc.plaintext {
				t.Errorf("Roundtrip failed: got %q, want %q", decrypted, tc.plaintext)
			}
		})
	}
}

func TestEncryptLookupField_Determinism(t *testing.T) {
	// Generate a fixed key for determinism test
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	keyHex := hex.EncodeToString(key)

	cr := createTestCryptWithKey(t, keyHex)

	plaintext := "deterministic test"

	// Encrypt multiple times
	encrypted1, err := cr.EncryptLookupField(plaintext)
	if err != nil {
		t.Fatalf("First EncryptLookupField failed: %v", err)
	}

	encrypted2, err := cr.EncryptLookupField(plaintext)
	if err != nil {
		t.Fatalf("Second EncryptLookupField failed: %v", err)
	}

	encrypted3, err := cr.EncryptLookupField(plaintext)
	if err != nil {
		t.Fatalf("Third EncryptLookupField failed: %v", err)
	}

	// All encryptions should produce the same ciphertext
	if encrypted1 != encrypted2 {
		t.Errorf("Encryption is not deterministic: first=%q, second=%q", encrypted1, encrypted2)
	}

	if encrypted1 != encrypted3 {
		t.Errorf("Encryption is not deterministic: first=%q, third=%q", encrypted1, encrypted3)
	}

	if encrypted2 != encrypted3 {
		t.Errorf("Encryption is not deterministic: second=%q, third=%q", encrypted2, encrypted3)
	}
}

func TestDecryptLookupField_WrongKey(t *testing.T) {
	// Create first crypt with one key
	key1 := make([]byte, 32)
	for i := range key1 {
		key1[i] = byte(i)
	}
	cr1 := createTestCryptWithKey(t, hex.EncodeToString(key1))

	// Create second crypt with different key
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(i + 100)
	}
	cr2 := createTestCryptWithKey(t, hex.EncodeToString(key2))

	plaintext := "secret message"

	// Encrypt with first key
	encrypted, err := cr1.EncryptLookupField(plaintext)
	if err != nil {
		t.Fatalf("EncryptLookupField failed: %v", err)
	}

	// Try to decrypt with wrong key
	_, err = cr2.DecryptLookupField(encrypted)
	if err == nil {
		t.Error("DecryptLookupField with wrong key should return error, got nil")
	}

	if err != ErrIncorrectEncryptKeys {
		t.Errorf("DecryptLookupField with wrong key returned error %v, want %v", err, ErrIncorrectEncryptKeys)
	}
}

func TestDecryptLookupField_MultipleKeys(t *testing.T) {
	// Create crypt with multiple keys
	key1 := make([]byte, 32)
	for i := range key1 {
		key1[i] = byte(i)
	}

	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(i + 50)
	}

	key3 := make([]byte, 32)
	for i := range key3 {
		key3[i] = byte(i + 100)
	}

	// Create crypt with first key for encryption
	cr1 := createTestCryptWithKey(t, hex.EncodeToString(key1))

	plaintext := "multi-key test"
	encrypted, err := cr1.EncryptLookupField(plaintext)
	if err != nil {
		t.Fatalf("EncryptLookupField failed: %v", err)
	}

	// Create crypt with multiple keys including the correct one
	cr2 := &Crypt{
		keys: []*charm.EncryptKey{
			{ID: "key-2", Key: hex.EncodeToString(key2)},
			{ID: "key-3", Key: hex.EncodeToString(key3)},
			{ID: "key-1", Key: hex.EncodeToString(key1)}, // Correct key is third
		},
	}

	// Should successfully decrypt by trying all keys
	decrypted, err := cr2.DecryptLookupField(encrypted)
	if err != nil {
		t.Fatalf("DecryptLookupField with multiple keys failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("DecryptLookupField returned %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptLookupField_InvalidHex(t *testing.T) {
	cr := createTestCrypt(t)

	// Invalid hex string
	_, err := cr.DecryptLookupField("not-valid-hex!")
	if err == nil {
		t.Error("DecryptLookupField with invalid hex should return error, got nil")
	}
}
