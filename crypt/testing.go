// ABOUTME: Test helpers for the crypt package, exported for use in other package tests.
// ABOUTME: Provides NewTestCrypt for creating test Crypt instances with random keys.
package crypt

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	charm "github.com/charmbracelet/charm/proto"
)

// NewTestCrypt creates a Crypt instance with a test key for testing purposes.
// This is exported so other packages can use it in tests.
func NewTestCrypt(t testing.TB) *Crypt {
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
