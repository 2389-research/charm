// ABOUTME: PocketBase implementation of the storage.FileStore interface.
// ABOUTME: Stores encrypted files as PocketBase file attachments.

package pocketbase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	charmfs "github.com/charmbracelet/charm/fs"
	charm "github.com/charmbracelet/charm/proto"
	pb "github.com/charmbracelet/charm/server/pocketbase"
)

// FileStore implements storage.FileStore using PocketBase.
type FileStore struct {
	app *pb.App
}

// New creates a new PocketBase-backed FileStore.
func New(app *pb.App) *FileStore {
	return &FileStore{app: app}
}

func (s *FileStore) pb() core.App {
	return s.app.PB()
}

// Stat returns the FileInfo for the given Charm ID and path.
func (s *FileStore) Stat(charmID, path string) (fs.FileInfo, error) {
	record, err := s.findFileRecord(charmID, path)
	if err != nil {
		return nil, fs.ErrNotExist
	}

	info := s.recordToFileInfo(record)

	// For directories, calculate total size of contents
	if info.IsDir() {
		size, err := s.calculateDirSize(charmID, path)
		if err == nil {
			info.FileInfo.Size = size
		}
	}

	return info, nil
}

// Get returns an fs.File for the given Charm ID and path.
func (s *FileStore) Get(charmID string, path string) (fs.File, error) {
	record, err := s.findFileRecord(charmID, path)
	if err != nil {
		return nil, fs.ErrNotExist
	}

	isDir := record.GetBool("is_dir")

	if isDir {
		// Return directory listing as JSON
		return s.getDirListing(charmID, path, record)
	}

	// Return file contents
	return s.getFile(record)
}

// Put stores the data with the Charm ID and path.
func (s *FileStore) Put(charmID string, path string, r io.Reader, mode fs.FileMode) error {
	if cpath := filepath.Clean(path); cpath == string(os.PathSeparator) {
		return fmt.Errorf("invalid path specified: %s", cpath)
	}

	app := s.pb()
	collection, err := app.FindCollectionByNameOrId(pb.CollectionCharmFiles)
	if err != nil {
		return err
	}

	// Check if record already exists
	existing, _ := s.findFileRecord(charmID, path)

	var record *core.Record
	if existing != nil {
		record = existing
	} else {
		record = core.NewRecord(collection)
		record.Set("charm_id", charmID)
		record.Set("path", path)
	}

	record.Set("is_dir", mode.IsDir())
	record.Set("mode", int(mode))

	if !mode.IsDir() {
		// Read file content and create file attachment
		content, err := io.ReadAll(r)
		if err != nil {
			return err
		}

		filename := filepath.Base(path)
		file, err := filesystem.NewFileFromBytes(content, filename)
		if err != nil {
			return err
		}

		record.Set("file", file)
	}

	// Ensure parent directories exist
	if err := s.ensureParentDirs(charmID, path, mode); err != nil {
		return err
	}

	return app.Save(record)
}

// Delete deletes the file at the given path.
func (s *FileStore) Delete(charmID string, path string) error {
	app := s.pb()

	// Delete the record and all children (for directories)
	return app.RunInTransaction(func(txApp core.App) error {
		// Delete exact match
		record, err := s.findFileRecordTx(txApp, charmID, path)
		if err == nil {
			if err := txApp.Delete(record); err != nil {
				return err
			}
		}

		// Delete children if this was a directory
		children, err := txApp.FindRecordsByFilter(
			pb.CollectionCharmFiles,
			fmt.Sprintf("charm_id = '%s' && path ~ '%s/'", charmID, path),
			"",
			0,
			0,
		)
		if err != nil {
			return nil // No children, that's fine
		}

		for _, child := range children {
			if err := txApp.Delete(child); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *FileStore) findFileRecord(charmID, path string) (*core.Record, error) {
	return s.pb().FindFirstRecordByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path = '%s'", charmID, path),
	)
}

func (s *FileStore) findFileRecordTx(txApp core.App, charmID, path string) (*core.Record, error) {
	return txApp.FindFirstRecordByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path = '%s'", charmID, path),
	)
}

func (s *FileStore) recordToFileInfo(r *core.Record) *charmfs.FileInfo {
	isDir := r.GetBool("is_dir")
	mode := fs.FileMode(r.GetInt("mode"))
	if isDir {
		mode = mode | fs.ModeDir
	}

	var size int64
	if !isDir {
		// Get file size from the file field
		files := r.GetStringSlice("file")
		if len(files) > 0 {
			// PocketBase stores file metadata - we'd need filesystem access for actual size
			// For now, use 0 and let Stat calculate if needed
			size = 0
		}
	}

	return &charmfs.FileInfo{
		FileInfo: charm.FileInfo{
			Name:    filepath.Base(r.GetString("path")),
			IsDir:   isDir,
			Size:    size,
			ModTime: r.GetDateTime("updated").Time(),
			Mode:    mode,
		},
	}
}

func (s *FileStore) calculateDirSize(charmID, path string) (int64, error) {
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	records, err := s.pb().FindRecordsByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path ~ '%s' && is_dir = false", charmID, prefix),
		"",
		0,
		0,
	)
	if err != nil {
		return 0, err
	}

	var total int64
	for range records {
		// TODO: Get actual file sizes from PocketBase filesystem
		// For now, this is a placeholder
		total += 0
	}

	return total, nil
}

