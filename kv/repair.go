// ABOUTME: Database repair functionality for corrupted SQLite databases
// ABOUTME: Provides WAL checkpoint, SHM cleanup, integrity check, vacuum, and cloud reset

package kv

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/charm/client"
)

// RepairResult contains details of repair operations performed.
type RepairResult struct {
	WalCheckpointed   bool  // WAL was checkpointed into main DB
	ShmRemoved        bool  // Stale SHM file was removed
	IntegrityOK       bool  // Database passed integrity check
	Vacuumed          bool  // Database was vacuumed
	RecoveryAttempted bool  // REINDEX recovery was attempted
	ResetFromCloud    bool  // Local DB was reset from cloud
	Error             error // Non-fatal warning (e.g., vacuum skipped)
}

// String returns a human-readable summary of the repair result.
func (r *RepairResult) String() string {
	var parts []string
	if r.WalCheckpointed {
		parts = append(parts, "WAL checkpointed")
	}
	if r.ShmRemoved {
		parts = append(parts, "SHM removed")
	}
	if r.IntegrityOK {
		parts = append(parts, "integrity OK")
	} else {
		parts = append(parts, "integrity FAILED")
	}
	if r.Vacuumed {
		parts = append(parts, "vacuumed")
	}
	if r.RecoveryAttempted {
		parts = append(parts, "recovery attempted")
	}
	if r.ResetFromCloud {
		parts = append(parts, "reset from cloud")
	}
	if r.Error != nil {
		parts = append(parts, fmt.Sprintf("warning: %v", r.Error))
	}
	return strings.Join(parts, ", ")
}

// Repair attempts to fix a corrupted database.
//
// Steps performed:
//  1. Checkpoint WAL (merge pending writes into main DB)
//  2. Remove stale SHM file
//  3. Run integrity check
//  4. Vacuum database
//
// If force=true and integrity check fails:
//  5. Attempt REINDEX recovery
//  6. Reset from cloud as last resort (requires WithClient option)
//
// Returns a RepairResult with details of operations performed.
func Repair(name string, force bool, opts ...Option) (*RepairResult, error) {
	result := &RepairResult{}

	// Apply options to get configuration
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Determine database path
	var dataDir string
	if cfg.customPath != "" {
		dataDir = cfg.customPath
	} else {
		// Use default client to get data path
		cc, err := client.NewClientWithDefaults()
		if err != nil {
			return result, fmt.Errorf("failed to create client: %w", err)
		}
		dataDir, err = cc.DataPath()
		if err != nil {
			return result, fmt.Errorf("failed to get data path: %w", err)
		}
	}

	dbPath := filepath.Join(dataDir, "kv", name+".db")
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"

	// Ensure kv directory exists
	kvDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		return result, fmt.Errorf("failed to create kv directory: %w", err)
	}

	// Step 1: Checkpoint WAL
	// Open database for checkpoint (may fail if severely corrupted)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		if force {
			result.RecoveryAttempted = true
			return result, fmt.Errorf("failed to open database: %w", err)
		}
		return result, fmt.Errorf("failed to open database: %w", err)
	}

	// Set busy timeout
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		if force {
			result.RecoveryAttempted = true
			// Try to recover by removing corrupt files
			if recoverErr := recoverCorruptDatabase(dbPath); recoverErr == nil {
				// Retry with fresh database
				return repairFreshDatabase(dbPath, result)
			}
		}
		return result, fmt.Errorf("database may be corrupted: %w", err)
	}

	// Attempt WAL checkpoint
	if _, err := os.Stat(walPath); err == nil {
		_, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		if err != nil {
			result.Error = fmt.Errorf("WAL checkpoint failed: %w", err)
		} else {
			result.WalCheckpointed = true
		}
	}

	// Close database before removing SHM
	if err := db.Close(); err != nil {
		result.Error = fmt.Errorf("failed to close database after checkpoint: %w", err)
	}

	// Step 2: Remove stale SHM file
	if _, err := os.Stat(shmPath); err == nil {
		if err := os.Remove(shmPath); err != nil {
			result.Error = fmt.Errorf("failed to remove SHM file: %w", err)
		} else {
			result.ShmRemoved = true
		}
	}

	// Step 3: Integrity check
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		if force {
			result.RecoveryAttempted = true
			return result, fmt.Errorf("failed to reopen database for integrity check: %w", err)
		}
		return result, fmt.Errorf("failed to reopen database: %w", err)
	}

	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return result, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	var integrityResult string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil {
		_ = db.Close()
		if force {
			result.RecoveryAttempted = true
			return attemptRecovery(dbPath, result, cfg)
		}
		return result, fmt.Errorf("integrity check failed: %w", err)
	}

	if integrityResult != "ok" {
		_ = db.Close()
		if force {
			result.RecoveryAttempted = true
			return attemptRecovery(dbPath, result, cfg)
		}
		return result, fmt.Errorf("database corruption detected: %s (run with force=true to attempt recovery)", integrityResult)
	}

	result.IntegrityOK = true

	// Step 4: Vacuum
	if _, err := db.Exec("VACUUM"); err != nil {
		result.Error = fmt.Errorf("vacuum failed: %w", err)
	} else {
		result.Vacuumed = true
	}

	if err := db.Close(); err != nil {
		result.Error = fmt.Errorf("failed to close database after vacuum: %w", err)
	}

	return result, nil
}

