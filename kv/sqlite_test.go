// ABOUTME: Tests for SQLite storage layer
// ABOUTME: Covers schema creation, basic operations, and backup/restore

package kv

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestSQLiteOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Verify schema exists by querying meta table
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM meta").Scan(&count)
	if err != nil {
		t.Fatalf("meta table should exist: %v", err)
	}
}

func TestSQLiteCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	key := []byte("testkey")
	value := []byte("testvalue")

	// Test Set
	if err := sqliteSet(db, key, value); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Test Get
	got, err := sqliteGet(db, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(got) != string(value) {
		t.Errorf("Get returned %q, want %q", got, value)
	}

	// Test overwrite
	newValue := []byte("newvalue")
	if err := sqliteSet(db, key, newValue); err != nil {
		t.Fatalf("Set (overwrite) failed: %v", err)
	}
	got, err = sqliteGet(db, key)
	if err != nil {
		t.Fatalf("Get after overwrite failed: %v", err)
	}
	if string(got) != string(newValue) {
		t.Errorf("Get returned %q, want %q", got, newValue)
	}

	// Test Delete
	if err := sqliteDelete(db, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = sqliteGet(db, key)
	if err != ErrMissingKey {
		t.Errorf("Get after delete should return ErrMissingKey, got %v", err)
	}
}

func TestSQLiteKeys(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Insert some keys
	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	for _, k := range keys {
		if err := sqliteSet(db, k, []byte("value")); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	// Get all keys
	got, err := sqliteKeys(db)
	if err != nil {
		t.Fatalf("Keys failed: %v", err)
	}

	if len(got) != len(keys) {
		t.Errorf("Keys returned %d keys, want %d", len(got), len(keys))
	}

	// Verify actual key contents
	keyMap := make(map[string]bool)
	for _, k := range got {
		keyMap[string(k)] = true
	}

	for _, expected := range keys {
		if !keyMap[string(expected)] {
			t.Errorf("Keys missing expected key %q", expected)
		}
	}
}

func TestSQLiteKeysEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Get keys from empty database
	got, err := sqliteKeys(db)
	if err != nil {
		t.Fatalf("Keys failed on empty database: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("Keys returned %d keys from empty database, want 0", len(got))
	}

	if got == nil {
		t.Error("Keys returned nil instead of empty slice")
	}
}

func TestSQLiteMeta(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Test get on missing key returns 0
	val, err := sqliteGetMeta(db, "max_version")
	if err != nil {
		t.Fatalf("GetMeta failed: %v", err)
	}
	if val != 0 {
		t.Errorf("GetMeta on missing key returned %d, want 0", val)
	}

	// Test set
	if err := sqliteSetMeta(db, "max_version", 42); err != nil {
		t.Fatalf("SetMeta failed: %v", err)
	}

	// Test get returns set value
	val, err = sqliteGetMeta(db, "max_version")
	if err != nil {
		t.Fatalf("GetMeta failed: %v", err)
	}
	if val != 42 {
		t.Errorf("GetMeta returned %d, want 42", val)
	}
}

func TestSQLiteBackupRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and populate source database
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	testData := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}
	for k, v := range testData {
		if err := sqliteSet(db, []byte(k), []byte(v)); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}
	if err := sqliteSetMeta(db, "max_version", 100); err != nil {
		t.Fatalf("SetMeta failed: %v", err)
	}
	db.Close()

	// Backup to buffer
	var buf bytes.Buffer
	if err := sqliteBackup(dbPath, &buf); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Restore to new database
	restorePath := filepath.Join(dir, "restored.db")
	if err := sqliteRestore(&buf, restorePath); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify restored data
	restored, err := openSQLite(restorePath)
	if err != nil {
		t.Fatalf("failed to open restored db: %v", err)
	}
	defer restored.Close()

	for k, want := range testData {
		got, err := sqliteGet(restored, []byte(k))
		if err != nil {
			t.Errorf("Get %s failed: %v", k, err)
			continue
		}
		if string(got) != want {
			t.Errorf("Get %s = %q, want %q", k, got, want)
		}
	}

	ver, _ := sqliteGetMeta(restored, "max_version")
	if ver != 100 {
		t.Errorf("max_version = %d, want 100", ver)
	}
}

func TestValidateSQLitePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid path",
			path:    "/tmp/backup-123.db",
			wantErr: false,
		},
		{
			name:    "path with spaces",
			path:    "/tmp/my backup.db",
			wantErr: false,
		},
		{
			name:    "null byte injection",
			path:    "/tmp/test\x00.db",
			wantErr: true,
		},
		{
			name:    "newline injection",
			path:    "/tmp/test\n.db",
			wantErr: true,
		},
		{
			name:    "carriage return injection",
			path:    "/tmp/test\r.db",
			wantErr: true,
		},
		{
			name:    "double quote injection",
			path:    `/tmp/test".db`,
			wantErr: true,
		},
		{
			name:    "sql injection attempt",
			path:    `/tmp/x"; DROP TABLE kv; --`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLitePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSQLitePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEscapeSQLiteString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no quotes",
			input: "/tmp/backup.db",
			want:  "/tmp/backup.db",
		},
		{
			name:  "single double quote",
			input: `test"file`,
			want:  `test""file`,
		},
		{
			name:  "multiple double quotes",
			input: `test"file"name`,
			want:  `test""file""name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeSQLiteString(tt.input)
			if got != tt.want {
				t.Errorf("escapeSQLiteString() = %q, want %q", got, tt.want)
			}
		})
	}
}
