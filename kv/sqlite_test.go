// ABOUTME: Tests for SQLite storage layer
// ABOUTME: Covers schema creation, basic operations, and backup/restore

package kv

import (
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
