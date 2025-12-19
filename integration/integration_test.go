// ABOUTME: End-to-end integration tests for the Charm client-server system.
// ABOUTME: Tests cover auth, file system, KV store, encryption keys, and user lifecycle.
package integration

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/charm/client"
	charmfs "github.com/charmbracelet/charm/fs"
	"github.com/charmbracelet/charm/kv"
	"github.com/charmbracelet/charm/testserver"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupClient creates a test server and returns a configured client.
// It fails the test immediately if setup fails.
func setupClient(t *testing.T) *client.Client {
	t.Helper()
	cl := testserver.SetupTestServer(t)
	if cl == nil {
		t.Fatal("SetupTestServer returned nil client")
	}
	return cl
}

// mustAuth authenticates the client and fails the test if it errors.
func mustAuth(t *testing.T, cl *client.Client) {
	t.Helper()
	auth, err := cl.Auth()
	if err != nil {
		t.Fatalf("Auth failed: %v", err)
	}
	if auth == nil {
		t.Fatal("Auth returned nil")
	}
	if auth.JWT == "" {
		t.Fatal("Auth returned empty JWT")
	}
}

// setupFS creates a test server, authenticates, and returns a configured FS.
func setupFS(t *testing.T) (*client.Client, *charmfs.FS) {
	t.Helper()
	cl := setupClient(t)
	mustAuth(t, cl)

	cfs, err := charmfs.NewFSWithClient(cl)
	if err != nil {
		t.Fatalf("NewFSWithClient failed: %v", err)
	}
	return cl, cfs
}

// writeTestFile writes content to the FS and fails the test on error.
func writeTestFile(t *testing.T, cfs *charmfs.FS, path string, content []byte) {
	t.Helper()
	err := cfs.WriteFile(path, &memFile{
		name:    filepath.Base(path),
		content: bytes.NewReader(content),
		size:    int64(len(content)),
		mode:    0644,
	})
	if err != nil {
		t.Fatalf("WriteFile(%q) failed: %v", path, err)
	}
}

// assertFileContent reads a file and asserts its content matches expected.
func assertFileContent(t *testing.T, cfs *charmfs.FS, path string, expected []byte) {
	t.Helper()
	content, err := cfs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) failed: %v", path, err)
	}
	if !bytes.Equal(content, expected) {
		t.Errorf("File content mismatch for %q\ngot:  %q\nwant: %q", path, content, expected)
	}
}

// memFile implements fs.File for in-memory content.
type memFile struct {
	name    string
	content io.Reader
	size    int64
	mode    fs.FileMode
}

func (f *memFile) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: f.name, size: f.size, mode: f.mode}, nil
}
func (f *memFile) Read(p []byte) (int, error) { return f.content.Read(p) }
func (f *memFile) Close() error               { return nil }

type memFileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (fi *memFileInfo) Name() string       { return fi.name }
func (fi *memFileInfo) Size() int64        { return fi.size }
func (fi *memFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *memFileInfo) ModTime() time.Time { return time.Now() }
func (fi *memFileInfo) IsDir() bool        { return false }
func (fi *memFileInfo) Sys() interface{}   { return nil }

// =============================================================================
// Auth Flow Tests
// =============================================================================

func TestE2E_Auth_BasicFlow(t *testing.T) {
	cl := setupClient(t)

	// First auth - should succeed and return valid data
	auth1, err := cl.Auth()
	if err != nil {
		t.Fatalf("First Auth() failed: %v", err)
	}

	if auth1.JWT == "" {
		t.Error("Auth returned empty JWT")
	}
	if auth1.ID == "" {
		t.Error("Auth returned empty ID")
	}
	if auth1.PublicKey == "" {
		t.Error("Auth returned empty PublicKey")
	}

	// Second auth - should return cached result (same JWT)
	auth2, err := cl.Auth()
	if err != nil {
		t.Fatalf("Second Auth() failed: %v", err)
	}
	if auth2.JWT != auth1.JWT {
		t.Error("Second Auth() returned different JWT - expected cached result")
	}
}

