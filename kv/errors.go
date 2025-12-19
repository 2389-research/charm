// ABOUTME: Error types and helpers for KV database operations.
// ABOUTME: Includes lock detection and read-only mode error handling.

package kv

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDatabaseLocked is returned when the database cannot be opened because
// another process holds the lock.
type ErrDatabaseLocked struct {
	Path string
	Err  error
}

func (e *ErrDatabaseLocked) Error() string {
	return fmt.Sprintf("database is locked by another process at %q: %v\n\n"+
		"Another process is using this database. Options:\n"+
		"  1. Stop the other process (e.g., MCP server)\n"+
		"  2. Use OpenReadOnly() for read-only access\n"+
		"  3. Wait and retry", e.Path, e.Err)
}

func (e *ErrDatabaseLocked) Unwrap() error {
	return e.Err
}

// ErrReadOnlyMode is returned when a write operation is attempted on a
// read-only database.
type ErrReadOnlyMode struct {
	Operation string
}

func (e *ErrReadOnlyMode) Error() string {
	return fmt.Sprintf("cannot %s: database is open in read-only mode\n\n"+
		"The database was opened read-only because another process holds the lock.\n"+
		"To perform writes, stop the other process and reopen the database.", e.Operation)
}

// IsLocked returns true if the error indicates the database is locked by
// another process.
func IsLocked(err error) bool {
	if err == nil {
		return false
	}

	// Check for our wrapped error type
	var lockErr *ErrDatabaseLocked
	if errors.As(err, &lockErr) {
		return true
	}

	// Check for common lock error messages from BadgerDB
	errStr := err.Error()
	lockIndicators := []string{
		"Cannot acquire directory lock",
		"resource temporarily unavailable",
		"LOCK",
		"another process",
	}
	for _, indicator := range lockIndicators {
		if strings.Contains(errStr, indicator) {
			return true
		}
	}

	return false
}

// IsReadOnly returns true if the error indicates a write was attempted on a
// read-only database.
func IsReadOnly(err error) bool {
	var roErr *ErrReadOnlyMode
	return errors.As(err, &roErr)
}
