// ABOUTME: Tests for concurrent SQLite operations
// ABOUTME: Ensures backup is concurrent-safe with concurrent writers

package kv

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestSQLiteConcurrentBackup verifies that backup is safe with concurrent writes.
// This test intentionally creates concurrent write pressure during backup to ensure
// the backup API produces a consistent snapshot.
func TestSQLiteConcurrentBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and populate database
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	// Insert initial data
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i * 2)}
		if err := sqliteSet(db, key, value); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	// Start concurrent writers
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Launch 3 concurrent writers that continuously update keys
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					key := []byte{byte(workerID)}
					value := []byte{byte(workerID * 3)}
					_ = sqliteSet(db, key, value)
					time.Sleep(time.Millisecond)
				}
			}
		}(w)
	}

	// Give writers time to start
	time.Sleep(10 * time.Millisecond)

	// Perform backup while writers are active
	var buf bytes.Buffer
	if err := sqliteBackup(dbPath, &buf); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("Backup failed: %v", err)
	}

	// Stop writers
	close(stop)
	wg.Wait()
	db.Close()

	// Verify backup is valid and can be restored
	restorePath := filepath.Join(dir, "restored.db")
	if err := sqliteRestore(&buf, restorePath); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify restored database is valid and readable
	restored, err := openSQLite(restorePath)
	if err != nil {
		t.Fatalf("failed to open restored db: %v", err)
	}
	defer restored.Close()

	// Read some keys to verify database integrity
	keys, err := sqliteKeys(restored)
	if err != nil {
		t.Fatalf("Keys failed on restored db: %v", err)
	}

	if len(keys) == 0 {
		t.Error("Restored database has no keys, expected data")
	}

	// Verify we can read values
	for _, key := range keys {
		_, err := sqliteGet(restored, key)
		if err != nil {
			t.Errorf("Get failed on restored db for key %v: %v", key, err)
		}
	}
}

// TestSQLiteConcurrentBackupStress performs intensive concurrent backup testing.
// This test creates heavy write load and multiple simultaneous backups to
// stress test the concurrent safety of the backup implementation.
func TestSQLiteConcurrentBackupStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stress.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	// Insert initial dataset
	for i := 0; i < 500; i++ {
		key := []byte{byte(i >> 8), byte(i & 0xff)}
		value := []byte{byte(i), byte(i >> 8)}
		if err := sqliteSet(db, key, value); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	backupErrors := make(chan error, 10)

	// Start 5 concurrent writers with high frequency
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-stop:
					return
				default:
					key := []byte{byte(workerID), byte(counter)}
					value := []byte{byte(counter >> 8), byte(counter & 0xff)}
					_ = sqliteSet(db, key, value)
					counter++
					// No sleep - maximum write pressure
				}
			}
		}(w)
	}

	// Perform 10 concurrent backups while writers are running
	for b := 0; b < 10; b++ {
		wg.Add(1)
		go func(backupID int) {
			defer wg.Done()
			time.Sleep(time.Duration(backupID) * time.Millisecond)
			var buf bytes.Buffer
			if err := sqliteBackup(dbPath, &buf); err != nil {
				backupErrors <- err
				return
			}

			// Verify backup is valid
			restorePath := filepath.Join(dir, fmt.Sprintf("stress_restore_%d.db", backupID))
			if err := sqliteRestore(&buf, restorePath); err != nil {
				backupErrors <- err
				return
			}

			restored, err := openSQLite(restorePath)
			if err != nil {
				backupErrors <- err
				return
			}
			defer restored.Close()

			// Quick sanity check
			keys, err := sqliteKeys(restored)
			if err != nil {
				backupErrors <- err
				return
			}
			if len(keys) == 0 {
				backupErrors <- fmt.Errorf("backup %d has no keys", backupID)
			}
		}(b)
	}

	// Let stress test run for a bit
	time.Sleep(50 * time.Millisecond)

	// Stop all workers
	close(stop)
	wg.Wait()
	db.Close()
	close(backupErrors)

	// Check for any backup errors
	for err := range backupErrors {
		t.Errorf("Backup error: %v", err)
	}
}

// TestSQLiteBackupDuringTransaction verifies backup works even when
// a transaction is active in another connection.
func TestSQLiteBackupDuringTransaction(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create database and insert data
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i * 2)}
		if err := sqliteSet(db, key, value); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}
	db.Close()

	// Open a second connection and start a transaction
	db2, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open second connection: %v", err)
	}
	defer db2.Close()

	tx, err := db2.Begin()
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// Insert data in transaction (not yet committed)
	if _, err := tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", []byte{99}, []byte{199}); err != nil {
		t.Fatalf("Insert in transaction failed: %v", err)
	}

	// Perform backup while transaction is active
	var buf bytes.Buffer
	if err := sqliteBackup(dbPath, &buf); err != nil {
		t.Fatalf("Backup during transaction failed: %v", err)
	}

	// Rollback transaction
	tx.Rollback()

	// Verify backup doesn't include uncommitted data
	restorePath := filepath.Join(dir, "restored.db")
	if err := sqliteRestore(&buf, restorePath); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := openSQLite(restorePath)
	if err != nil {
		t.Fatalf("failed to open restored db: %v", err)
	}
	defer restored.Close()

	// Verify key 99 doesn't exist (it was rolled back)
	_, err = sqliteGet(restored, []byte{99})
	if err != ErrMissingKey {
		t.Errorf("Expected ErrMissingKey for uncommitted data, got %v", err)
	}
}