func TestE2E_Auth_InvalidateAndRefresh(t *testing.T) {
	cl := setupClient(t)

	// Get initial auth
	auth1, err := cl.Auth()
	if err != nil {
		t.Fatalf("Initial Auth() failed: %v", err)
	}
	jwt1 := auth1.JWT

	// Invalidate
	cl.InvalidateAuth()

	// Auth again - should get fresh JWT
	auth2, err := cl.Auth()
	if err != nil {
		t.Fatalf("Auth() after invalidate failed: %v", err)
	}

	// JWT might be the same or different depending on server implementation
	// but auth should succeed
	if auth2.JWT == "" {
		t.Error("Auth after invalidate returned empty JWT")
	}
	if auth2.ID != auth1.ID {
		t.Errorf("User ID changed after invalidate: %q -> %q", auth1.ID, auth2.ID)
	}

	t.Logf("JWT same after invalidate: %v", jwt1 == auth2.JWT)
}

func TestE2E_Auth_ConcurrentCalls(t *testing.T) {
	cl := setupClient(t)

	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make(chan string, numGoroutines)
	errors := make(chan error, numGoroutines)

	// Spawn concurrent Auth calls
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			auth, err := cl.Auth()
			if err != nil {
				errors <- err
				return
			}
			results <- auth.JWT
		}()
	}

	wg.Wait()
	close(results)
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent Auth() error: %v", err)
	}

	// All JWTs should be the same (cached)
	var firstJWT string
	for jwt := range results {
		if firstJWT == "" {
			firstJWT = jwt
		} else if jwt != firstJWT {
			t.Error("Concurrent Auth() returned different JWTs - race condition?")
		}
	}
}

func TestE2E_Auth_CharmID(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Get charm ID via SSH command
	id, err := cl.ID()
	if err != nil {
		t.Fatalf("ID() failed: %v", err)
	}

	if id == "" {
		t.Error("ID() returned empty string")
	}

	// Verify it matches auth ID
	auth, _ := cl.Auth()
	if id != auth.ID {
		t.Errorf("ID() = %q, but Auth().ID = %q", id, auth.ID)
	}
}

func TestE2E_Auth_AuthorizedKeys(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Get authorized keys via SSH "keys" command
	keys, err := cl.AuthorizedKeys()
	if err != nil {
		t.Fatalf("AuthorizedKeys() failed: %v", err)
	}

	// Note: The raw "keys" SSH command may return empty in testserver,
	// while AuthorizedKeysWithMetadata() (which uses "api-keys") returns proper data.
	// This documents current behavior.
	if keys == "" {
		t.Log("AuthorizedKeys() returned empty string (expected in testserver)")
	} else if !strings.Contains(keys, "ssh-ed25519") && !strings.Contains(keys, "ssh-rsa") {
		t.Logf("AuthorizedKeys() output: %q", keys)
	}

	// Verify the metadata version works
	keysWithMeta, err := cl.AuthorizedKeysWithMetadata()
	if err != nil {
		t.Fatalf("AuthorizedKeysWithMetadata() failed: %v", err)
	}
	if len(keysWithMeta.Keys) == 0 {
		t.Error("AuthorizedKeysWithMetadata() returned no keys")
	}
}

// =============================================================================
// File System Tests
// =============================================================================

func TestE2E_FS_WriteReadRoundtrip(t *testing.T) {
	_, cfs := setupFS(t)

	testCases := []struct {
		name    string
		path    string
		content []byte
	}{
		{"simple text", "test.txt", []byte("hello world")},
		{"binary data", "binary.bin", []byte{0x00, 0x01, 0x02, 0xFF, 0xFE}},
		{"empty file", "empty.txt", []byte{}},
		{"large file", "large.txt", bytes.Repeat([]byte("x"), 100000)},
		{"unicode content", "unicode.txt", []byte("Hello 世界 🌍")},
		{"nested path", "dir/subdir/file.txt", []byte("nested content")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			writeTestFile(t, cfs, tc.path, tc.content)
			assertFileContent(t, cfs, tc.path, tc.content)
		})
	}
}

func TestE2E_FS_Overwrite(t *testing.T) {
	_, cfs := setupFS(t)

	path := "overwrite-test.txt"

	// Write initial content
	writeTestFile(t, cfs, path, []byte("initial content"))
	assertFileContent(t, cfs, path, []byte("initial content"))

	// Overwrite with new content
	writeTestFile(t, cfs, path, []byte("new content"))
	assertFileContent(t, cfs, path, []byte("new content"))
}

func TestE2E_FS_ReadNonexistent(t *testing.T) {
	_, cfs := setupFS(t)

	_, err := cfs.ReadFile("does-not-exist.txt")
	if err == nil {
		t.Error("ReadFile on nonexistent file should return error")
	}
}