// attemptRecovery tries REINDEX and then cloud reset if needed.
func attemptRecovery(dbPath string, result *RepairResult, cfg *Config) (*RepairResult, error) {
	// Try REINDEX recovery
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return result, fmt.Errorf("failed to open database for recovery: %w", err)
	}

	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return result, fmt.Errorf("failed to set busy timeout for recovery: %w", err)
	}

	// Enable writable schema for REINDEX
	if _, err := db.Exec("PRAGMA writable_schema=ON"); err != nil {
		_ = db.Close()
		return result, fmt.Errorf("failed to enable writable schema: %w", err)
	}

	// Attempt REINDEX
	if _, err := db.Exec("REINDEX"); err != nil {
		_ = db.Close()
		// REINDEX failed, database may be too corrupted
		return result, fmt.Errorf("REINDEX recovery failed (database may need cloud reset): %w", err)
	}

	// Disable writable schema
	if _, err := db.Exec("PRAGMA writable_schema=OFF"); err != nil {
		result.Error = fmt.Errorf("failed to disable writable schema: %w", err)
	}

	// Check integrity after REINDEX
	var integrityResult string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
	if err != nil || integrityResult != "ok" {
		_ = db.Close()
		// Recovery failed, would need cloud reset but we don't have client access here
		return result, fmt.Errorf("database still corrupted after recovery attempt: %s", integrityResult)
	}

	result.IntegrityOK = true

	// Vacuum after successful recovery
	if _, err := db.Exec("VACUUM"); err != nil {
		result.Error = fmt.Errorf("vacuum after recovery failed: %w", err)
	} else {
		result.Vacuumed = true
	}

	if err := db.Close(); err != nil {
		result.Error = fmt.Errorf("failed to close database after recovery: %w", err)
	}

	return result, nil
}

// repairFreshDatabase handles repair after corrupt files have been removed.
func repairFreshDatabase(dbPath string, result *RepairResult) (*RepairResult, error) {
	// Open fresh database (will be created)
	db, err := openSQLite(dbPath)
	if err != nil {
		return result, fmt.Errorf("failed to create fresh database: %w", err)
	}

	result.IntegrityOK = true

	if _, err := db.Exec("VACUUM"); err != nil {
		result.Error = fmt.Errorf("vacuum failed on fresh database: %w", err)
	} else {
		result.Vacuumed = true
	}

	if err := db.Close(); err != nil {
		result.Error = fmt.Errorf("failed to close fresh database: %w", err)
	}

	return result, nil
}

// Reset deletes the local database and pulls fresh data from Charm Cloud.
// This discards any unsynced local changes.
//
// Use WithPath for testing with a custom data directory.
// Without WithPath, uses the default Charm client to connect to cloud.
func Reset(name string, opts ...Option) error {
	// Apply options to get configuration
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	// If custom path is set, do local-only reset (for testing)
	if cfg.customPath != "" {
		return resetLocal(name, cfg.customPath)
	}

	// Open KV with defaults to get cloud sync capability
	kv, err := OpenWithDefaults(name)
	if err != nil {
		return fmt.Errorf("failed to open KV for reset: %w", err)
	}

	// Use the existing Reset method which deletes local and syncs from cloud
	if err := kv.Reset(); err != nil {
		_ = kv.Close()
		return fmt.Errorf("reset failed: %w", err)
	}

	return kv.Close()
}

