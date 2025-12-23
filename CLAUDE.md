# charm Library

This is the 2389-research fork of charmbracelet/charm with self-hosted server support and KV improvements.

## Repository Boundaries

When working in this repo, stay focused on charm library code only. If downstream clients (bbs-mcp, memo, etc.) need updates to use new features:

1. Document what changes they need (text file or release notes)
2. Do NOT make changes in other repos unless explicitly asked
3. Ask: "Want me to update the client too, or just document it?"

## Key Packages

- `kv/` - Key-value store with cloud sync (SQLite-based, migrated from BadgerDB)
- `client/` - Charm Cloud client
- `fs/` - Encrypted filesystem
- `server/` - Self-hosted server implementation

## KV Package Features

The `kv/` package has several important functions for database management:

### Opening Modes
- `OpenWithDefaults(name)` - Opens KV store with write access (exclusive lock)
- `OpenWithDefaultsReadOnly(name)` - Opens in read-only mode
- `OpenWithDefaultsFallback(name)` - Tries write access, falls back to read-only if locked

### Database Maintenance (repair.go)
- `Repair(name, force, opts...)` - Repairs corrupted databases (WAL checkpoint, SHM cleanup, integrity check, vacuum). With `force=true`, attempts REINDEX recovery.
- `Reset(name, opts...)` - Deletes local database and pulls fresh data from Charm Cloud. Discards unsynced local changes.
- `Wipe(name, opts...)` - Permanently deletes all data (local AND cloud). Destructive and irreversible.
- `(*KV).Reset()` - Instance method that wipes local data and re-syncs from cloud

### Concurrency Notes
- File locking is cross-platform (Unix flock, Windows exclusive access)
- Concurrent recovery attempts are serialized via file lock
- SQLite WAL mode allows concurrent readers with single writer
- See `docs/plans/2025-12-23-concurrent-kv-access.md` for future improvements

## Testing

Run tests with: `go test ./...`

Note: Some packages have no tests (UI, server internals). The KV and client packages have good coverage.

## Releasing

Use the `releasing-software` skill. Last release: v0.17.0 (Repair/Reset/Wipe functions).
