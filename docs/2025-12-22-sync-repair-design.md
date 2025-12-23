# Sync Repair Command Design

## Overview

Add database repair functionality to chronicle via new `kv.Repair()`, `kv.Reset()`, and `kv.Wipe()` functions in the charm library, exposed through chronicle CLI commands.

## Problem

SQLite databases using WAL mode can become corrupted, particularly the SHM (shared memory) file. Users currently need to manually run sqlite3 commands to recover. This should be a built-in repair command.

## Charm KV Library API

```go
// In github.com/charmbracelet/charm/kv

// === REPAIR ===

// RepairResult contains details of repair operations performed.
type RepairResult struct {
    WalCheckpointed   bool   // WAL was checkpointed into main DB
    ShmRemoved        bool   // Stale SHM file was removed
    IntegrityOK       bool   // Database passed integrity check
    Vacuumed          bool   // Database was vacuumed
    RecoveryAttempted bool   // REINDEX recovery was attempted
    ResetFromCloud    bool   // Local DB was reset from cloud
    Error             error  // Non-fatal warning (e.g., vacuum skipped)
}

// Repair attempts to fix a corrupted database.
// Steps: checkpoint WAL -> remove SHM -> integrity check -> vacuum
// If force=true and integrity fails, attempts REINDEX recovery.
func Repair(name string, force bool, opts ...Option) (*RepairResult, error)

// === RESET ===

// Reset deletes the local database and pulls fresh data from Charm Cloud.
// This discards any unsynced local changes.
func Reset(name string, opts ...Option) error

// === WIPE ===

// WipeResult contains details of wipe operations performed.
type WipeResult struct {
    CloudBackupsDeleted int   // Number of cloud backups deleted
    LocalFilesDeleted   int   // Number of local files deleted
    Error               error // Non-fatal warning
}

// Wipe permanently deletes all data for a KV store, both local and cloud.
// This is destructive and cannot be undone.
func Wipe(name string, opts ...Option) (*WipeResult, error)
```

## Chronicle CLI Commands

### `chronicle sync repair`

```
Usage: chronicle sync repair [--force]

Repair a corrupted local database.

Steps performed:
  1. Checkpoint WAL (merge pending writes into main DB)
  2. Remove stale SHM file
  3. Run integrity check
  4. Vacuum database

Flags:
  --force   If corruption persists, attempt REINDEX recovery

Examples:
  chronicle sync repair          # Safe repair
  chronicle sync repair --force  # Aggressive repair with REINDEX recovery
```

### `chronicle sync reset`

```
Usage: chronicle sync reset

Discard local database and re-download from Charm Cloud.
Useful when local data is corrupted beyond repair.

Requires confirmation prompt.
```

### `chronicle sync wipe`

```
Usage: chronicle sync wipe

Permanently delete ALL data, both local and cloud.
This cannot be undone!

Requires confirmation prompt.
```

## Command Summary

| Command | Local Data | Cloud Data | Use Case |
|---------|------------|------------|----------|
| `repair` | Fix in place | Unchanged | Corruption, WAL/SHM issues |
| `reset` | Delete & re-sync | Unchanged | Local corruption, fresh start |
| `wipe` | Delete | Delete | Complete data removal |

## Repair Implementation Logic

```
Repair(name, force) flow:

1. Locate database files:
   - {dataDir}/kv/{name}.db
   - {dataDir}/kv/{name}.db-wal
   - {dataDir}/kv/{name}.db-shm

2. Open DB with sqlite3 (direct, not through KV):
   - PRAGMA wal_checkpoint(TRUNCATE)
   - Close connection

3. Remove SHM file if exists (it's recreated on next open)

4. Reopen and check integrity:
   - PRAGMA integrity_check
   - If "ok" -> continue
   - If not "ok" and !force -> return error with suggestion
   - If not "ok" and force -> goto step 5

5. Recovery attempt (force only):
   - PRAGMA writable_schema=ON
   - REINDEX
   - PRAGMA writable_schema=OFF
   - Re-check integrity
   - If still broken -> return error

6. Vacuum (if integrity OK):
   - VACUUM
   - Return success
```

## CLI Output Examples

**Successful repair:**
```
$ chronicle sync repair
Repairing chronicle database...
  ✓ WAL checkpointed
  ✓ SHM file removed
  ✓ Integrity check passed
  ✓ Database vacuumed

Repair complete.
```

**Corruption found, no --force:**
```
$ chronicle sync repair
Repairing chronicle database...
  ✓ WAL checkpointed
  ✓ SHM file removed
  ✗ Integrity check failed: database disk image is malformed

Repair incomplete. Run with --force to attempt recovery.
```

**Reset from cloud:**
```
$ chronicle sync reset
This will delete your local database and re-download from Charm Cloud.
Any unsynced local data will be lost.

Continue? [y/N] y

Resetting chronicle database...
  ✓ Local database deleted
  ✓ Synced from cloud (47 entries)

Reset complete.
```

**Wipe all data:**
```
$ chronicle sync wipe
WARNING: This will permanently delete ALL chronicle data!
This includes local AND cloud data. This cannot be undone.

Type 'wipe' to confirm: wipe

Wiping chronicle database...
  ✓ 3 cloud backups deleted
  ✓ 3 local files deleted

Wipe complete.
```

## Implementation Status

### Charm library (DONE)

- [x] `Repair(name string, force bool, opts ...Option) (*RepairResult, error)`
- [x] `Reset(name string, opts ...Option) error`
- [x] `Wipe(name string, opts ...Option) (*WipeResult, error)`
- [x] Tests for all functions

### Chronicle CLI (TODO)

- [ ] Add `syncRepairCmd` with `--force` flag
- [ ] Add `syncResetCmd` with confirmation prompt
- [ ] Add `syncWipeCmd` with confirmation prompt
- [ ] Wire up to charm's KV functions and display results