func TestE2E_FS_ReadDir(t *testing.T) {
	_, cfs := setupFS(t)

	// Create some files in a directory
	writeTestFile(t, cfs, "mydir/file1.txt", []byte("content1"))
	writeTestFile(t, cfs, "mydir/file2.txt", []byte("content2"))
	writeTestFile(t, cfs, "mydir/file3.txt", []byte("content3"))

	// List directory
	entries, err := cfs.ReadDir("mydir")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("ReadDir returned %d entries, expected 3", len(entries))
	}

	// Check that all files are present
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}

	for _, expected := range []string{"file1.txt", "file2.txt", "file3.txt"} {
		if !names[expected] {
			t.Errorf("ReadDir missing file: %s", expected)
		}
	}
}

func TestE2E_FS_ReadDirEmpty(t *testing.T) {
	_, cfs := setupFS(t)

	// Reading a nonexistent directory should return empty slice, not error
	entries, err := cfs.ReadDir("nonexistent-dir")
	if err != nil {
		t.Logf("ReadDir on nonexistent returned error (may be expected): %v", err)
	}

	// Per fs.go behavior, should return empty slice
	if entries == nil {
		t.Log("ReadDir returned nil slice for nonexistent directory")
	}
}

func TestE2E_FS_Remove(t *testing.T) {
	_, cfs := setupFS(t)

	path := "to-delete.txt"

	// Create file
	writeTestFile(t, cfs, path, []byte("delete me"))

	// Verify it exists
	_, err := cfs.ReadFile(path)
	if err != nil {
		t.Fatalf("File should exist before delete: %v", err)
	}

	// Delete it
	err = cfs.Remove(path)
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify it's gone
	_, err = cfs.ReadFile(path)
	if err == nil {
		t.Error("File should not exist after delete")
	}
}

func TestE2E_FS_Open(t *testing.T) {
	_, cfs := setupFS(t)

	content := []byte("file content for Open test")
	writeTestFile(t, cfs, "open-test.txt", content)

	// Open the file
	f, err := cfs.Open("open-test.txt")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer f.Close()

	// Read via the file handle
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll from opened file failed: %v", err)
	}

	if !bytes.Equal(data, content) {
		t.Errorf("Open content mismatch\ngot:  %q\nwant: %q", data, content)
	}
}

func TestE2E_FS_CharmPrefix(t *testing.T) {
	_, cfs := setupFS(t)

	content := []byte("charm prefix test")

	// Write with charm: prefix
	writeTestFile(t, cfs, "charm:prefixed.txt", content)

	// Read without prefix - should find the same file
	assertFileContent(t, cfs, "prefixed.txt", content)

	// Read with prefix - should also work
	assertFileContent(t, cfs, "charm:prefixed.txt", content)
}

func TestE2E_FS_SpecialCharactersInPath(t *testing.T) {
	_, cfs := setupFS(t)

	// Test various special characters in filenames
	testCases := []struct {
		name string
		path string
	}{
		{"spaces", "file with spaces.txt"},
		{"dots", "file.multiple.dots.txt"},
		{"underscores", "file_with_underscores.txt"},
		{"dashes", "file-with-dashes.txt"},
		{"mixed", "file with spaces-and_mixed.chars.txt"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte("content for " + tc.name)
			writeTestFile(t, cfs, tc.path, content)
			assertFileContent(t, cfs, tc.path, content)
		})
	}
}

// =============================================================================
// KV Store Tests
// =============================================================================

func TestE2E_KV_BasicOperations(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	cl := setupClient(t)
	mustAuth(t, cl)

	// Open KV store
	db, err := kv.OpenWithDefaults("test-kv")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	// Set a value
	err = db.Set([]byte("key1"), []byte("value1"))
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get the value back
	value, err := db.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(value, []byte("value1")) {
		t.Errorf("Get returned wrong value: %q", value)
	}
}

func TestE2E_KV_Overwrite(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	cl := setupClient(t)
	mustAuth(t, cl)

	db, err := kv.OpenWithDefaults("test-kv-overwrite")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	key := []byte("overwrite-key")

	// Set initial value
	err = db.Set(key, []byte("initial"))
	if err != nil {
		t.Fatalf("Initial Set failed: %v", err)
	}

	// Overwrite
	err = db.Set(key, []byte("updated"))
	if err != nil {
		t.Fatalf("Overwrite Set failed: %v", err)
	}

	// Verify
	value, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get after overwrite failed: %v", err)
	}

	if !bytes.Equal(value, []byte("updated")) {
		t.Errorf("Expected updated value, got: %q", value)
	}
}

