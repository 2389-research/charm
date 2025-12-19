package client

import (
	"strings"
	"testing"
	"time"

	charm "github.com/charmbracelet/charm/proto"
)

func TestKeyForID_EmptyID_ReturnsFirstKey(t *testing.T) {
	t.Run("returns first plaintext key when plainTextEncryptKeys already populated", func(t *testing.T) {
		cc := &Client{
			plainTextEncryptKeys: []*charm.EncryptKey{
				{
					ID:        "key-1",
					Key:       "test-key-1",
					PublicKey: "test-pub-key-1",
				},
				{
					ID:        "key-2",
					Key:       "test-key-2",
					PublicKey: "test-pub-key-2",
				},
			},
		}

		key, err := cc.KeyForID("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if key == nil {
			t.Fatal("expected key, got nil")
		}

		if key.ID != "key-1" {
			t.Errorf("expected first key with ID 'key-1', got %q", key.ID)
		}

		if key.Key != "test-key-1" {
			t.Errorf("expected key value 'test-key-1', got %q", key.Key)
		}
	})
}

func TestKeyForID_NonexistentID_ReturnsError(t *testing.T) {
	t.Run("returns error containing 'key not found for id'", func(t *testing.T) {
		cc := &Client{
			plainTextEncryptKeys: []*charm.EncryptKey{
				{
					ID:        "key-1",
					Key:       "test-key-1",
					PublicKey: "test-pub-key-1",
				},
			},
		}

		_, err := cc.KeyForID("nonexistent-id")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !strings.Contains(err.Error(), "key not found for id") {
			t.Errorf("expected error message to contain 'key not found for id', got: %v", err)
		}

		if !strings.Contains(err.Error(), "nonexistent-id") {
			t.Errorf("expected error message to contain the ID 'nonexistent-id', got: %v", err)
		}
	})
}

func TestDefaultEncryptKey(t *testing.T) {
	t.Run("returns first key when plainTextEncryptKeys populated", func(t *testing.T) {
		now := time.Now()
		cc := &Client{
			plainTextEncryptKeys: []*charm.EncryptKey{
				{
					ID:        "default-key",
					Key:       "default-value",
					PublicKey: "default-pub-key",
					CreatedAt: &now,
				},
			},
		}

		key, err := cc.DefaultEncryptKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if key.ID != "default-key" {
			t.Errorf("expected key ID 'default-key', got %q", key.ID)
		}
	})
}

func TestKeyForID_WithValidID(t *testing.T) {
	t.Run("returns correct key when valid ID provided", func(t *testing.T) {
		cc := &Client{
			plainTextEncryptKeys: []*charm.EncryptKey{
				{
					ID:        "key-1",
					Key:       "value-1",
					PublicKey: "pub-key-1",
				},
				{
					ID:        "key-2",
					Key:       "value-2",
					PublicKey: "pub-key-2",
				},
				{
					ID:        "key-3",
					Key:       "value-3",
					PublicKey: "pub-key-3",
				},
			},
		}

		key, err := cc.KeyForID("key-2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if key.ID != "key-2" {
			t.Errorf("expected key ID 'key-2', got %q", key.ID)
		}

		if key.Key != "value-2" {
			t.Errorf("expected key value 'value-2', got %q", key.Key)
		}

		if key.PublicKey != "pub-key-2" {
			t.Errorf("expected public key 'pub-key-2', got %q", key.PublicKey)
		}
	})
}