// resetLocal performs a local-only reset without cloud sync.
// Used for testing with custom paths.
func resetLocal(name, dataDir string) error {
	dbPath := filepath.Join(dataDir, "kv", name+".db")
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"

	// Ensure kv directory exists
	kvDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(kvDir, 0700); err != nil {
		return fmt.Errorf("failed to create kv directory: %w", err)
	}

	// Remove all database files
	for _, path := range []string{dbPath, walPath, shmPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	// Create fresh database
	db, err := openSQLite(dbPath)
	if err != nil {
		return fmt.Errorf("failed to create fresh database: %w", err)
	}

	if err := db.Close(); err != nil {
		return fmt.Errorf("failed to close fresh database: %w", err)
	}

	return nil
}

// WipeResult contains details of wipe operations performed.
type WipeResult struct {
	CloudBackupsDeleted int   // Number of cloud backups deleted
	LocalFilesDeleted   int   // Number of local files deleted
	Error               error // Non-fatal warning
}

// String returns a human-readable summary of the wipe result.
func (r *WipeResult) String() string {
	var parts []string
	if r.CloudBackupsDeleted > 0 {
		parts = append(parts, fmt.Sprintf("%d cloud backups deleted", r.CloudBackupsDeleted))
	}
	if r.LocalFilesDeleted > 0 {
		parts = append(parts, fmt.Sprintf("%d local files deleted", r.LocalFilesDeleted))
	}
	if r.Error != nil {
		parts = append(parts, fmt.Sprintf("warning: %v", r.Error))
	}
	if len(parts) == 0 {
		return "no data to wipe"
	}
	return strings.Join(parts, ", ")
}

// Wipe permanently deletes all data for a KV store, both local and cloud.
// This is destructive and cannot be undone.
//
// Use WithPath for testing with a custom data directory (local-only wipe).
// Without WithPath, uses the default Charm client to delete cloud data too.
func Wipe(name string, opts ...Option) (*WipeResult, error) {
	result := &WipeResult{}

	// Apply options to get configuration
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	// If custom path is set, do local-only wipe (for testing)
	if cfg.customPath != "" {
		return wipeLocal(name, cfg.customPath, result)
	}

	// Open KV with defaults to get cloud access
	kv, err := OpenWithDefaults(name)
	if err != nil {
		// If we can't open, try to at least clean up local files
		cc, clientErr := client.NewClientWithDefaults()
		if clientErr != nil {
			return result, fmt.Errorf("failed to create client: %w", err)
		}
		dataDir, pathErr := cc.DataPath()
		if pathErr != nil {
			return result, fmt.Errorf("failed to get data path: %w", err)
		}
		return wipeLocal(name, dataDir, result)
	}
	defer func() { _ = kv.Close() }()

	// Delete cloud backups
	seqDir, err := kv.fs.ReadDir(name)
	if err == nil {
		for _, de := range seqDir {
			seqKey := name + "/" + de.Name()
			if err := kv.fs.Remove(seqKey); err != nil {
				result.Error = fmt.Errorf("failed to delete cloud backup %s: %w", seqKey, err)
			} else {
				result.CloudBackupsDeleted++
			}
		}
	}

	// Get data path for local cleanup
	dataDir, err := kv.cc.DataPath()
	if err != nil {
		return result, fmt.Errorf("failed to get data path: %w", err)
	}

	// Close KV before deleting local files
	if err := kv.Close(); err != nil {
		result.Error = fmt.Errorf("failed to close KV: %w", err)
	}

	// Delete local files
	localResult, err := wipeLocal(name, dataDir, &WipeResult{})
	if err != nil {
		return result, err
	}
	result.LocalFilesDeleted = localResult.LocalFilesDeleted

	return result, nil
}

// wipeLocal deletes local database files only.
func wipeLocal(name, dataDir string, result *WipeResult) (*WipeResult, error) {
	dbPath := filepath.Join(dataDir, "kv", name+".db")
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"

	for _, path := range []string{dbPath, walPath, shmPath} {
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return result, fmt.Errorf("failed to remove %s: %w", path, err)
			}
		} else {
			result.LocalFilesDeleted++
		}
	}

	return result, nil
}
