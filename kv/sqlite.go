// ABOUTME: SQLite storage layer for KV store
// ABOUTME: Provides encrypted key-value storage with WAL mode for concurrency

package kv

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// isCorruptDatabaseError checks if an error indicates the database file is corrupt
// or not a valid SQLite database. This can happen when old BadgerDB backup data
// gets synced to the database path.
func isCorruptDatabaseError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// SQLite error code 26 is SQLITE_NOTADB
	return strings.Contains(errStr, "not a database") ||
		strings.Contains(errStr, "(26)") ||
		strings.Contains(errStr, "file is encrypted or is not a database")
}

// recoverCorruptDatabase removes a corrupt database file and its WAL/SHM files.
// This is called when we detect a corrupt database (e.g., from old BadgerDB backups)
// to allow creating a fresh database on retry.
func recoverCorruptDatabase(path string) error {
	// Remove the main database file
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove corrupt database: %w", err)
	}
	// Remove WAL file if it exists
	_ = os.Remove(path + "-wal")
	// Remove SHM file if it exists
	_ = os.Remove(path + "-shm")
	return nil
}

// openSQLite opens or creates a SQLite database with the KV schema.
// Uses WAL mode for better concurrency (multiple readers, one writer).
// If the database file is corrupt (e.g., from old BadgerDB backups), it will
// delete the corrupt file and create a fresh database.
//
//nolint:unused // Used in sqlite_test.go and will be used in kv.go integration
func openSQLite(path string) (*sql.DB, error) {
	return openSQLiteWithRecovery(path, true)
}

// openSQLiteWithRecovery opens a SQLite database with optional corruption recovery.
// If allowRecovery is true and the file is corrupt, it deletes the file and retries.
// Uses a file lock to serialize concurrent recovery attempts across goroutines/processes.
func openSQLiteWithRecovery(path string, allowRecovery bool) (*sql.DB, error) {
	// Acquire lock to serialize recovery attempts across processes.
	// This prevents SIGBUS when one process removes WAL files while another is using them.
	_, cleanup, lockErr := recoveryLockFile(path)
	if lockErr != nil {
		// If we can't get the lock, proceed without it (best effort)
		cleanup = func() {}
	}
	defer cleanup()

	return openSQLiteCore(path, allowRecovery)
}

