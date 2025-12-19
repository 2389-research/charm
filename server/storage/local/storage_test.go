package localstorage

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	charm "github.com/charmbracelet/charm/proto"
	"github.com/google/uuid"
)

func TestPut(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	buf := bytes.NewBufferString("")
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{filepath.Join(string(os.PathSeparator), ""), filepath.Join(string(os.PathSeparator), "//")}
	for _, path := range paths {
		err = lfs.Put(charmID, path, buf, fs.FileMode(0o644))
		if err == nil {
			t.Fatalf("expected error when file path is %s", path)
		}

	}

	content := "hello world"
	path := filepath.Join(string(os.PathSeparator), "hello.txt")
	t.Run(path, func(t *testing.T) {
		buf = bytes.NewBufferString(content)
		err = lfs.Put(charmID, path, buf, fs.FileMode(0o644))
		if err != nil {
			t.Fatalf("expected no error when file path is %s, %v", path, err)
		}

		file, err := os.Open(filepath.Join(tdir, charmID, path))
		if err != nil {
			t.Fatalf("expected no error when opening file %s", path)
		}
		defer file.Close() //nolint:errcheck

		fileInfo, err := file.Stat()
		if err != nil {
			t.Fatalf("expected no error when getting file info for %s", path)
		}

		if fileInfo.IsDir() {
			t.Fatalf("expected file %s to be a regular file", path)
		}

		read, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("expected no error when reading file %s", path)
		}
		if string(read) != content {
			t.Fatalf("expected content to be %s, got %s", content, string(read))
		}
	})

	content = "bar"
	path = filepath.Join(string(os.PathSeparator), "foo", "hello.txt")
	t.Run(path, func(t *testing.T) {
		buf = bytes.NewBufferString(content)
		err = lfs.Put(charmID, path, buf, fs.FileMode(0o644))
		if err != nil {
			t.Fatalf("expected no error when file path is %s, %v", path, err)
		}

		file, err := os.Open(filepath.Join(tdir, charmID, path))
		if err != nil {
			t.Fatalf("expected no error when opening file %s", path)
		}
		defer file.Close() //nolint:errcheck

		fileInfo, err := file.Stat()
		if err != nil {
			t.Fatalf("expected no error when getting file info for %s", path)
		}

		if fileInfo.IsDir() {
			t.Fatalf("expected file %s to be a regular file", path)
		}

		read, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("expected no error when reading file %s", path)
		}
		if string(read) != content {
			t.Fatalf("expected content to be %s, got %s", content, string(read))
		}
	})
}

func TestGetMissingFile(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(string(os.PathSeparator), "nonexistent.txt")
	_, err = lfs.Get(charmID, path)
	if err != fs.ErrNotExist {
		t.Fatalf("expected fs.ErrNotExist for missing file, got %v", err)
	}
}

func TestGetRegularFile(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	content := "test file content"
	path := filepath.Join(string(os.PathSeparator), "test.txt")
	buf := bytes.NewBufferString(content)
	err = lfs.Put(charmID, path, buf, fs.FileMode(0o644))
	if err != nil {
		t.Fatalf("failed to put file: %v", err)
	}

	file, err := lfs.Get(charmID, path)
	if err != nil {
		t.Fatalf("expected no error when getting file, got %v", err)
	}
	defer file.Close() //nolint:errcheck

	read, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(read) != content {
		t.Fatalf("expected content %s, got %s", content, string(read))
	}
}

func TestGetDirectory(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a directory with some files
	dirPath := filepath.Join(string(os.PathSeparator), "testdir")
	file1Path := filepath.Join(dirPath, "file1.txt")
	file2Path := filepath.Join(dirPath, "file2.txt")

	err = lfs.Put(charmID, file1Path, bytes.NewBufferString("content1"), fs.FileMode(0o644))
	if err != nil {
		t.Fatalf("failed to put file1: %v", err)
	}

	err = lfs.Put(charmID, file2Path, bytes.NewBufferString("content2"), fs.FileMode(0o644))
	if err != nil {
		t.Fatalf("failed to put file2: %v", err)
	}

	// Get directory listing
	file, err := lfs.Get(charmID, dirPath)
	if err != nil {
		t.Fatalf("expected no error when getting directory, got %v", err)
	}
	defer file.Close() //nolint:errcheck

	// Read and decode JSON listing
	read, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("failed to read directory listing: %v", err)
	}

	var dirInfo charm.FileInfo
	err = json.Unmarshal(read, &dirInfo)
	if err != nil {
		t.Fatalf("failed to unmarshal directory listing: %v", err)
	}

	if !dirInfo.IsDir {
		t.Fatal("expected directory FileInfo to have IsDir=true")
	}

	if len(dirInfo.Files) != 2 {
		t.Fatalf("expected 2 files in directory, got %d", len(dirInfo.Files))
	}

	// Check that both files are in the listing
	foundFile1 := false
	foundFile2 := false
	for _, fi := range dirInfo.Files {
		if fi.Name == "file1.txt" {
			foundFile1 = true
		}
		if fi.Name == "file2.txt" {
			foundFile2 = true
		}
	}

	if !foundFile1 || !foundFile2 {
		t.Fatal("expected both files to be in directory listing")
	}
}

