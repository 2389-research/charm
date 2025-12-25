// ABOUTME: Sync lock implementation for preventing concurrent Sync() races
// ABOUTME: Uses SQLite table with expiring lease for cross-process coordination

package kv

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	// syncLockTimeout is how long a sync lock is held before expiring.
	// This prevents deadlocks if a process crashes while holding the lock.
	// 30 seconds is enough for slow networks while not blocking too long on crash.
	syncLockTimeout = 30 * time.Second
)

// ErrSyncLockHeld is returned when another process holds the sync lock.
var ErrSyncLockHeld = errors.New("sync lock held by another process")

// syncLockHolder generates a unique identifier for this lock acquisition.
// Each call returns a new UUID to uniquely identify the lock holder.
//
// IMPORTANT: The caller must capture the returned holder ID and pass it to
// releaseSyncLock. Do not call syncLockHolder() again to get the holder ID
// for release - that will generate a new UUID and fail to release the lock.
// The withSyncLock function handles this correctly.
func syncLockHolder() string {
	return uuid.New().String()
}

// acquireSyncLock attempts to acquire the sync lock.
// Returns the holder ID if successful, or ErrSyncLockHeld if another process holds it.
// The lock expires after syncLockTimeout to prevent deadlocks.
func acquireSyncLock(db *sql.DB) (string, error) {
	holder := syncLockHolder()
	now := time.Now().Unix()
	expiresAt := now + int64(syncLockTimeout.Seconds())

	// Try to insert new lock, or take over expired lock
	result, err := db.Exec(`
		INSERT INTO sync_lock (id, holder, acquired_at, expires_at)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			holder = excluded.holder,
			acquired_at = excluded.acquired_at,
			expires_at = excluded.expires_at
		WHERE sync_lock.expires_at < ?
	`, holder, now, expiresAt, now)
	if err != nil {
		return "", fmt.Errorf("failed to acquire sync lock: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("failed to check sync lock result: %w", err)
	}

	if rows == 0 {
		// Lock is held by someone else and hasn't expired
		return "", ErrSyncLockHeld
	}

	return holder, nil
}

// releaseSyncLock releases the sync lock if we hold it.
// Only releases if the holder matches (prevents releasing someone else's lock).
func releaseSyncLock(db *sql.DB, holder string) error {
	_, err := db.Exec(`DELETE FROM sync_lock WHERE id = 1 AND holder = ?`, holder)
	if err != nil {
		return fmt.Errorf("failed to release sync lock: %w", err)
	}
	return nil
}

// refreshSyncLock extends the lock expiry if we still hold it.
// Returns false if we no longer hold the lock (it expired and was taken).
//
//nolint:unused // Reserved for long-running sync operations
func refreshSyncLock(db *sql.DB, holder string) (bool, error) {
	now := time.Now().Unix()
	expiresAt := now + int64(syncLockTimeout.Seconds())

	result, err := db.Exec(`
		UPDATE sync_lock
		SET expires_at = ?
		WHERE id = 1 AND holder = ?
	`, expiresAt, holder)
	if err != nil {
		return false, fmt.Errorf("failed to refresh sync lock: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check refresh result: %w", err)
	}

	return rows > 0, nil
}

// withSyncLock executes fn while holding the sync lock.
// If the lock cannot be acquired, returns ErrSyncLockHeld.
func withSyncLock(db *sql.DB, fn func() error) error {
	holder, err := acquireSyncLock(db)
	if err != nil {
		return err
	}
	defer func() {
		_ = releaseSyncLock(db, holder)
	}()

	return fn()
}