// openSQLiteCore does the actual database open work (called with lock held).
func openSQLiteCore(path string, allowRecovery bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	// Set busy timeout first to handle concurrent access gracefully.
	// This makes SQLite wait up to 5 seconds for locks instead of failing immediately.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		// Check for corruption and recover if allowed
		if allowRecovery && isCorruptDatabaseError(err) {
			if recoverErr := recoverCorruptDatabase(path); recoverErr == nil {
				return openSQLiteCore(path, false) // Don't allow nested recovery
			}
		}
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	// Set synchronous mode for durability.
	// NORMAL provides good balance between durability and performance.
	// In WAL mode, NORMAL guarantees no corruption and only risks losing
	// the last transaction on power failure (acceptable for our use case).
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		_ = db.Close()
		// Check for corruption and recover if allowed
		if allowRecovery && isCorruptDatabaseError(err) {
			if recoverErr := recoverCorruptDatabase(path); recoverErr == nil {
				return openSQLiteCore(path, false) // Don't allow nested recovery
			}
		}
		return nil, fmt.Errorf("failed to set synchronous mode: %w", err)
	}

	// Check current journal mode - only enable WAL if not already set.
	// This avoids lock contention when multiple processes open the same database,
	// since PRAGMA journal_mode=WAL requires an exclusive lock to switch modes.
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		_ = db.Close()
		// Check for corruption and recover if allowed
		if allowRecovery && isCorruptDatabaseError(err) {
			if recoverErr := recoverCorruptDatabase(path); recoverErr == nil {
				return openSQLiteCore(path, false) // Don't allow nested recovery
			}
		}
		return nil, fmt.Errorf("failed to query journal mode: %w", err)
	}
	if journalMode != "wal" {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			// If WAL enable failed, check if another connection already set it.
			// This handles the race where multiple connections open simultaneously.
			var currentMode string
			if queryErr := db.QueryRow("PRAGMA journal_mode").Scan(&currentMode); queryErr == nil && currentMode == "wal" {
				// Another connection set WAL mode, we're good
			} else {
				_ = db.Close()
				return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
			}
		}
	}

	// Create schema
	schema := `
		CREATE TABLE IF NOT EXISTS kv (
			key   BLOB PRIMARY KEY,
			value BLOB NOT NULL
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS meta (
			name  TEXT PRIMARY KEY,
			value INTEGER NOT NULL
		) WITHOUT ROWID;

		-- Sync lock table: prevents concurrent Sync() calls from racing.
		-- Uses a singleton row (id=1) with expiring lease.
		CREATE TABLE IF NOT EXISTS sync_lock (
			id          INTEGER PRIMARY KEY CHECK (id = 1),
			holder      TEXT NOT NULL,
			acquired_at INTEGER NOT NULL,
			expires_at  INTEGER NOT NULL
		);

		-- Pending ops table: tracks writes that haven't been synced to cloud.
		-- This makes pending writes durable across process restarts.
		CREATE TABLE IF NOT EXISTS pending_ops (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			op_type    TEXT NOT NULL CHECK (op_type IN ('set', 'delete')),
			key        BLOB NOT NULL,
			value      BLOB,
			created_at INTEGER NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return db, nil
}

// sqliteGet retrieves a value by key. Returns ErrMissingKey if not found.
//
//nolint:unused // Will be used in kv.go integration
func sqliteGet(db *sql.DB, key []byte) ([]byte, error) {
	var value []byte
	err := db.QueryRow("SELECT value FROM kv WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, ErrMissingKey
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	return value, nil
}

// sqliteSet stores a key-value pair, overwriting if exists.
//
//nolint:unused // Will be used in kv.go integration
func sqliteSet(db *sql.DB, key, value []byte) error {
	_, err := db.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, value)
	if err != nil {
		return fmt.Errorf("failed to set key: %w", err)
	}
	return nil
}

// sqliteDelete removes a key. No error if key doesn't exist.
//
//nolint:unused // Will be used in kv.go integration
func sqliteDelete(db *sql.DB, key []byte) error {
	_, err := db.Exec("DELETE FROM kv WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}
	return nil
}

// sqliteSetWithPendingOp stores a key-value pair and records a pending op atomically.
// This ensures the pending op is tracked durably with the write.
func sqliteSetWithPendingOp(db *sql.DB, key, value []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Store the key-value pair
	_, err = tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, value)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to set key: %w", err)
	}

	// Record the pending op
	if err := recordPendingOp(tx, "set", key, value); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// sqliteDeleteWithPendingOp removes a key and records a pending op atomically.
// This ensures the pending op is tracked durably with the delete.
func sqliteDeleteWithPendingOp(db *sql.DB, key []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Delete the key
	_, err = tx.Exec("DELETE FROM kv WHERE key = ?", key)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to delete key: %w", err)
	}

	// Record the pending op
	if err := recordPendingOp(tx, "delete", key, nil); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// sqliteKeys returns all keys in the database.
// Returns an empty slice (not nil) if no keys exist.
//
//nolint:unused // Will be used in kv.go integration
func sqliteKeys(db *sql.DB) ([][]byte, error) {
	rows, err := db.Query("SELECT key FROM kv")
	if err != nil {
		return nil, fmt.Errorf("failed to query keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([][]byte, 0)
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("failed to scan key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating keys: %w", err)
	}
	return keys, nil
}

// sqliteGetMeta retrieves a metadata value. Returns 0 if not found.
//
//nolint:unused // Will be used in kv.go integration
func sqliteGetMeta(db *sql.DB, name string) (int64, error) {
	var value int64
	err := db.QueryRow("SELECT value FROM meta WHERE name = ?", name).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get meta %s: %w", name, err)
	}
	return value, nil
}

// sqliteSetMeta stores a metadata value.
//
//nolint:unused // Will be used in kv.go integration
func sqliteSetMeta(db *sql.DB, name string, value int64) error {
	_, err := db.Exec("INSERT OR REPLACE INTO meta (name, value) VALUES (?, ?)", name, value)
	if err != nil {
		return fmt.Errorf("failed to set meta %s: %w", name, err)
	}
	return nil
}

// sqliteBackup creates a backup of the database to the writer.
// Uses VACUUM INTO to create a consistent snapshot that is safe even with
// concurrent writers. VACUUM INTO creates a transactionally consistent copy
// in a single atomic operation.
//
//nolint:unused // Will be used in kv.go integration
func sqliteBackup(srcPath string, w io.Writer) error {
	// Open source for backup in read-only mode
	src, err := sql.Open("sqlite", srcPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("failed to open source for backup: %w", err)
	}
	defer func() { _ = src.Close() }()

	// Set busy timeout to handle concurrent access gracefully.
	if _, err := src.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("failed to set busy timeout for backup: %w", err)
	}

	// Create temporary file for VACUUM INTO output
	tmpDir := filepath.Dir(srcPath)
	tmpFile, err := os.CreateTemp(tmpDir, "backup-*.db")
	if err != nil {
		return fmt.Errorf("failed to create temp backup file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	// Use VACUUM INTO to create a consistent snapshot.
	// This is safe with concurrent writes because VACUUM INTO takes a read lock
	// and creates a transactionally consistent point-in-time snapshot.
	//
	// SQLite's VACUUM INTO doesn't support parameter binding, so we must
	// validate the path to prevent SQL injection. The path comes from
	// os.CreateTemp which generates safe filenames, but we validate anyway.
	if err := validateSQLitePath(tmpPath); err != nil {
		return fmt.Errorf("unsafe backup path: %w", err)
	}
	// Use double quotes per SQLite standard for identifiers/paths
	query := fmt.Sprintf(`VACUUM INTO "%s"`, escapeSQLiteString(tmpPath))
	_, err = src.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to vacuum into temp file: %w", err)
	}

	// Read the consistent backup file
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	return nil
}

// SQLite magic bytes: "SQLite format 3\x00"
var sqliteMagic = []byte("SQLite format 3\x00")

// ErrNotSQLite indicates the data is not a valid SQLite database.
// This typically happens when trying to restore old BadgerDB backups
// after migrating to SQLite storage.
var ErrNotSQLite = fmt.Errorf("data is not a valid SQLite database (possibly a pre-migration BadgerDB backup)")

// sqliteRestore restores a database from the reader.
// Returns ErrNotSQLite if the data is not a valid SQLite database.
//
//nolint:unused // Will be used in kv.go integration
func sqliteRestore(r io.Reader, dstPath string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read backup data: %w", err)
	}

	// Validate SQLite magic bytes before writing.
	// This prevents restoring old BadgerDB backups that would corrupt the database.
	if len(data) < len(sqliteMagic) || string(data[:len(sqliteMagic)]) != string(sqliteMagic) {
		return ErrNotSQLite
	}

	if err := os.WriteFile(dstPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write database file: %w", err)
	}

	return nil
}

// validateSQLitePath ensures the path doesn't contain SQL injection attempts.
// Checks for dangerous characters that could break out of quoted strings.
//
//nolint:unused // Used by sqliteBackup which is marked unused pending kv.go integration
func validateSQLitePath(path string) error {
	// Check for null bytes (path traversal/injection)
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte")
	}
	// Check for newlines (SQL injection)
	if strings.ContainsAny(path, "\n\r") {
		return fmt.Errorf("path contains newline characters")
	}
	// Check for quote characters that could break out of quoting
	if strings.Contains(path, `"`) {
		return fmt.Errorf("path contains double quotes")
	}
	return nil
}

// escapeSQLiteString escapes a string for use in SQLite.
// This is defense-in-depth since validateSQLitePath already blocks quotes.
//
//nolint:unused // Used by sqliteBackup which is marked unused pending kv.go integration
func escapeSQLiteString(s string) string {
	// In SQLite, double quotes in identifiers are escaped by doubling them
	return strings.ReplaceAll(s, `"`, `""`)
}