func TestStatMissingFile(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(string(os.PathSeparator), "nonexistent.txt")
	_, err = lfs.Stat(charmID, path)
	if err != fs.ErrNotExist {
		t.Fatalf("expected fs.ErrNotExist for missing file, got %v", err)
	}
}

func TestStatDirectory(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a directory with some files
	dirPath := filepath.Join(string(os.PathSeparator), "testdir")
	file1Path := filepath.Join(dirPath, "file1.txt")
	file2Path := filepath.Join(dirPath, "file2.txt")

	content1 := "hello"
	content2 := "world!"

	err = lfs.Put(charmID, file1Path, bytes.NewBufferString(content1), fs.FileMode(0o644))
	if err != nil {
		t.Fatalf("failed to put file1: %v", err)
	}

	err = lfs.Put(charmID, file2Path, bytes.NewBufferString(content2), fs.FileMode(0o644))
	if err != nil {
		t.Fatalf("failed to put file2: %v", err)
	}

	// Stat the directory
	info, err := lfs.Stat(charmID, dirPath)
	if err != nil {
		t.Fatalf("expected no error when statting directory, got %v", err)
	}

	if !info.IsDir() {
		t.Fatal("expected directory to be reported as directory")
	}

	// Check that the size is the sum of file sizes
	expectedSize := int64(len(content1) + len(content2))
	if info.Size() != expectedSize {
		t.Fatalf("expected directory size %d, got %d", expectedSize, info.Size())
	}
}

func TestDelete(t *testing.T) {
	tdir := t.TempDir()
	charmID := uuid.New().String()
	lfs, err := NewLocalFileStore(tdir)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("delete single file", func(t *testing.T) {
		path := filepath.Join(string(os.PathSeparator), "delete-me.txt")
		err = lfs.Put(charmID, path, bytes.NewBufferString("content"), fs.FileMode(0o644))
		if err != nil {
			t.Fatalf("failed to put file: %v", err)
		}

		// Verify file exists
		_, err = lfs.Get(charmID, path)
		if err != nil {
			t.Fatalf("file should exist before delete: %v", err)
		}

		// Delete the file
		err = lfs.Delete(charmID, path)
		if err != nil {
			t.Fatalf("failed to delete file: %v", err)
		}

		// Verify file no longer exists
		_, err = lfs.Get(charmID, path)
		if err != fs.ErrNotExist {
			t.Fatalf("expected fs.ErrNotExist after delete, got %v", err)
		}
	})

	t.Run("delete directory recursively", func(t *testing.T) {
		dirPath := filepath.Join(string(os.PathSeparator), "delete-dir")
		file1Path := filepath.Join(dirPath, "file1.txt")
		file2Path := filepath.Join(dirPath, "subdir", "file2.txt")

		err = lfs.Put(charmID, file1Path, bytes.NewBufferString("content1"), fs.FileMode(0o644))
		if err != nil {
			t.Fatalf("failed to put file1: %v", err)
		}

		err = lfs.Put(charmID, file2Path, bytes.NewBufferString("content2"), fs.FileMode(0o644))
		if err != nil {
			t.Fatalf("failed to put file2: %v", err)
		}

		// Verify directory exists
		_, err = lfs.Stat(charmID, dirPath)
		if err != nil {
			t.Fatalf("directory should exist before delete: %v", err)
		}

		// Delete the directory
		err = lfs.Delete(charmID, dirPath)
		if err != nil {
			t.Fatalf("failed to delete directory: %v", err)
		}

		// Verify directory no longer exists
		_, err = lfs.Stat(charmID, dirPath)
		if err != fs.ErrNotExist {
			t.Fatalf("expected fs.ErrNotExist after delete, got %v", err)
		}

		// Verify nested files also don't exist
		_, err = lfs.Get(charmID, file1Path)
		if err != fs.ErrNotExist {
			t.Fatalf("expected fs.ErrNotExist for nested file after delete, got %v", err)
		}

		_, err = lfs.Get(charmID, file2Path)
		if err != fs.ErrNotExist {
			t.Fatalf("expected fs.ErrNotExist for deeply nested file after delete, got %v", err)
		}
	})
}