func (s *FileStore) getDirListing(charmID, path string, dirRecord *core.Record) (fs.File, error) {
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Find immediate children only
	records, err := s.pb().FindRecordsByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path ~ '%s'", charmID, prefix),
		"",
		0,
		0,
	)
	if err != nil {
		records = []*core.Record{}
	}

	// Filter to immediate children only
	fis := make([]charm.FileInfo, 0)
	seen := make(map[string]bool)

	for _, r := range records {
		childPath := r.GetString("path")
		// Get relative path from prefix
		rel := strings.TrimPrefix(childPath, prefix)
		if rel == "" {
			continue
		}

		// Get first component (immediate child name)
		parts := strings.SplitN(rel, "/", 2)
		childName := parts[0]

		if seen[childName] {
			continue
		}
		seen[childName] = true

		info := s.recordToFileInfo(r)
		fis = append(fis, info.FileInfo)
	}

	dirInfo := s.recordToFileInfo(dirRecord)
	dir := charm.FileInfo{
		Name:    dirInfo.Name(),
		IsDir:   true,
		Size:    0,
		ModTime: dirInfo.ModTime(),
		Mode:    dirInfo.Mode(),
		Files:   fis,
	}

	buf := bytes.NewBuffer(nil)
	if err := json.NewEncoder(buf).Encode(dir); err != nil {
		return nil, err
	}

	return &charmfs.DirFile{
		Buffer:   buf,
		FileInfo: dirInfo,
	}, nil
}

func (s *FileStore) getFile(record *core.Record) (fs.File, error) {
	app := s.pb()

	files := record.GetStringSlice("file")
	if len(files) == 0 {
		return nil, fs.ErrNotExist
	}

	filename := files[0]
	fsys, err := app.NewFilesystem()
	if err != nil {
		return nil, err
	}
	defer fsys.Close()

	collection, err := app.FindCollectionByNameOrId(pb.CollectionCharmFiles)
	if err != nil {
		return nil, err
	}

	key := record.BaseFilesPath() + "/" + filename
	_ = collection // Used for path construction

	// Read file from PocketBase storage
	reader, err := fsys.GetFile(key)
	if err != nil {
		return nil, err
	}

	content, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		return nil, err
	}

	info := s.recordToFileInfo(record)

	return &pbFile{
		Reader:   bytes.NewReader(content),
		info:     info,
		filename: filename,
	}, nil
}

func (s *FileStore) ensureParentDirs(charmID, path string, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		return nil
	}

	// Check if parent exists
	_, err := s.findFileRecord(charmID, dir)
	if err == nil {
		return nil // Parent exists
	}

	// Create parent directory
	app := s.pb()
	collection, err := app.FindCollectionByNameOrId(pb.CollectionCharmFiles)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("charm_id", charmID)
	record.Set("path", dir)
	record.Set("is_dir", true)
	record.Set("mode", int(mode|fs.ModeDir|0100)) // Add execute for dirs

	if err := app.Save(record); err != nil {
		return err
	}

	// Recursively ensure grandparent
	return s.ensureParentDirs(charmID, dir, mode)
}

// pbFile implements fs.File for PocketBase files.
type pbFile struct {
	*bytes.Reader
	info     fs.FileInfo
	filename string
}

func (f *pbFile) Stat() (fs.FileInfo, error) {
	return f.info, nil
}

func (f *pbFile) Close() error {
	return nil
}

// Name returns the filename (required for some fs.File consumers).
func (f *pbFile) Name() string {
	return f.filename
}
