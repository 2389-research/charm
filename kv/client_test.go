// ABOUTME: Unit tests for kv/client.go, focusing on encryptKeyToBadgerKey and openDB functions.
// ABOUTME: Tests cover encryption key conversion, length validation, and BadgerDB opening with invalid keys.
package kv

import (
	"encoding/hex"
	"testing"

	"github.com/charmbracelet/charm/client"
	charm "github.com/charmbracelet/charm/proto"
	badger "github.com/dgraph-io/badger/v3"
)

func TestEncryptKeyToBadgerKey_ErrorWhenKeyTooShort(t *testing.T) {
	// The function checks len([]byte(k.Key)) < 32, which means it checks
	// the length of the STRING, not the decoded hex bytes.
	// So a key needs at least 32 characters to not error.
	tests := []struct {
		name   string
		keyStr string
	}{
		{
			name:   "empty key",
			keyStr: "",
		},
		{
			name:   "key with 31 characters",
			keyStr: "0123456789abcdef0123456789abcde", // 31 chars
		},
		{
			name:   "key with 16 characters",
			keyStr: "0123456789abcdef", // 16 chars
		},
		{
			name:   "key with 1 character",
			keyStr: "a",
		},
		{
			name:   "key with 10 characters",
			keyStr: "0123456789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encryptKey := &charm.EncryptKey{
				ID:  "test-key",
				Key: tt.keyStr,
			}

			// Call encryptKeyToBadgerKey
			_, err := encryptKeyToBadgerKey(encryptKey)

			// Verify error is returned
			if err == nil {
				t.Errorf("encryptKeyToBadgerKey with %d-character key should return error, got nil", len(tt.keyStr))
			}

			// Verify error message
			expectedMsg := "encryption key is too short"
			if err.Error() != expectedMsg {
				t.Errorf("encryptKeyToBadgerKey error = %q, want %q", err.Error(), expectedMsg)
			}
		})
	}
}

func TestEncryptKeyToBadgerKey_ReturnsFirst32Bytes(t *testing.T) {
	// The function returns the first 32 bytes of the KEY STRING, not decoded bytes.
	tests := []struct {
		name   string
		keyStr string
	}{
		{
			name:   "key with exactly 32 characters",
			keyStr: "0123456789abcdef0123456789abcdef", // 32 chars
		},
		{
			name:   "key with 64 characters (typical hex-encoded 32-byte key)",
			keyStr: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", // 64 chars
		},
		{
			name:   "key with 40 characters",
			keyStr: "0123456789abcdef0123456789abcdef01234567", // 40 chars
		},
		{
			name:   "key with 100 characters",
			keyStr: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123", // 100 chars
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encryptKey := &charm.EncryptKey{
				ID:  "test-key",
				Key: tt.keyStr,
			}

			// Call encryptKeyToBadgerKey
			result, err := encryptKeyToBadgerKey(encryptKey)

			// Verify no error
			if err != nil {
				t.Fatalf("encryptKeyToBadgerKey with %d-character key returned error: %v", len(tt.keyStr), err)
			}

			// Verify result length is exactly 32 bytes
			if len(result) != 32 {
				t.Errorf("encryptKeyToBadgerKey returned %d bytes, want 32 bytes", len(result))
			}

			// Verify result contains the first 32 bytes of the original key string
			// The function uses []byte(k.Key)[0:32], taking the first 32 characters
			expectedBytes := []byte(tt.keyStr)[0:32]
			for i := 0; i < 32; i++ {
				if result[i] != expectedBytes[i] {
					t.Errorf("byte at position %d = %v, want %v", i, result[i], expectedBytes[i])
				}
			}
		})
	}
}