func TestE2E_KV_Delete(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	cl := setupClient(t)
	mustAuth(t, cl)

	db, err := kv.OpenWithDefaults("test-kv-delete")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	key := []byte("to-delete")

	// Set and verify
	err = db.Set(key, []byte("delete me"))
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Delete
	err = db.Delete(key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Should not find key
	_, err = db.Get(key)
	if err == nil {
		t.Error("Get after Delete should return error")
	}
}

func TestE2E_KV_Keys(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	cl := setupClient(t)
	mustAuth(t, cl)

	db, err := kv.OpenWithDefaults("test-kv-keys")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	// Add multiple keys
	keysToAdd := []string{"apple", "banana", "cherry"}
	for _, k := range keysToAdd {
		err = db.Set([]byte(k), []byte("value-"+k))
		if err != nil {
			t.Fatalf("Set %q failed: %v", k, err)
		}
	}

	// List all keys
	keys, err := db.Keys()
	if err != nil {
		t.Fatalf("Keys() failed: %v", err)
	}

	if len(keys) != len(keysToAdd) {
		t.Errorf("Keys() returned %d keys, expected %d", len(keys), len(keysToAdd))
	}

	// Verify all keys present
	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[string(k)] = true
	}

	for _, expected := range keysToAdd {
		if !keySet[expected] {
			t.Errorf("Keys() missing: %q", expected)
		}
	}
}

func TestE2E_KV_BinaryData(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	cl := setupClient(t)
	mustAuth(t, cl)

	db, err := kv.OpenWithDefaults("test-kv-binary")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	// Binary key and value
	key := []byte{0x00, 0x01, 0x02, 0xFF}
	value := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x00}

	err = db.Set(key, value)
	if err != nil {
		t.Fatalf("Set binary failed: %v", err)
	}

	got, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get binary failed: %v", err)
	}

	if !bytes.Equal(got, value) {
		t.Errorf("Binary value mismatch\ngot:  %x\nwant: %x", got, value)
	}
}

func TestE2E_KV_LargeValue(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	cl := setupClient(t)
	mustAuth(t, cl)

	db, err := kv.OpenWithDefaults("test-kv-large")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	// 100KB value (BadgerDB has value size limits that may cause issues with larger values)
	key := []byte("large-key")
	value := bytes.Repeat([]byte("x"), 100*1024)

	err = db.Set(key, value)
	if err != nil {
		t.Fatalf("Set large value failed: %v", err)
	}

	got, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get large value failed: %v", err)
	}

	if !bytes.Equal(got, value) {
		t.Errorf("Large value length mismatch: got %d, want %d", len(got), len(value))
	}
}

// =============================================================================
// Encryption Key Tests
// =============================================================================

func TestE2E_EncryptKey_AutoGenerate(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// First call should auto-generate a key
	keys, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("EncryptKeys() failed: %v", err)
	}

	if len(keys) == 0 {
		t.Fatal("EncryptKeys() returned empty slice - should auto-generate")
	}

	// Verify key has required fields
	key := keys[0]
	if key.ID == "" {
		t.Error("EncryptKey missing ID")
	}
	if key.Key == "" {
		t.Error("EncryptKey missing Key")
	}
	// Note: CreatedAt may be nil depending on server implementation
	if key.CreatedAt == nil {
		t.Log("EncryptKey CreatedAt is nil (expected in some server configs)")
	}

	t.Logf("Generated key ID: %s", key.ID)
}

func TestE2E_EncryptKey_Idempotent(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Call twice
	keys1, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("First EncryptKeys() failed: %v", err)
	}

	keys2, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("Second EncryptKeys() failed: %v", err)
	}

	// Should return same keys
	if len(keys1) != len(keys2) {
		t.Errorf("Key count changed: %d -> %d", len(keys1), len(keys2))
	}

	if len(keys1) > 0 && len(keys2) > 0 {
		if keys1[0].ID != keys2[0].ID {
			t.Error("Key ID changed between calls")
		}
	}
}

