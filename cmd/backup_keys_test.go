package cmd

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/charm/testserver"
	"golang.org/x/crypto/ssh"
)

func TestBackupKeysCmd(t *testing.T) {
	backupFilePath := "./charm-keys-backup.tar"
	_ = os.RemoveAll(backupFilePath)
	_ = testserver.SetupTestServer(t)

	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatalf("command failed: %s", err)
	}

	f, err := os.Open(backupFilePath)
	if err != nil {
		t.Fatalf("error opening tar file: %s", err)
	}
	t.Cleanup(func() {
		_ = f.Close()
	})
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("error reading length of tar file: %s", err)
	}
	if fi.Size() <= 1024 {
		t.Errorf("tar file should not be empty")
	}

	var paths []string
	r := tar.NewReader(f)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Errorf("error opening tar file: %s", err)
		}
		paths = append(paths, h.Name)

		if name := filepath.Base(h.Name); name != "charm_ed25519" && name != "charm_ed25519.pub" {
			t.Errorf("invalid file name: %q", name)
		}
	}

	if len(paths) != 2 {
		t.Errorf("expected at least 2 files (public and private keys), got %d: %v", len(paths), paths)
	}
}

func TestBackupToStdout(t *testing.T) {
	_ = testserver.SetupTestServer(t)
	var b bytes.Buffer

	BackupKeysCmd.SetArgs([]string{"-o", "-"})
	BackupKeysCmd.SetOut(&b)
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatalf("command failed: %s", err)
	}

	if _, err := ssh.ParsePrivateKey(b.Bytes()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateDirectory(t *testing.T) {
	t.Run("error when path is a file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %s", err)
		}

		err := validateDirectory(filePath)
		if err == nil {
			t.Fatal("expected error when path is a file, got nil")
		}
		expectedMsg := fmt.Sprintf("%v is not a directory, but it should be", filePath)
		if err.Error() != expectedMsg {
			t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
		}
	})

	t.Run("error when less than 2 key files", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create only one key file
		keyPath := filepath.Join(tmpDir, "charm_ed25519")
		if err := os.WriteFile(keyPath, []byte("privatekey"), 0o600); err != nil {
			t.Fatalf("failed to create key file: %s", err)
		}

		err := validateDirectory(tmpDir)
		if err == nil {
			t.Fatal("expected error when less than 2 key files, got nil")
		}
		// Check that error contains expected parts (avoiding curly apostrophe issues)
		errMsg := err.Error()
		if !strings.Contains(errMsg, "find any keys to backup in") || !strings.Contains(errMsg, tmpDir) {
			t.Errorf("unexpected error message: %s", errMsg)
		}
	})

	t.Run("success with ed25519 keys", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create ed25519 key pair
		privKeyPath := filepath.Join(tmpDir, "charm_ed25519")
		pubKeyPath := filepath.Join(tmpDir, "charm_ed25519.pub")

		if err := os.WriteFile(privKeyPath, []byte("privatekey"), 0o600); err != nil {
			t.Fatalf("failed to create private key: %s", err)
		}
		if err := os.WriteFile(pubKeyPath, []byte("publickey"), 0o644); err != nil {
			t.Fatalf("failed to create public key: %s", err)
		}

		err := validateDirectory(tmpDir)
		if err != nil {
			t.Fatalf("expected no error with ed25519 keys, got %s", err)
		}
	})

	t.Run("success with RSA keys", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create RSA key pair
		privKeyPath := filepath.Join(tmpDir, "charm_rsa")
		pubKeyPath := filepath.Join(tmpDir, "charm_rsa.pub")

		if err := os.WriteFile(privKeyPath, []byte("privatekey"), 0o600); err != nil {
			t.Fatalf("failed to create private key: %s", err)
		}
		if err := os.WriteFile(pubKeyPath, []byte("publickey"), 0o644); err != nil {
			t.Fatalf("failed to create public key: %s", err)
		}

		err := validateDirectory(tmpDir)
		if err != nil {
			t.Fatalf("expected no error with RSA keys, got %s", err)
		}
	})

	t.Run("error when directory does not exist", func(t *testing.T) {
		nonExistentPath := "/tmp/this-directory-should-not-exist-12345"
		err := validateDirectory(nonExistentPath)
		if err == nil {
			t.Fatal("expected error when directory does not exist, got nil")
		}
		expectedMsg := fmt.Sprintf("'%v' does not exist", nonExistentPath)
		if err.Error() != expectedMsg {
			t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
		}
	})
}