func TestEncryptKeyToBadgerKey_DocumentBehavior(t *testing.T) {
	// This test documents the actual behavior of encryptKeyToBadgerKey:
	// It returns the first 32 bytes of the STRING representation of the key,
	// NOT the decoded bytes. This means for a hex-encoded key, it returns
	// the first 32 hex characters (representing 16 bytes of the actual key).

	// Create a 32-byte key (64 hex characters)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1) // 01, 02, 03, ..., 20 in hex
	}
	keyHex := hex.EncodeToString(key) // "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

	encryptKey := &charm.EncryptKey{
		ID:  "test-key",
		Key: keyHex,
	}

	result, err := encryptKeyToBadgerKey(encryptKey)
	if err != nil {
		t.Fatalf("encryptKeyToBadgerKey returned error: %v", err)
	}

	// The result should be the first 32 characters of the hex string
	// "0102030405060708090a0b0c0d0e0f10" as bytes (ASCII values)
	expected := []byte(keyHex[0:32])

	if len(result) != 32 {
		t.Fatalf("result length = %d, want 32", len(result))
	}

	for i := 0; i < 32; i++ {
		if result[i] != expected[i] {
			t.Errorf("byte at position %d = %v (%c), want %v (%c)",
				i, result[i], result[i], expected[i], expected[i])
		}
	}

	// Document that this is NOT the same as the decoded key bytes
	// The first 16 bytes of the decoded key would be: 01 02 03 ... 10
	// But the function returns ASCII bytes: '0' '1' '0' '2' ... (48 49 48 50 ...)
	if result[0] == 0x01 {
		t.Error("encryptKeyToBadgerKey appears to decode the hex string, but it should return raw string bytes")
	}

	// Verify it returns ASCII hex characters
	if result[0] != '0' || result[1] != '1' {
		t.Errorf("expected ASCII hex characters, got %v %v", result[0], result[1])
	}
}

func TestOpenDB_ErrorWhenAllKeysInvalid(t *testing.T) {
	// This test verifies that openDB returns the specific error message
	// "could not open BadgerDB, bad encrypt keys" when all encryption keys
	// are invalid (too short to pass encryptKeyToBadgerKey).

	// Create a test client with invalid keys (all too short)
	invalidKeys := []*charm.EncryptKey{
		{ID: "key1", Key: "short"},      // Only 5 characters, need 32+
		{ID: "key2", Key: "alsoShort"},  // Only 9 characters, need 32+
		{ID: "key3", Key: "stillShort"}, // Only 10 characters, need 32+
	}

	// Create test client using the helper from client_test.go
	// Note: This creates a minimal client that can return encryption keys
	// but may not have full auth/config setup
	testClient := createMinimalTestClient(invalidKeys)

	// Create temporary directory for BadgerDB
	tmpDir := t.TempDir()

	// Create minimal BadgerDB options
	opts := badger.DefaultOptions(tmpDir)
	opts = opts.WithLogger(nil) // Disable logging for cleaner test output

	// Call openDB with the test client
	db, err := openDB(testClient, opts)

	// Verify we got an error
	if err == nil {
		t.Fatal("openDB with all invalid keys should return error, got nil")
		if db != nil {
			db.Close()
		}
	}

	// Verify the error message
	expectedMsg := "could not open BadgerDB, bad encrypt keys"
	if err.Error() != expectedMsg {
		t.Errorf("openDB error = %q, want %q", err.Error(), expectedMsg)
	}

	// Verify no database was opened
	if db != nil {
		t.Error("openDB returned non-nil database despite error")
		db.Close()
	}
}

// createMinimalTestClient creates a minimal client for testing purposes.
// This helper creates a client with only the fields needed for EncryptKeys() to work.
func createMinimalTestClient(keys []*charm.EncryptKey) *client.Client {
	// We need to create a client that can return encryption keys.
	// The simplest approach is to use the NewTestClient helper if it exists,
	// or create a minimal struct ourselves.

	// For this to work, we'd need the client package to export a test helper.
	// Since we added NewTestClient to client_test.go, we need to either:
	// 1. Export it (rename to NewTestClient and make it public)
	// 2. Create an export_test.go file in the client package
	// 3. Use a different approach

	// For now, let's use an export_test.go file pattern
	return client.NewTestClientWithKeys(keys)
}
