// ABOUTME: Tests for NewClient function focusing on key discovery and algorithm validation
// ABOUTME: Covers error handling, key generation, and unsupported key type rejection
package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/keygen"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

// TestNewClient_IdentityKeyNonExistent tests that NewClient returns an error
// when Config.IdentityKey points to a non-existent file.
func TestNewClient_IdentityKeyNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentKey := filepath.Join(tmpDir, "nonexistent_key")

	cfg := &Config{
		Host:        "test.charm.sh",
		SSHPort:     35353,
		HTTPPort:    35354,
		KeyType:     "ed25519",
		IdentityKey: nonExistentKey,
	}

	_, err := NewClient(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent identity key, got nil")
	}

	// The error should be about not being able to read the file or missing SSH auth
	errMsg := err.Error()
	if !strings.Contains(errMsg, "no such file") && !strings.Contains(errMsg, "missing ssh auth") {
		t.Errorf("expected error about missing file or SSH auth, got: %v", err)
	}
}

// TestNewClient_GeneratesKeysWhenNoneExist tests that NewClient generates keys
// when no keys exist in DataPath and proceeds successfully.
func TestNewClient_GeneratesKeysWhenNoneExist(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		Host:     "test.charm.sh",
		SSHPort:  35353,
		HTTPPort: 35354,
		KeyType:  "ed25519",
		DataDir:  tmpDir,
	}

	// Verify no keys exist initially
	dataPath := filepath.Join(tmpDir, cfg.Host)
	matches, err := filepath.Glob(filepath.Join(dataPath, "charm_*"))
	if err != nil {
		t.Fatalf("failed to check for existing keys: %v", err)
	}
	if len(matches) > 0 {
		t.Fatalf("expected no keys initially, found: %v", matches)
	}

	// Create client - should generate keys
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("expected NewClient to succeed and generate keys, got error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// Verify keys were generated
	privateKeyPath := filepath.Join(dataPath, "charm_ed25519")
	publicKeyPath := filepath.Join(dataPath, "charm_ed25519.pub")

	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Errorf("expected private key to exist at %s", privateKeyPath)
	}
	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Errorf("expected public key to exist at %s", publicKeyPath)
	}

	// Verify the client has the correct key path
	if len(client.authKeyPaths) != 1 {
		t.Fatalf("expected 1 auth key path, got %d", len(client.authKeyPaths))
	}
	if client.authKeyPaths[0] != privateKeyPath {
		t.Errorf("expected auth key path %s, got %s", privateKeyPath, client.authKeyPaths[0])
	}
}

// TestNewClient_GeneratesRSAKeys tests that NewClient can generate RSA keys
// when KeyType is set to "rsa".
func TestNewClient_GeneratesRSAKeys(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		Host:     "test.charm.sh",
		SSHPort:  35353,
		HTTPPort: 35354,
		KeyType:  "rsa",
		DataDir:  tmpDir,
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("expected NewClient to succeed with RSA keys, got error: %v", err)
	}

	// Verify RSA keys were generated
	dataPath := filepath.Join(tmpDir, cfg.Host)
	privateKeyPath := filepath.Join(dataPath, "charm_rsa")
	publicKeyPath := filepath.Join(dataPath, "charm_rsa.pub")

	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Errorf("expected RSA private key to exist at %s", privateKeyPath)
	}
	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Errorf("expected RSA public key to exist at %s", publicKeyPath)
	}

	if len(client.authKeyPaths) != 1 {
		t.Fatalf("expected 1 auth key path, got %d", len(client.authKeyPaths))
	}
	if client.authKeyPaths[0] != privateKeyPath {
		t.Errorf("expected auth key path %s, got %s", privateKeyPath, client.authKeyPaths[0])
	}
}

// TestCheckKeyAlgo_RejectsECDSA tests that checkKeyAlgo rejects ECDSA keys
// with an appropriate error message.
func TestCheckKeyAlgo_RejectsECDSA(t *testing.T) {
	// Generate an ECDSA key
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	// Convert to SSH signer
	signer, err := ssh.NewSignerFromKey(ecdsaKey)
	if err != nil {
		t.Fatalf("failed to create SSH signer from ECDSA key: %v", err)
	}

	// checkKeyAlgo should reject this
	err = checkKeyAlgo(signer)
	if err == nil {
		t.Fatal("expected checkKeyAlgo to reject ECDSA key, got nil error")
	}

	expectedMsg := "we don't support"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("expected error message to contain %q, got: %v", expectedMsg, err)
	}

	if !strings.Contains(err.Error(), "ecdsa") {
		t.Errorf("expected error message to mention 'ecdsa', got: %v", err)
	}
}

// TestCheckKeyAlgo_AcceptsED25519 tests that checkKeyAlgo accepts ED25519 keys.
func TestCheckKeyAlgo_AcceptsED25519(t *testing.T) {
	// Generate an ED25519 key
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ED25519 key: %v", err)
	}

	// Convert to SSH signer
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("failed to create SSH signer from ED25519 key: %v", err)
	}

	// checkKeyAlgo should accept this
	err = checkKeyAlgo(signer)
	if err != nil {
		t.Errorf("expected checkKeyAlgo to accept ED25519 key, got error: %v", err)
	}
}

// TestCheckKeyAlgo_AcceptsRSA tests that checkKeyAlgo accepts RSA keys.
func TestCheckKeyAlgo_AcceptsRSA(t *testing.T) {
	// Generate an RSA key
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Convert to SSH signer
	signer, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatalf("failed to create SSH signer from RSA key: %v", err)
	}

	// checkKeyAlgo should accept this
	err = checkKeyAlgo(signer)
	if err != nil {
		t.Errorf("expected checkKeyAlgo to accept RSA key, got error: %v", err)
	}
}

