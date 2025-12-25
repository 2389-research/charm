// ABOUTME: Health checking functionality for KV databases
// ABOUTME: Provides Doctor() to diagnose sync issues, corruption, and pending ops

package kv

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// DoctorResult contains the results of a health check on a KV database.
type DoctorResult struct {
	// IntegrityOK is true if SQLite integrity_check passed.
	IntegrityOK bool

	// IntegrityDetails contains the raw output from PRAGMA integrity_check.
	// Will be "ok" if healthy, or error details if corrupt.
	IntegrityDetails string

	// PendingOpsCount is the number of write operations waiting to be synced.
	PendingOpsCount int64

	// OldestPendingOp is the timestamp of the oldest pending operation.
	// Zero if no pending ops.
	OldestPendingOp time.Time

	// LocalSeq is the latest sequence number in the local database.
	LocalSeq uint64

	// WALSize is the size of the WAL file in bytes, or -1 if not present.
	WALSize int64

	// SHMExists indicates if the shared memory file exists.
	SHMExists bool

	// SyncLockHeld indicates if another process currently holds the sync lock.
	SyncLockHeld bool

	// SyncLockHolder is the holder ID if the lock is held.
	SyncLockHolder string

	// SyncLockExpiresAt is when the current lock expires (if held).
	SyncLockExpiresAt time.Time

	// Errors contains any non-fatal errors encountered during the check.
	Errors []string

	// Warnings contains advisory messages that aren't errors.
	Warnings []string
}

// IsHealthy returns true if the database appears healthy.
func (r *DoctorResult) IsHealthy() bool {
	return r.IntegrityOK && len(r.Errors) == 0
}

// String returns a human-readable summary of the health check.
func (r *DoctorResult) String() string {
	var sb strings.Builder

	// Integrity
	if r.IntegrityOK {
		sb.WriteString("✓ SQLite integrity: OK\n")
	} else {
		sb.WriteString(fmt.Sprintf("✗ SQLite integrity: FAILED (%s)\n", r.IntegrityDetails))
	}

	// Pending ops
	if r.PendingOpsCount == 0 {
		sb.WriteString("✓ Pending ops: 0\n")
	} else {
		age := ""
		if !r.OldestPendingOp.IsZero() {
			age = fmt.Sprintf(" (oldest: %s ago)", time.Since(r.OldestPendingOp).Round(time.Second))
		}
		sb.WriteString(fmt.Sprintf("⚠ Pending ops: %d%s\n", r.PendingOpsCount, age))
	}

	// Local sequence
	sb.WriteString(fmt.Sprintf("✓ Local seq: %d\n", r.LocalSeq))

	// WAL status
	if r.WALSize >= 0 {
		sb.WriteString(fmt.Sprintf("✓ WAL file: %d bytes\n", r.WALSize))
	} else {
		sb.WriteString("✓ WAL file: not present\n")
	}

	// Sync lock
	if r.SyncLockHeld {
		expiresIn := time.Until(r.SyncLockExpiresAt).Round(time.Second)
		sb.WriteString(fmt.Sprintf("⚠ Sync lock: held by %s (expires in %s)\n", r.SyncLockHolder, expiresIn))
	} else {
		sb.WriteString("✓ Sync lock: not held\n")
	}

	// Warnings
	for _, w := range r.Warnings {
		sb.WriteString(fmt.Sprintf("⚠ Warning: %s\n", w))
	}

	// Errors
	for _, e := range r.Errors {
		sb.WriteString(fmt.Sprintf("✗ Error: %s\n", e))
	}

	return sb.String()
}

// Doctor performs a health check on the KV database.
// This is safe to call on a read-only database.
func (kv *KV) Doctor() (*DoctorResult, error) {
	result := &DoctorResult{
		WALSize: -1, // Default to "not present"
	}

	// SQLite integrity check
	if err := kv.checkIntegrity(result); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("integrity check failed: %v", err))
	}

	// Pending ops
	if err := kv.checkPendingOps(result); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("pending ops check failed: %v", err))
	}

	// Local sequence
	result.LocalSeq = kv.maxVersion()

	// WAL file status
	kv.checkWALStatus(result)

	// Sync lock status
	kv.checkSyncLock(result)

	return result, nil
}

// checkIntegrity runs SQLite's integrity_check pragma.
func (kv *KV) checkIntegrity(result *DoctorResult) error {
	var integrityResult string
	err := kv.db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil {
		return err
	}

	result.IntegrityDetails = integrityResult
	result.IntegrityOK = integrityResult == "ok"

	return nil
}

// checkPendingOps counts pending operations and finds the oldest.
func (kv *KV) checkPendingOps(result *DoctorResult) error {
	// Count pending ops
	count, err := countPendingOps(kv.db)
	if err != nil {
		return err
	}
	result.PendingOpsCount = count

	// Find oldest pending op if any exist
	if count > 0 {
		var oldestUnix int64
		err := kv.db.QueryRow("SELECT MIN(created_at) FROM pending_ops").Scan(&oldestUnix)
		if err == nil && oldestUnix > 0 {
			result.OldestPendingOp = time.Unix(oldestUnix, 0)
		}

		// Warn if pending ops are old
		if !result.OldestPendingOp.IsZero() {
			age := time.Since(result.OldestPendingOp)
			if age > 24*time.Hour {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("pending ops older than 24h (oldest: %s ago)", age.Round(time.Hour)))
			}
		}
	}

	return nil
}

// checkWALStatus checks for WAL and SHM files.
func (kv *KV) checkWALStatus(result *DoctorResult) {
	walPath := kv.dbPath + "-wal"
	shmPath := kv.dbPath + "-shm"

	// Check WAL file
	if info, err := statFile(walPath); err == nil {
		result.WALSize = info.Size()
	}

	// Check SHM file
	if _, err := statFile(shmPath); err == nil {
		result.SHMExists = true
	}
}

// checkSyncLock checks if another process holds the sync lock.
func (kv *KV) checkSyncLock(result *DoctorResult) {
	var holder string
	var expiresAt int64

	err := kv.db.QueryRow("SELECT holder, expires_at FROM sync_lock WHERE id = 1").Scan(&holder, &expiresAt)
	if err != nil {
		// No lock held (row doesn't exist)
		return
	}

	now := time.Now().Unix()
	if expiresAt > now {
		result.SyncLockHeld = true
		result.SyncLockHolder = holder
		result.SyncLockExpiresAt = time.Unix(expiresAt, 0)
	}
	// If expiresAt <= now, the lock is expired and not really "held"
}

// statFile is a helper that wraps os.Stat for file size checking.
func statFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// DoctorDB performs a health check on a KV database by name.
// This opens the database in read-only mode, runs Doctor(), and closes it.
// Useful for CLI tools that need to check database health without
// holding a long-lived connection.
func DoctorDB(name string, opts ...Option) (*DoctorResult, error) {
	kv, err := OpenWithDefaultsReadOnly(name, opts...)
	if err != nil {
		// If we can't even open, return a result with the error
		return &DoctorResult{
			Errors: []string{fmt.Sprintf("failed to open database: %v", err)},
		}, nil
	}
	defer func() { _ = kv.Close() }()

	return kv.Doctor()
}