func TestCreateTar(t *testing.T) {
	t.Run("error when os.Create fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create source directory with keys
		sourceDir := filepath.Join(tmpDir, "source")
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			t.Fatalf("failed to create source dir: %s", err)
		}

		privKeyPath := filepath.Join(sourceDir, "charm_ed25519")
		pubKeyPath := filepath.Join(sourceDir, "charm_ed25519.pub")
		if err := os.WriteFile(privKeyPath, []byte("privatekey"), 0o600); err != nil {
			t.Fatalf("failed to create private key: %s", err)
		}
		if err := os.WriteFile(pubKeyPath, []byte("publickey"), 0o644); err != nil {
			t.Fatalf("failed to create public key: %s", err)
		}

		// Try to create tar in a non-existent directory without parent creation
		invalidTarget := "/tmp/non-existent-dir-12345/subdir/backup.tar"
		err := createTar(sourceDir, invalidTarget)
		if err == nil {
			t.Fatal("expected error when target directory does not exist, got nil")
		}
	})

	t.Run("tar contains only matching key files", func(t *testing.T) {
		tmpDir := t.TempDir()
		sourceDir := filepath.Join(tmpDir, "source")
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			t.Fatalf("failed to create source dir: %s", err)
		}

		// Create key files
		privKeyPath := filepath.Join(sourceDir, "charm_ed25519")
		pubKeyPath := filepath.Join(sourceDir, "charm_ed25519.pub")
		if err := os.WriteFile(privKeyPath, []byte("privatekey"), 0o600); err != nil {
			t.Fatalf("failed to create private key: %s", err)
		}
		if err := os.WriteFile(pubKeyPath, []byte("publickey"), 0o644); err != nil {
			t.Fatalf("failed to create public key: %s", err)
		}

		// Create a non-key file that should not be included
		nonKeyPath := filepath.Join(sourceDir, "some_other_file.txt")
		if err := os.WriteFile(nonKeyPath, []byte("other content"), 0o644); err != nil {
			t.Fatalf("failed to create non-key file: %s", err)
		}

		// Create tar
		targetTar := filepath.Join(tmpDir, "backup.tar")
		if err := createTar(sourceDir, targetTar); err != nil {
			t.Fatalf("failed to create tar: %s", err)
		}

		// Verify tar contents
		f, err := os.Open(targetTar)
		if err != nil {
			t.Fatalf("failed to open tar: %s", err)
		}
		defer f.Close()

		var foundFiles []string
		r := tar.NewReader(f)
		for {
			h, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("error reading tar: %s", err)
			}
			foundFiles = append(foundFiles, filepath.Base(h.Name))
		}

		if len(foundFiles) != 2 {
			t.Errorf("expected 2 files in tar, got %d: %v", len(foundFiles), foundFiles)
		}

		hasPrivKey := false
		hasPubKey := false
		for _, name := range foundFiles {
			if name == "charm_ed25519" {
				hasPrivKey = true
			} else if name == "charm_ed25519.pub" {
				hasPubKey = true
			} else if name == "some_other_file.txt" {
				t.Errorf("tar should not contain non-key file: %s", name)
			}
		}

		if !hasPrivKey {
			t.Error("tar missing private key file")
		}
		if !hasPubKey {
			t.Error("tar missing public key file")
		}
	})
}

