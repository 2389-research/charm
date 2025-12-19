// ABOUTME: SQLite storage layer for KV store
// ABOUTME: Provides encrypted key-value storage with WAL mode for concurrency

package kv

import (
	"database/sql"
	"fmt"

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

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
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
