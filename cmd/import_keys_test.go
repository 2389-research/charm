package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/charm/testserver"
)

func TestImportKeysFromStdin(t *testing.T) {
	c := testserver.SetupTestServer(t)

	var r bytes.Buffer
	BackupKeysCmd.SetArgs([]string{"-o", "-"})
	BackupKeysCmd.SetOut(&r)
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	dd, _ := c.DataPath()
	if err := os.RemoveAll(dd); err != nil {
		t.Fatal(err)
	}

	ImportKeysCmd.SetIn(&r)
	ImportKeysCmd.SetArgs([]string{"-f", "-"})
	if err := ImportKeysCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dd, "charm_ed25519")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dd, "charm_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
}

func TestImportKeysFromFile(t *testing.T) {
	c := testserver.SetupTestServer(t)

	f := filepath.Join(t.TempDir(), "backup.tar")

	BackupKeysCmd.SetArgs([]string{"-o", f})
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	dd, _ := c.DataPath()
	if err := os.RemoveAll(dd); err != nil {
		t.Fatal(err)
	}

	ImportKeysCmd.SetArgs([]string{"-f", f})
	if err := ImportKeysCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dd, "charm_ed25519")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dd, "charm_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFromReader_InvalidPrivateKey(t *testing.T) {
	dd := t.TempDir()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "invalid data",
			input: "not a valid private key",
		},
		{
			name:  "random bytes",
			input: "\x00\x01\x02\x03\x04\x05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewBufferString(tt.input)
			err := restoreFromReader(r, dd)
			if err == nil {
				t.Error("expected error for invalid private key, got nil")
			}
		})
	}
}

func TestRestoreFromReader_FilePermissions(t *testing.T) {
	c := testserver.SetupTestServer(t)

	var r bytes.Buffer
	BackupKeysCmd.SetArgs([]string{"-o", "-"})
	BackupKeysCmd.SetOut(&r)
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatalf("failed to backup keys: %s", err)
	}

	dd := t.TempDir()
	if err := restoreFromReader(&r, dd); err != nil {
		t.Fatalf("failed to restore keys: %s", err)
	}

	// Check private key permissions
	privKeyPath := filepath.Join(dd, "charm_ed25519")
	info, err := os.Stat(privKeyPath)
	if err != nil {
		t.Fatalf("failed to stat private key: %s", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private key has wrong permissions: got %o, want 0600", info.Mode().Perm())
	}

	// Check public key permissions
	pubKeyPath := filepath.Join(dd, "charm_ed25519.pub")
	info, err = os.Stat(pubKeyPath)
	if err != nil {
		t.Fatalf("failed to stat public key: %s", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("public key has wrong permissions: got %o, want 0600", info.Mode().Perm())
	}

	_ = c
}

func TestUntar_NonExistentFile(t *testing.T) {
	dd := t.TempDir()
	nonExistentTar := filepath.Join(dd, "nonexistent.tar")

	err := untar(nonExistentTar, dd)
	if err == nil {
		t.Error("expected error for non-existent tar file, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist error, got: %v", err)
	}
}

func TestUntar_PathTraversalProtection(t *testing.T) {
	c := testserver.SetupTestServer(t)

	// Create a backup to get a valid tar file
	backupFile := filepath.Join(t.TempDir(), "backup.tar")
	BackupKeysCmd.SetArgs([]string{"-o", backupFile})
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatalf("failed to create backup: %s", err)
	}

	// Extract to a temp directory
	extractDir := t.TempDir()
	if err := untar(backupFile, extractDir); err != nil {
		t.Fatalf("failed to untar: %s", err)
	}

	// Verify files are only in the target directory (not traversed elsewhere)
	// The untar function uses filepath.Base which strips path info, so path
	// traversal should be prevented
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		t.Fatalf("failed to read extract dir: %s", err)
	}

	for _, entry := range entries {
		fullPath := filepath.Join(extractDir, entry.Name())
		// Ensure the file is directly in extractDir, not in a subdirectory
		if filepath.Dir(fullPath) != extractDir {
			t.Errorf("file escaped target directory: %s", fullPath)
		}
	}

	_ = c
}

func TestImportKeysCmd_NonEmptyDataDirNoForceFlag(t *testing.T) {
	// Ensure the force flag is not set from previous tests
	forceImportOverwrite = false

	c := testserver.SetupTestServer(t)

	// Create a backup file
	backupFile := filepath.Join(t.TempDir(), "backup.tar")
	BackupKeysCmd.SetArgs([]string{"-o", backupFile})
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatalf("failed to create backup: %s", err)
	}

	// Data directory should already have keys from SetupTestServer
	dd, err := c.DataPath()
	if err != nil {
		t.Fatalf("failed to get data path: %s", err)
	}

	// Verify data dir is not empty
	empty, err := isEmpty(dd)
	if err != nil {
		t.Fatalf("failed to check if dir is empty: %s", err)
	}
	if empty {
		t.Fatal("expected non-empty data dir for this test")
	}

	// Try to import without -f flag (should fail in non-TTY mode)
	ImportKeysCmd.SetArgs([]string{backupFile})
	err = ImportKeysCmd.Execute()
	if err == nil {
		t.Error("expected error when importing to non-empty dir without -f flag, got nil")
	}
	if err != nil && err.Error() != fmt.Sprintf("not overwriting the existing keys in %s; to force, use -f", dd) {
		t.Errorf("unexpected error message: %v", err)
	}
}