func TestGetKeyPaths(t *testing.T) {
	t.Run("empty slice for empty dir", func(t *testing.T) {
		tmpDir := t.TempDir()
		exp := regexp.MustCompilePOSIX("charm_(rsa|ed25519)(.pub)?$")

		paths, err := getKeyPaths(tmpDir, exp)
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}
		if len(paths) != 0 {
			t.Errorf("expected empty slice, got %d paths: %v", len(paths), paths)
		}
	})

	t.Run("error for non-existent source", func(t *testing.T) {
		nonExistentPath := "/tmp/this-directory-should-not-exist-54321"
		exp := regexp.MustCompilePOSIX("charm_(rsa|ed25519)(.pub)?$")

		_, err := getKeyPaths(nonExistentPath, exp)
		if err == nil {
			t.Fatal("expected error for non-existent directory, got nil")
		}
	})

	t.Run("returns matching key files", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create matching files
		privKeyPath := filepath.Join(tmpDir, "charm_ed25519")
		pubKeyPath := filepath.Join(tmpDir, "charm_ed25519.pub")
		rsaPrivKeyPath := filepath.Join(tmpDir, "charm_rsa")
		rsaPubKeyPath := filepath.Join(tmpDir, "charm_rsa.pub")

		if err := os.WriteFile(privKeyPath, []byte("key"), 0o600); err != nil {
			t.Fatalf("failed to create file: %s", err)
		}
		if err := os.WriteFile(pubKeyPath, []byte("key"), 0o644); err != nil {
			t.Fatalf("failed to create file: %s", err)
		}
		if err := os.WriteFile(rsaPrivKeyPath, []byte("key"), 0o600); err != nil {
			t.Fatalf("failed to create file: %s", err)
		}
		if err := os.WriteFile(rsaPubKeyPath, []byte("key"), 0o644); err != nil {
			t.Fatalf("failed to create file: %s", err)
		}

		// Create non-matching file
		nonKeyPath := filepath.Join(tmpDir, "other_file.txt")
		if err := os.WriteFile(nonKeyPath, []byte("other"), 0o644); err != nil {
			t.Fatalf("failed to create file: %s", err)
		}

		exp := regexp.MustCompilePOSIX("charm_(rsa|ed25519)(.pub)?$")
		paths, err := getKeyPaths(tmpDir, exp)
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}

		if len(paths) != 4 {
			t.Errorf("expected 4 matching paths, got %d: %v", len(paths), paths)
		}

		// Verify all expected paths are present
		expectedPaths := map[string]bool{
			privKeyPath:    false,
			pubKeyPath:     false,
			rsaPrivKeyPath: false,
			rsaPubKeyPath:  false,
		}
		for _, p := range paths {
			if _, ok := expectedPaths[p]; ok {
				expectedPaths[p] = true
			}
		}
		for path, found := range expectedPaths {
			if !found {
				t.Errorf("expected path %s not found in results", path)
			}
		}
	})
}

func TestFileOrDirectoryExists(t *testing.T) {
	t.Run("false for non-existent", func(t *testing.T) {
		nonExistentPath := "/tmp/this-should-not-exist-99999"
		if fileOrDirectoryExists(nonExistentPath) {
			t.Error("expected false for non-existent path, got true")
		}
	})

	t.Run("true for file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "testfile")
		if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %s", err)
		}

		if !fileOrDirectoryExists(filePath) {
			t.Error("expected true for existing file, got false")
		}
	})

	t.Run("true for directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		subDir := filepath.Join(tmpDir, "subdir")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("failed to create directory: %s", err)
		}

		if !fileOrDirectoryExists(subDir) {
			t.Error("expected true for existing directory, got false")
		}
	})
}

func TestCreateTarNonExistentSource(t *testing.T) {
	// REGRESSION TEST: createTar should return error for non-existent source
	// Current bug: line 140-142 in backup_keys.go returns nil instead of err
	// when os.Stat(source) fails, silently succeeding instead of erroring
	tmpDir := t.TempDir()
	nonExistentSource := filepath.Join(tmpDir, "does-not-exist")
	targetTar := filepath.Join(tmpDir, "output.tar")

	err := createTar(nonExistentSource, targetTar)
	if err == nil {
		t.Fatal("expected error when source directory does not exist, got nil")
	}

	// Verify the error message indicates the source doesn't exist
	if !strings.Contains(err.Error(), "no such file or directory") &&
		!strings.Contains(err.Error(), "does not exist") &&
		!strings.Contains(err.Error(), "cannot find") {
		t.Errorf("expected error about missing source, got: %s", err.Error())
	}
}

func TestBackupKeysAutoAddsTarSuffix(t *testing.T) {
	// Test that backup-keys -o /tmp/foo creates /tmp/foo.tar (suffix auto-added)
	_ = testserver.SetupTestServer(t)

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "my-backup")
	expectedPath := outputPath + ".tar"

	// Clean up any existing files
	_ = os.RemoveAll(outputPath)
	_ = os.RemoveAll(expectedPath)

	BackupKeysCmd.SetArgs([]string{"-o", outputPath})
	if err := BackupKeysCmd.Execute(); err != nil {
		t.Fatalf("command failed: %s", err)
	}

	// Verify the file was created with .tar suffix
	if !fileOrDirectoryExists(expectedPath) {
		t.Errorf("expected file to be created at %s, but it does not exist", expectedPath)
	}

	// Verify the file without suffix was NOT created
	if fileOrDirectoryExists(outputPath) {
		t.Errorf("file should not exist at %s (without .tar suffix)", outputPath)
	}

	// Verify it's a valid tar file
	f, err := os.Open(expectedPath)
	if err != nil {
		t.Fatalf("failed to open tar file: %s", err)
	}
	defer f.Close()

	r := tar.NewReader(f)
	fileCount := 0
	for {
		_, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("error reading tar: %s", err)
		}
		fileCount++
	}

	if fileCount < 2 {
		t.Errorf("expected at least 2 files in tar (key pair), got %d", fileCount)
	}
}