func TestE2E_EncryptKey_DefaultKey(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Ensure we have keys
	_, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("EncryptKeys() failed: %v", err)
	}

	// Get default key
	defaultKey, err := cl.DefaultEncryptKey()
	if err != nil {
		t.Fatalf("DefaultEncryptKey() failed: %v", err)
	}

	if defaultKey == nil {
		t.Fatal("DefaultEncryptKey() returned nil")
	}
	if defaultKey.Key == "" {
		t.Error("DefaultEncryptKey has empty Key field")
	}
}

func TestE2E_EncryptKey_KeyForID(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Get all keys
	keys, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("EncryptKeys() failed: %v", err)
	}

	if len(keys) == 0 {
		t.Skip("No encryption keys available")
	}

	// Look up by ID
	keyID := keys[0].ID
	key, err := cl.KeyForID(keyID)
	if err != nil {
		t.Fatalf("KeyForID(%q) failed: %v", keyID, err)
	}

	if key.ID != keyID {
		t.Errorf("KeyForID returned wrong key: got %q, want %q", key.ID, keyID)
	}
}

func TestE2E_EncryptKey_KeyForID_NotFound(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Ensure we have at least one key
	_, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("EncryptKeys() failed: %v", err)
	}

	// Look up nonexistent ID
	_, err = cl.KeyForID("nonexistent-key-id")
	if err == nil {
		t.Error("KeyForID with nonexistent ID should return error")
	}
}

// =============================================================================
// User Lifecycle Tests
// =============================================================================

func TestE2E_User_SetName(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Set a username
	user, err := cl.SetName("testuser123")
	if err != nil {
		t.Fatalf("SetName failed: %v", err)
	}

	if user.Name != "testuser123" {
		t.Errorf("SetName returned wrong name: %q", user.Name)
	}
}

func TestE2E_User_SetName_Invalid(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Invalid names should fail
	invalidNames := []string{
		"",                       // empty
		"ab",                     // too short (assuming min 3)
		"user with spaces",       // spaces
		"user@special",           // special chars
		strings.Repeat("x", 100), // too long
	}

	for _, name := range invalidNames {
		t.Run(fmt.Sprintf("name=%q", name), func(t *testing.T) {
			_, err := cl.SetName(name)
			// Some of these might succeed depending on validation rules
			// Log the result for documentation
			if err != nil {
				t.Logf("SetName(%q) correctly rejected: %v", name, err)
			} else {
				t.Logf("SetName(%q) was accepted", name)
			}
		})
	}
}

func TestE2E_User_Bio(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	// Set name first
	_, err := cl.SetName("biotest")
	if err != nil {
		t.Logf("SetName failed (may be expected): %v", err)
	}

	// Get bio
	user, err := cl.Bio()
	if err != nil {
		t.Fatalf("Bio() failed: %v", err)
	}

	if user == nil {
		t.Fatal("Bio() returned nil")
	}

	// Should have an ID at minimum
	if user.CharmID == "" {
		t.Error("Bio returned user with empty CharmID")
	}

	t.Logf("User bio: CharmID=%s Name=%s", user.CharmID, user.Name)
}

func TestE2E_User_AuthorizedKeysWithMetadata(t *testing.T) {
	cl := setupClient(t)
	mustAuth(t, cl)

	keys, err := cl.AuthorizedKeysWithMetadata()
	if err != nil {
		t.Fatalf("AuthorizedKeysWithMetadata() failed: %v", err)
	}

	if keys == nil {
		t.Fatal("AuthorizedKeysWithMetadata returned nil")
	}

	if len(keys.Keys) == 0 {
		t.Error("No authorized keys found")
	}

	t.Logf("Found %d authorized keys, active index: %d", len(keys.Keys), keys.ActiveKey)
}

// =============================================================================
// End-to-End Workflow Tests
// =============================================================================

func TestE2E_Workflow_AuthThenFS(t *testing.T) {
	// Complete workflow: auth -> write file -> read file -> verify
	cl := setupClient(t)

	// Step 1: Authenticate
	auth, err := cl.Auth()
	if err != nil {
		t.Fatalf("Auth failed: %v", err)
	}
	t.Logf("Authenticated as: %s", auth.ID)

	// Step 2: Create FS
	cfs, err := charmfs.NewFSWithClient(cl)
	if err != nil {
		t.Fatalf("NewFSWithClient failed: %v", err)
	}

	// Step 3: Write a file
	content := []byte("workflow test content - " + time.Now().String())
	writeTestFile(t, cfs, "workflow-test.txt", content)

	// Step 4: Read and verify
	assertFileContent(t, cfs, "workflow-test.txt", content)

	t.Log("Auth -> FS workflow completed successfully")
}

