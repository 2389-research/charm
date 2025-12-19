package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	charm "github.com/charmbracelet/charm/proto"
	"golang.org/x/crypto/ssh"
)

func TestAlgo(t *testing.T) {
	for k, v := range map[string]string{
		ssh.KeyAlgoRSA:        "rsa",
		ssh.KeyAlgoDSA:        "dss",
		ssh.KeyAlgoECDSA256:   "ecdsa",
		ssh.KeyAlgoSKECDSA256: "ecdsa",
		ssh.KeyAlgoECDSA384:   "ecdsa",
		ssh.KeyAlgoECDSA521:   "ecdsa",
		ssh.KeyAlgoED25519:    "ed25519",
		ssh.KeyAlgoSKED25519:  "ed25519",
	} {
		t.Run(k, func(t *testing.T) {
			got := algo(k)
			if got != v {
				t.Errorf("expected %q, got %q", v, got)
			}
		})
	}
}

func TestBitsize(t *testing.T) {
	for k, v := range map[string]int{
		ssh.KeyAlgoRSA:        3071,
		ssh.KeyAlgoDSA:        1024,
		ssh.KeyAlgoECDSA256:   256,
		ssh.KeyAlgoSKECDSA256: 256,
		ssh.KeyAlgoECDSA384:   384,
		ssh.KeyAlgoECDSA521:   521,
		ssh.KeyAlgoED25519:    256,
		ssh.KeyAlgoSKED25519:  256,
	} {
		t.Run(k, func(t *testing.T) {
			got := bitsize(k)
			if got != v {
				t.Errorf("expected %d, got %d", v, got)
			}
		})
	}
}

func TestFingerprintSHA256(t *testing.T) {
	t.Run("success on valid authorized key", func(t *testing.T) {
		// Generate a valid ed25519 key pair
		pubKey, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("failed to generate key: %v", err)
		}

		// Convert to SSH authorized key format
		sshPubKey, err := ssh.NewPublicKey(pubKey)
		if err != nil {
			t.Fatalf("failed to create SSH public key: %v", err)
		}
		authorizedKey := string(ssh.MarshalAuthorizedKey(sshPubKey))

		// Create charm.PublicKey
		charmKey := charm.PublicKey{
			Key: authorizedKey,
		}

		// Test FingerprintSHA256
		fp, err := FingerprintSHA256(charmKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the result
		if fp.Algorithm != "ed25519" {
			t.Errorf("expected algorithm %q, got %q", "ed25519", fp.Algorithm)
		}
		if fp.Type != "SHA256" {
			t.Errorf("expected type %q, got %q", "SHA256", fp.Type)
		}
		if fp.Value == "" {
			t.Error("expected non-empty fingerprint value")
		}
		if strings.HasPrefix(fp.Value, "SHA256:") {
			t.Error("fingerprint value should not have SHA256: prefix")
		}
	})

	t.Run("error on invalid key string", func(t *testing.T) {
		// Test with invalid key string
		charmKey := charm.PublicKey{
			Key: "not a valid ssh key",
		}

		_, err := FingerprintSHA256(charmKey)
		if err == nil {
			t.Error("expected error for invalid key, got nil")
		}
		if !strings.Contains(err.Error(), "failed to parse public key") {
			t.Errorf("expected error message to contain 'failed to parse public key', got: %v", err)
		}
	})
}

func TestRandomArt(t *testing.T) {
	t.Run("returns non-empty trimmed board on valid key", func(t *testing.T) {
		// Generate a valid ed25519 key pair
		pubKey, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("failed to generate key: %v", err)
		}

		// Convert to SSH authorized key format
		sshPubKey, err := ssh.NewPublicKey(pubKey)
		if err != nil {
			t.Fatalf("failed to create SSH public key: %v", err)
		}
		authorizedKey := string(ssh.MarshalAuthorizedKey(sshPubKey))

		// Create charm.PublicKey
		charmKey := charm.PublicKey{
			Key: authorizedKey,
		}

		// Test RandomArt
		board, err := RandomArt(charmKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the result
		if board == "" {
			t.Error("expected non-empty board")
		}
		if board != strings.TrimSpace(board) {
			t.Error("board should be trimmed")
		}
		// RandomArt boards typically contain box drawing characters
		if !strings.Contains(board, "+") {
			t.Error("expected board to contain box drawing characters")
		}
	})

	t.Run("error on malformed key", func(t *testing.T) {
		// Test with invalid key string
		charmKey := charm.PublicKey{
			Key: "not a valid ssh key",
		}

		_, err := RandomArt(charmKey)
		if err == nil {
			t.Error("expected error for invalid key, got nil")
		}
		if !strings.Contains(err.Error(), "failed to parse public key") {
			t.Errorf("expected error message to contain 'failed to parse public key', got: %v", err)
		}
	})
}
