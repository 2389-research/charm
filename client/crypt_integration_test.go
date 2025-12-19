package client_test

import (
	"testing"

	"github.com/charmbracelet/charm/testserver"
)

func TestCryptCheck_GeneratesNewEncryptKey(t *testing.T) {
	t.Run("generates a new encrypt key when server auth has EncryptKeys == 0", func(t *testing.T) {
		cl := testserver.SetupTestServer(t)

		keys, err := cl.EncryptKeys()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(keys) != 1 {
			t.Errorf("expected 1 encrypt key, got %d", len(keys))
		}

		if keys[0].ID == "" {
			t.Error("expected non-empty key ID")
		}

		if keys[0].Key == "" {
			t.Error("expected non-empty key value")
		}

		if keys[0].PublicKey == "" {
			t.Error("expected non-empty public key")
		}
	})
}

func TestCryptCheck_Idempotent(t *testing.T) {
	t.Run("calling EncryptKeys() twice doesn't duplicate keys", func(t *testing.T) {
		cl := testserver.SetupTestServer(t)

		// First call - should create a key
		keys1, err := cl.EncryptKeys()
		if err != nil {
			t.Fatalf("first call failed: %v", err)
		}

		if len(keys1) != 1 {
			t.Fatalf("expected 1 encrypt key after first call, got %d", len(keys1))
		}

		firstKeyID := keys1[0].ID
		firstKeyValue := keys1[0].Key

		// Second call - should return the same key, not create a duplicate
		keys2, err := cl.EncryptKeys()
		if err != nil {
			t.Fatalf("second call failed: %v", err)
		}

		if len(keys2) != 1 {
			t.Errorf("expected 1 encrypt key after second call, got %d", len(keys2))
		}

		if keys2[0].ID != firstKeyID {
			t.Errorf("expected same key ID %q, got %q", firstKeyID, keys2[0].ID)
		}

		if keys2[0].Key != firstKeyValue {
			t.Errorf("expected same key value, got different value")
		}
	})
}
