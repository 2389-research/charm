package localstorage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	charmfs "github.com/charmbracelet/charm/fs"
	charm "github.com/charmbracelet/charm/proto"
	"github.com/charmbracelet/charm/server/storage"
)

// LocalFileStore is a FileStore implementation that stores files locally in a
// folder.
type LocalFileStore struct {
	Path string
}

// NewLocalFileStore creates a FileStore locally in the provided path. Files
// will be encrypted client-side and stored as regular file system files and
// folders.
func NewLocalFileStore(path string) (*LocalFileStore, error) {
	err := storage.EnsureDir(path, 0o700)
	if err != nil {
		return nil, err
	}
	return &LocalFileStore{path}, nil
}

// validatePath validates that the user-provided path does not attempt to
// escape the charmID directory using path traversal sequences like "../".
// It returns the sanitized full path and an error if validation fails.
func (lfs *LocalFileStore) validatePath(charmID, path string) (string, error) {
	// Clean the path to resolve . and .. elements
	cleaned := filepath.Clean(path)

	// Check if cleaned path is root - this is invalid
	if cleaned == string(os.PathSeparator) || cleaned == "." {
		return "", fmt.Errorf("invalid path specified: %s", path)
	}

	// Build the full path: basePath/charmID/userPath
	fullPath := filepath.Join(lfs.Path, charmID, cleaned)

	// Ensure the resolved path is still within the intended base directory
	baseDir := filepath.Join(lfs.Path, charmID)

	// Get absolute paths for comparison to handle any remaining relative components
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Check that the full path is within the base directory
	// Add separator to prevent partial matches (e.g., /base/charmID vs /base/charmID2)
	if !strings.HasPrefix(absFull, absBase+string(os.PathSeparator)) && absFull != absBase {
		return "", fmt.Errorf("path traversal detected: %s", path)
	}

	return fullPath, nil
}

// Stat returns the FileInfo for the given Charm ID and path.
func (lfs *LocalFileStore) Stat(charmID, path string) (fs.FileInfo, error) {
	fp, err := lfs.validatePath(charmID, path)
	if err != nil {
		return nil, err
	}
	i, err := os.Stat(fp)
	if os.IsNotExist(err) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	in := &charmfs.FileInfo{
		FileInfo: charm.FileInfo{
			Name:    i.Name(),
			IsDir:   i.IsDir(),
			Size:    i.Size(),
			ModTime: i.ModTime(),
			Mode:    i.Mode(),
		},
	}
	// Get the actual size of the files in a directory
	if i.IsDir() {
		in.FileInfo.Size = 0
		if err = filepath.Walk(fp, func(path string, info fs.FileInfo, err error) error {
			if info.IsDir() {
				return nil
			}
			in.FileInfo.Size += info.Size()
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// Get returns an fs.File for the given Charm ID and path.
func (lfs *LocalFileStore) Get(charmID string, path string) (fs.File, error) {
	fp, err := lfs.validatePath(charmID, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(fp)
	if os.IsNotExist(err) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(fp)
	if err != nil {
		return nil, err
	}
	// write a directory listing if path is a dir
	if info.IsDir() {
		rds, err := f.ReadDir(0)
		if err != nil {
			return nil, err
		}
		fis := make([]charm.FileInfo, 0)
		for _, v := range rds {
			fi, err := v.Info()
			if err != nil {
				return nil, err
			}
			fin := charm.FileInfo{
				Name:    v.Name(),
				IsDir:   fi.IsDir(),
				Size:    fi.Size(),
				ModTime: fi.ModTime(),
				Mode:    fi.Mode(),
			}
			fis = append(fis, fin)
		}
		dir := charm.FileInfo{
			Name:    info.Name(),
			IsDir:   true,
			Size:    0,
			ModTime: info.ModTime(),
			Mode:    info.Mode(),
			Files:   fis,
		}
		buf := bytes.NewBuffer(nil)
		enc := json.NewEncoder(buf)
		err = enc.Encode(dir)
		if err != nil {
			return nil, err
		}
		return &charmfs.DirFile{
			Buffer:   buf,
			FileInfo: info,
		}, nil
	}
	return f, nil
}

// Put reads from the provided io.Reader and stores the data with the Charm ID
// and path.
func (lfs *LocalFileStore) Put(charmID string, path string, r io.Reader, mode fs.FileMode) error {
	fp, err := lfs.validatePath(charmID, path)
	if err != nil {
		return err
	}
	if mode.IsDir() {
		return storage.EnsureDir(fp, mode)
	}
	err = storage.EnsureDir(filepath.Dir(fp), mode)
	if err != nil {
		return err
	}
	f, err := os.Create(fp)
	if err != nil {
		return err
	}
	defer f.Close() // nolint:errcheck
	_, err = io.Copy(f, r)
	if err != nil {
		return err
	}
	if mode != 0 {
		return f.Chmod(mode)
	}
	return nil
}

// Delete deletes the file at the given path for the provided Charm ID.
func (lfs *LocalFileStore) Delete(charmID string, path string) error {
	fp, err := lfs.validatePath(charmID, path)
	if err != nil {
		return err
	}
	return os.RemoveAll(fp)
}
