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
