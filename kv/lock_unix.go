// ABOUTME: Unix-specific file locking for SQLite recovery
// ABOUTME: Uses flock to serialize concurrent recovery attempts

//go:build !windows

package kv

import (
	"fmt"
	"os"
	"syscall"
)

// recoveryLockFile creates and locks a file to serialize recovery operations
// across concurrent goroutines/processes. Returns the lock file and cleanup func.
func recoveryLockFile(dbPath string) (*os.File, func(), error) {
	lockPath := dbPath + ".recovery.lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("failed to acquire lock: %w", err)
	}
	cleanup := func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(lockPath)
	}
	return f, cleanup, nil
}