// TestCheckKeyAlgo_RejectsDSA tests that checkKeyAlgo rejects DSA keys.
func TestCheckKeyAlgo_RejectsDSA(t *testing.T) {
	// We'll create a mock signer that reports DSA key type
	mockSigner := &mockSSHSigner{
		keyType: ssh.KeyAlgoDSA,
	}

	err := checkKeyAlgo(mockSigner)
	if err == nil {
		t.Fatal("expected checkKeyAlgo to reject DSA key, got nil error")
	}

	expectedMsg := "we don't support"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

// TestFindAuthKeys_ReturnsExactMatchOnly tests that findAuthKeys returns
// only the exact match charm_<keyType> and ignores .pub files and other files.
func TestFindAuthKeys_ReturnsExactMatchOnly(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		Host:     "test.charm.sh",
		SSHPort:  35353,
		HTTPPort: 35354,
		KeyType:  "ed25519",
		DataDir:  tmpDir,
	}

	client := &Client{Config: cfg}
	dataPath := filepath.Join(tmpDir, cfg.Host)

	// Create the directory
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		t.Fatalf("failed to create data path: %v", err)
	}

	// Create various files to test filtering
	filesToCreate := []string{
		"charm_ed25519",     // Should match
		"charm_ed25519.pub", // Should not match
		"charm_rsa",         // Should not match (different key type)
		"charm_ed25519_old", // Should not match (not exact)
		"other_file",        // Should not match
	}

	for _, filename := range filesToCreate {
		path := filepath.Join(dataPath, filename)
		if err := os.WriteFile(path, []byte("dummy content"), 0o600); err != nil {
			t.Fatalf("failed to create test file %s: %v", filename, err)
		}
	}

	// Call findAuthKeys
	found, err := client.findAuthKeys("ed25519")
	if err != nil {
		t.Fatalf("findAuthKeys returned error: %v", err)
	}

	// Should find exactly one match
	if len(found) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(found), found)
	}

	expectedPath := filepath.Join(dataPath, "charm_ed25519")
	if found[0] != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, found[0])
	}
}

// TestFindAuthKeys_ReturnsEmptyWhenNoMatch tests that findAuthKeys returns
// an empty slice when no matching keys are found.
func TestFindAuthKeys_ReturnsEmptyWhenNoMatch(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		Host:     "test.charm.sh",
		SSHPort:  35353,
		HTTPPort: 35354,
		KeyType:  "ed25519",
		DataDir:  tmpDir,
	}

	client := &Client{Config: cfg}
	dataPath := filepath.Join(tmpDir, cfg.Host)

	// Create the directory
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		t.Fatalf("failed to create data path: %v", err)
	}

	// Create files that don't match
	filesToCreate := []string{
		"charm_rsa",         // Wrong key type
		"charm_ed25519.pub", // Public key
		"other_file",        // Not a charm key
	}

	for _, filename := range filesToCreate {
		path := filepath.Join(dataPath, filename)
		if err := os.WriteFile(path, []byte("dummy content"), 0o600); err != nil {
			t.Fatalf("failed to create test file %s: %v", filename, err)
		}
	}

	// Call findAuthKeys
	found, err := client.findAuthKeys("ed25519")
	if err != nil {
		t.Fatalf("findAuthKeys returned error: %v", err)
	}

	// Should find no matches
	if len(found) != 0 {
		t.Errorf("expected 0 matches, got %d: %v", len(found), found)
	}
}

// TestNewClient_UsesExistingKeys tests that NewClient uses existing keys
// rather than generating new ones when keys already exist.
func TestNewClient_UsesExistingKeys(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		Host:     "test.charm.sh",
		SSHPort:  35353,
		HTTPPort: 35354,
		KeyType:  "ed25519",
		DataDir:  tmpDir,
	}

	dataPath := filepath.Join(tmpDir, cfg.Host)
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		t.Fatalf("failed to create data path: %v", err)
	}

	// Generate a key using keygen
	keyPath := filepath.Join(dataPath, "charm_ed25519")
	_, err := keygen.New(keyPath, keygen.WithKeyType(keygen.Ed25519), keygen.WithWrite())
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	// Read the original private key content
	originalContent, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read original key: %v", err)
	}

	// Create client - should use existing keys
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("expected NewClient to succeed with existing keys, got error: %v", err)
	}

	// Verify the same key is being used (content unchanged)
	currentContent, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read current key: %v", err)
	}

	if string(currentContent) != string(originalContent) {
		t.Error("expected existing key to be used, but content changed")
	}

	// Verify the client has the correct key path
	if len(client.authKeyPaths) != 1 {
		t.Fatalf("expected 1 auth key path, got %d", len(client.authKeyPaths))
	}
	if client.authKeyPaths[0] != keyPath {
		t.Errorf("expected auth key path %s, got %s", keyPath, client.authKeyPaths[0])
	}
}

// mockSSHSigner is a mock implementation of ssh.Signer for testing.
type mockSSHSigner struct {
	keyType string
}

func (m *mockSSHSigner) PublicKey() ssh.PublicKey {
	return &mockPublicKey{keyType: m.keyType}
}

func (m *mockSSHSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return nil, nil
}

// mockPublicKey is a mock implementation of ssh.PublicKey for testing.
type mockPublicKey struct {
	keyType string
}

func (m *mockPublicKey) Type() string {
	return m.keyType
}

func (m *mockPublicKey) Marshal() []byte {
	return []byte{}
}

func (m *mockPublicKey) Verify([]byte, *ssh.Signature) error {
	return nil
}