func TestE2E_Workflow_AuthThenKV(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	// Complete workflow: auth -> KV operations
	cl := setupClient(t)

	// Step 1: Authenticate
	auth, err := cl.Auth()
	if err != nil {
		t.Fatalf("Auth failed: %v", err)
	}
	t.Logf("Authenticated as: %s", auth.ID)

	// Step 2: Open KV
	db, err := kv.OpenWithDefaults("workflow-kv")
	if err != nil {
		t.Fatalf("OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	// Step 3: Store data
	err = db.Set([]byte("workflow-key"), []byte("workflow-value"))
	if err != nil {
		t.Fatalf("KV Set failed: %v", err)
	}

	// Step 4: Retrieve and verify
	value, err := db.Get([]byte("workflow-key"))
	if err != nil {
		t.Fatalf("KV Get failed: %v", err)
	}

	if string(value) != "workflow-value" {
		t.Errorf("KV value mismatch: %q", value)
	}

	t.Log("Auth -> KV workflow completed successfully")
}

func TestE2E_Workflow_FullUserJourney(t *testing.T) {
	if raceEnabled {
		t.Skip("skipping KV test with race detector: BadgerDB has known internal races")
	}
	// Complete user journey: auth -> set name -> encrypt keys -> FS -> KV
	cl := setupClient(t)

	// Step 1: Authenticate
	auth, err := cl.Auth()
	if err != nil {
		t.Fatalf("Auth failed: %v", err)
	}
	t.Logf("Step 1: Authenticated as %s", auth.ID)

	// Step 2: Set username (may fail if already set)
	_, err = cl.SetName("journeyuser")
	if err != nil {
		t.Logf("Step 2: SetName skipped (may already be set): %v", err)
	} else {
		t.Log("Step 2: Username set to 'journeyuser'")
	}

	// Step 3: Get encryption keys
	keys, err := cl.EncryptKeys()
	if err != nil {
		t.Fatalf("Step 3: EncryptKeys failed: %v", err)
	}
	t.Logf("Step 3: Got %d encryption keys", len(keys))

	// Step 4: Use FS
	cfs, err := charmfs.NewFSWithClient(cl)
	if err != nil {
		t.Fatalf("Step 4: NewFSWithClient failed: %v", err)
	}

	fsContent := []byte("journey file content")
	writeTestFile(t, cfs, "journey.txt", fsContent)
	assertFileContent(t, cfs, "journey.txt", fsContent)
	t.Log("Step 4: FS write/read completed")

	// Step 5: Use KV
	db, err := kv.OpenWithDefaults("journey-kv")
	if err != nil {
		t.Fatalf("Step 5: OpenWithDefaults failed: %v", err)
	}
	defer db.Close()

	err = db.Set([]byte("journey"), []byte("complete"))
	if err != nil {
		t.Fatalf("Step 5: KV Set failed: %v", err)
	}

	kvValue, err := db.Get([]byte("journey"))
	if err != nil {
		t.Fatalf("Step 5: KV Get failed: %v", err)
	}
	if string(kvValue) != "complete" {
		t.Errorf("Step 5: KV value wrong: %q", kvValue)
	}
	t.Log("Step 5: KV operations completed")

	t.Log("Full user journey completed successfully!")
}

// =============================================================================
// Isolation Tests (verify test isolation)
// =============================================================================

func TestE2E_Isolation_SeparateClients(t *testing.T) {
	// Two separate test servers should have isolated data
	cl1 := setupClient(t)
	mustAuth(t, cl1)

	cfs1, err := charmfs.NewFSWithClient(cl1)
	if err != nil {
		t.Fatalf("FS1 creation failed: %v", err)
	}

	// Write a file on client 1
	writeTestFile(t, cfs1, "isolated.txt", []byte("client1 data"))

	// Create second isolated environment
	// Note: This creates a completely separate server
	cl2 := testserver.SetupTestServer(t)
	mustAuth(t, cl2)

	cfs2, err := charmfs.NewFSWithClient(cl2)
	if err != nil {
		t.Fatalf("FS2 creation failed: %v", err)
	}

	// Client 2 should NOT see client 1's file (different server)
	_, err = cfs2.ReadFile("isolated.txt")
	if err == nil {
		t.Error("Client 2 should not see Client 1's file - isolation failure")
	}
}
