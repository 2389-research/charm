// ABOUTME: Windows-specific file locking stub for SQLite recovery
// ABOUTME: Windows uses different locking mechanism, but SQLite handles it

//go:build windows

package kv

import (
	"os"
)

// recoveryLockFile is a no-op on Windows.
// SQLite on Windows handles its own locking via LockFileEx.
// The concurrent recovery race condition is less likely on Windows
// since file deletion is blocked while handles are open.
func recoveryLockFile(dbPath string) (*os.File, func(), error) {
	// Return a no-op cleanup function
	return nil, func() {}, nil
}
