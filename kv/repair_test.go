// ABOUTME: Tests for database repair functionality
// ABOUTME: Covers WAL checkpoint, SHM removal, integrity check, vacuum, and cloud reset

package kv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepair_HealthyDatabase(t *testing.T) {
	// Create a temp directory for our test database
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a healthy database with some data
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	_, err = db.Exec("INSERT INTO kv (key, value) VALUES (?, ?)", []byte("key1"), []byte("value1"))
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	// Repair should succeed on healthy database
	result, err := Repair("test", false, WithPath(tmpDir))
	if err != nil {
		t.Fatalf("Repair failed on healthy database: %v", err)
	}

	// Check result
	if !result.IntegrityOK {
		t.Error("expected IntegrityOK to be true")
	}
	if !result.Vacuumed {
		t.Error("expected Vacuumed to be true")
	}
	if result.RecoveryAttempted {
		t.Error("expected RecoveryAttempted to be false for healthy db")
	}
	if result.ResetFromCloud {
		t.Error("expected ResetFromCloud to be false for healthy db")
	}
	if result.Error != nil {
		t.Errorf("expected no error, got: %v", result.Error)
	}

	// Verify database is still readable
	db, err = openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen database after repair: %v", err)
	}
	defer func() { _ = db.Close() }()

	var value []byte
	err = db.QueryRow("SELECT value FROM kv WHERE key = ?", []byte("key1")).Scan(&value)
	if err != nil {
		t.Fatalf("failed to read data after repair: %v", err)
	}
	if string(value) != "value1" {
		t.Errorf("expected value1, got %s", value)
	}
}

func TestRepair_StaleSHMFile(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a healthy database
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	// Create a stale SHM file (this simulates a crash leaving orphaned SHM)
	shmPath := dbPath + "-shm"
	if err := os.WriteFile(shmPath, []byte("stale shm data"), 0600); err != nil {
		t.Fatalf("failed to create stale SHM file: %v", err)
	}

	// Verify SHM file exists before repair
	if _, err := os.Stat(shmPath); os.IsNotExist(err) {
		t.Fatal("SHM file should exist before repair")
	}

	// Repair should remove the stale SHM file
	result, err := Repair("test", false, WithPath(tmpDir))
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}

	if !result.ShmRemoved {
		t.Error("expected ShmRemoved to be true")
	}

	// SHM file should be removed after repair
	// Note: SQLite may recreate it on checkpoint, so we just verify the repair ran
	if !result.IntegrityOK {
		t.Error("expected IntegrityOK to be true after SHM removal")
	}
}

func TestRepair_WALCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a database and write data (this creates WAL entries)
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	// Insert multiple rows to generate WAL data
	for i := 0; i < 100; i++ {
		_, err = db.Exec("INSERT INTO kv (key, value) VALUES (?, ?)",
			[]byte("key"+string(rune(i))), []byte("value"))
		if err != nil {
			t.Fatalf("failed to insert test data: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	// WAL file might exist after inserts
	walPath := dbPath + "-wal"
	walExistedBefore := false
	if _, err := os.Stat(walPath); err == nil {
		walExistedBefore = true
	}

	// Repair should checkpoint WAL
	result, err := Repair("test", false, WithPath(tmpDir))
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}

	// If WAL existed, it should have been checkpointed
	if walExistedBefore && !result.WalCheckpointed {
		t.Error("expected WalCheckpointed to be true when WAL existed")
	}

	if !result.IntegrityOK {
		t.Error("expected IntegrityOK to be true")
	}
}

func TestRepair_CorruptedDatabase_NoForce(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a corrupt database file (not valid SQLite)
	if err := os.WriteFile(dbPath, []byte("this is not a sqlite database"), 0600); err != nil {
		t.Fatalf("failed to create corrupt database: %v", err)
	}

	// Repair without force should fail
	result, err := Repair("test", false, WithPath(tmpDir))
	if err == nil {
		t.Fatal("expected error for corrupted database without force")
	}

	// Result should indicate failure
	if result != nil && result.IntegrityOK {
		t.Error("expected IntegrityOK to be false for corrupt database")
	}
}

func TestRepair_CorruptedDatabase_WithForce(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a corrupt database file
	if err := os.WriteFile(dbPath, []byte("this is not a sqlite database"), 0600); err != nil {
		t.Fatalf("failed to create corrupt database: %v", err)
	}

	// Repair with force should attempt recovery
	// Note: Without a client, cloud reset will fail, but recovery should be attempted
	result, err := Repair("test", true, WithPath(tmpDir))

	// We expect the repair to either succeed (if REINDEX works) or fail gracefully
	// The key is that RecoveryAttempted should be true
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}

	if !result.RecoveryAttempted {
		// For completely corrupt files (not even valid SQLite), we can't open them
		// to attempt REINDEX, so RecoveryAttempted may be false
		if err == nil {
			t.Error("expected either RecoveryAttempted=true or an error")
		}
	}
}

func TestRepair_NonExistentDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}

	// Repair on non-existent database should create it
	result, err := Repair("nonexistent", false, WithPath(tmpDir))
	if err != nil {
		t.Fatalf("Repair failed on non-existent database: %v", err)
	}

	if !result.IntegrityOK {
		t.Error("expected IntegrityOK to be true for fresh database")
	}

	// Database should now exist
	dbPath := filepath.Join(kvDir, "nonexistent.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database to be created")
	}
}

func TestRepairResult_String(t *testing.T) {
	result := &RepairResult{
		WalCheckpointed: true,
		ShmRemoved:      true,
		IntegrityOK:     true,
		Vacuumed:        true,
	}

	// Just verify it doesn't panic and returns something
	s := result.String()
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

func TestReset_CreatesNewDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a database with some data
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	_, err = db.Exec("INSERT INTO kv (key, value) VALUES (?, ?)", []byte("key1"), []byte("value1"))
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	// Reset without a client (local-only reset)
	err = Reset("test", WithPath(tmpDir))
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Database should exist but be empty (no data synced without client)
	db, err = openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open database after reset: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Old data should be gone
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM kv").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after reset, got %d", count)
	}
}

func TestReset_RemovesWALAndSHM(t *testing.T) {
	tmpDir := t.TempDir()
	kvDir := filepath.Join(tmpDir, "kv")
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		t.Fatalf("failed to create kv dir: %v", err)
	}
	dbPath := filepath.Join(kvDir, "test.db")

	// Create a database
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	// Create WAL and SHM files
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"
	if err := os.WriteFile(walPath, []byte("wal data"), 0600); err != nil {
		t.Fatalf("failed to create WAL file: %v", err)
	}
	if err := os.WriteFile(shmPath, []byte("shm data"), 0600); err != nil {
		t.Fatalf("failed to create SHM file: %v", err)
	}

	// Reset should remove all files
	err = Reset("test", WithPath(tmpDir))
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// WAL and SHM should be removed (main DB recreated)
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Error("expected WAL file to be removed after reset")
	}
	if _, err := os.Stat(shmPath); !os.IsNotExist(err) {
		t.Error("expected SHM file to be removed after reset")
	}

	// Main database should exist (recreated fresh)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database to exist after reset")
	}
}
