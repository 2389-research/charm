# KV Storage Migration: BadgerDB to SQLite

## Summary

Replace BadgerDB with SQLite as the local storage engine for the `kv` package. Keep the existing cloud sync model (full database backups to Charm Cloud). No backend interface abstraction - clean swap.

## Motivation

1. **Better concurrency** - SQLite's WAL mode handles multiple readers + one writer cleanly, eliminating lock contention issues that required read-only fallback modes
2. **Maturity and trust** - SQLite has 20+ years of production hardening
3. **Easier debugging** - SQLite databases are inspectable with standard tools (sqlite3 CLI, DB browsers)
4. **Eliminates race detector issues** - Current tests skip with `-race` due to BadgerDB internals

## Approach

- **Storage engine:** SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Encryption:** Application-level encryption (existing pattern), SQLite stores encrypted blobs
- **Sync model:** Unchanged - full database backups uploaded to Charm Cloud
- **Backend interface:** None - direct swap, YAGNI
- **Migration:** None - new SQLite database syncs from cloud on first open

## Schema

```sql
-- Main KV storage
CREATE TABLE kv (
    key   BLOB PRIMARY KEY,
    value BLOB NOT NULL
) WITHOUT ROWID;

-- Sync metadata (replaces Badger's internal sequence tracking)
CREATE TABLE meta (
    name  TEXT PRIMARY KEY,
    value INTEGER NOT NULL
) WITHOUT ROWID;
```

**Design notes:**

- `WITHOUT ROWID` optimizes for primary key lookups (our main access pattern)
- `BLOB` types preserve encrypted binary keys and values exactly
- `meta` table stores `max_version` (current sequence number)

## Operation Mapping

| Operation | BadgerDB | SQLite |
|-----------|----------|--------|
| Get | `txn.Get(key)` | `SELECT value FROM kv WHERE key = ?` |
| Set | `txn.Set(key, val)` | `INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)` |
| Delete | `txn.Delete(key)` | `DELETE FROM kv WHERE key = ?` |
| List keys | Badger iterator | `SELECT key FROM kv` |
| Backup | `Stream.Backup()` | SQLite Backup API |
| Restore | `DB.Load()` | Replace file + reopen |

## Backup & Restore

Use SQLite's Online Backup API (`sqlite3_backup_*`), available in `modernc.org/sqlite`:

```go
// Backup: safe even with concurrent readers
srcConn.Backup("main", dstConn, "main")

// Restore: download to temp file, close DB, replace, reopen
```

The Backup API handles WAL checkpointing automatically and doesn't block readers.

**Key difference from Badger:** Badger's `Load()` merges data; SQLite backup replaces the file. Current sync already does full snapshots from version 0, so this matches the existing semantics.

## Public API Changes

None. The public API remains identical:

```go
func Open(name string, opts ...Option) (*KV, error)
func (kv *KV) Get(key string) ([]byte, error)
func (kv *KV) Set(key string, value []byte) error
func (kv *KV) Delete(key string) error
func (kv *KV) Keys() ([]string, error)
func (kv *KV) Sync() error
func (kv *KV) Close() error
// etc.
```

## Internal Changes

| Component | Before | After |
|-----------|--------|-------|
| `kv.DB` field | `*badger.DB` | `*sqlite.Conn` |
| Transactions | Badger txn wrapper | SQL transactions |
| Sequence tracking | Badger timestamps | `meta` table |
| Backup | `Stream.Backup()` | SQLite Backup API |
| Encryption | `encryptKeyToBadgerKey()` | Same (unchanged) |

## Concurrency Improvements

SQLite with WAL mode provides:

- Multiple concurrent readers
- One writer (doesn't block readers)
- No lock contention requiring fallback modes

The current `Sync()` TODO about concurrent transaction safety goes away - WAL mode handles this correctly.

## Migration Path

No migration tooling. New approach:

1. New version creates SQLite database
2. On first `Open()`, database is empty
3. `Sync()` pulls data from Charm Cloud
4. Old BadgerDB files are orphaned/ignored

Users with unsynced local data would lose it, but unsynced data is already a bug.

## Testing

**Existing tests (should pass unchanged):**
- Integration tests in `integration/integration_test.go`
- API behavior tests

**Tests to update:**
- `client_test.go` - internal encryption tests
- `errors_test.go` - lock detection (simplified with WAL mode)

**Tests to remove:**
- `-race` skip workaround (SQLite doesn't have Badger's issues)

**New tests:**
- WAL mode concurrent access
- SQLite Backup API during active reads

## Dependencies

**Add:**
- `modernc.org/sqlite` - Pure Go SQLite

**Remove:**
- `github.com/dgraph-io/badger/v3`

## Risks

1. **Performance** - Pure Go SQLite is slightly slower than CGO Badger for write-heavy workloads. Unlikely to matter for KV store usage patterns.

2. **File format** - SQLite files are not append-only like Badger. Corruption recovery differs. SQLite's track record here is excellent.

3. **Downstream breakage** - Any code depending on internal Badger types breaks. Public API is stable.
