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

// openSQLite opens or creates a SQLite database with the KV schema.
// Uses WAL mode for better concurrency (multiple readers, one writer).
//
//nolint:unused // Used in sqlite_test.go and will be used in kv.go integration
func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	// Set busy timeout first to handle concurrent access gracefully.
	// This makes SQLite wait up to 5 seconds for locks instead of failing immediately.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	// Check current journal mode - only enable WAL if not already set.
	// This avoids lock contention when multiple processes open the same database,
	// since PRAGMA journal_mode=WAL requires an exclusive lock to switch modes.
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		_ = db.Close()
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

// sqliteRestore restores a database from the reader.
//
//nolint:unused // Will be used in kv.go integration
func sqliteRestore(r io.Reader, dstPath string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read backup data: %w", err)
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
