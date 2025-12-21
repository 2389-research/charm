// ABOUTME: Tests for SQLite storage layer
// ABOUTME: Covers schema creation, basic operations, backup/restore, and WAL mode

package kv

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
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

func TestSQLiteRestoreValidation(t *testing.T) {
	dir := t.TempDir()

	t.Run("rejects non-SQLite data", func(t *testing.T) {
		// Simulate old BadgerDB backup data
		badgerData := bytes.NewReader([]byte(`item:uuid-here{"data":"value"}`))
		dstPath := filepath.Join(dir, "reject.db")

		err := sqliteRestore(badgerData, dstPath)
		if err != ErrNotSQLite {
			t.Errorf("expected ErrNotSQLite, got %v", err)
		}

		// File should not have been created
		if _, statErr := os.Stat(dstPath); !os.IsNotExist(statErr) {
			t.Error("file should not exist when validation fails")
		}
	})

	t.Run("accepts valid SQLite data", func(t *testing.T) {
		// Create a valid SQLite database
		srcPath := filepath.Join(dir, "source.db")
		db, err := openSQLite(srcPath)
		if err != nil {
			t.Fatalf("failed to create source db: %v", err)
		}
		if err := sqliteSet(db, []byte("key"), []byte("value")); err != nil {
			t.Fatalf("failed to write test data: %v", err)
		}
		db.Close()

		// Read it back and restore to new location
		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("failed to read source: %v", err)
		}

		dstPath := filepath.Join(dir, "restored.db")
		if err := sqliteRestore(bytes.NewReader(srcData), dstPath); err != nil {
			t.Fatalf("restore failed: %v", err)
		}

		// Verify restored database works
		restored, err := openSQLite(dstPath)
		if err != nil {
			t.Fatalf("failed to open restored: %v", err)
		}
		defer restored.Close()

		val, err := sqliteGet(restored, []byte("key"))
		if err != nil {
			t.Fatalf("failed to read from restored: %v", err)
		}
		if string(val) != "value" {
			t.Errorf("got %q, want %q", val, "value")
		}
	})

	t.Run("rejects too-short data", func(t *testing.T) {
		shortData := bytes.NewReader([]byte("tiny"))
		dstPath := filepath.Join(dir, "short.db")

		err := sqliteRestore(shortData, dstPath)
		if err != ErrNotSQLite {
			t.Errorf("expected ErrNotSQLite for short data, got %v", err)
		}
	})
}

func TestSQLiteWALModeEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Verify WAL mode is enabled
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}
}

func TestSQLiteBusyTimeoutSet(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Verify busy timeout is set to 5000ms
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d, want %d", timeout, 5000)
	}
}

func TestSQLiteWALModePreservedOnReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open and close to create the database with WAL mode
	db1, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite first time: %v", err)
	}
	if err := sqliteSet(db1, []byte("key"), []byte("value")); err != nil {
		t.Fatalf("failed to set key: %v", err)
	}
	db1.Close()

	// Reopen the database - should not fail trying to re-enable WAL
	db2, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen sqlite: %v", err)
	}
	defer db2.Close()

	// Verify WAL mode is still enabled
	var journalMode string
	if err := db2.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode after reopen = %q, want %q", journalMode, "wal")
	}

	// Verify data persisted
	got, err := sqliteGet(db2, []byte("key"))
	if err != nil {
		t.Fatalf("failed to get key after reopen: %v", err)
	}
	if string(got) != "value" {
		t.Errorf("value after reopen = %q, want %q", got, "value")
	}
}

func TestSQLiteConcurrentConnections(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open first connection
	db1, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite first connection: %v", err)
	}
	defer db1.Close()

	// Open second connection to same database - should not fail with lock error
	db2, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite second connection: %v", err)
	}
	defer db2.Close()

	// Both connections should be able to read
	if _, err := sqliteKeys(db1); err != nil {
		t.Errorf("db1 read failed: %v", err)
	}
	if _, err := sqliteKeys(db2); err != nil {
		t.Errorf("db2 read failed: %v", err)
	}

	// Write from first connection
	if err := sqliteSet(db1, []byte("from_db1"), []byte("value1")); err != nil {
		t.Errorf("db1 write failed: %v", err)
	}

	// Second connection should see the write
	got, err := sqliteGet(db2, []byte("from_db1"))
	if err != nil {
		t.Errorf("db2 read after db1 write failed: %v", err)
	}
	if string(got) != "value1" {
		t.Errorf("db2 got %q, want %q", got, "value1")
	}
}

func TestSQLiteMultipleConnectionsWriting(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Pre-initialize the database with a single connection to avoid
	// race conditions on WAL mode enablement.
	initDB, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to pre-initialize database: %v", err)
	}
	_ = initDB.Close()

	// Simulate multiple processes by opening multiple connections
	const numConnections = 3
	const writesPerConnection = 5

	var wg sync.WaitGroup
	errors := make(chan error, numConnections*writesPerConnection)

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(connID int) {
			defer wg.Done()

			// Each goroutine opens its own connection (like separate processes)
			db, err := openSQLite(dbPath)
			if err != nil {
				errors <- err
				return
			}
			defer db.Close()

			for j := 0; j < writesPerConnection; j++ {
				key := []byte(filepath.Join("conn", string(rune('0'+connID)), "key", string(rune('0'+j))))
				value := []byte("value")
				if err := sqliteSet(db, key, value); err != nil {
					errors <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Collect any errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Errorf("concurrent connection writes produced %d errors, first: %v", len(errs), errs[0])
	}

	// Verify all data was written
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open for verification: %v", err)
	}
	defer db.Close()

	keys, err := sqliteKeys(db)
	if err != nil {
		t.Fatalf("failed to get keys: %v", err)
	}
	if len(keys) != numConnections*writesPerConnection {
		t.Errorf("expected %d keys, got %d", numConnections*writesPerConnection, len(keys))
	}
}
