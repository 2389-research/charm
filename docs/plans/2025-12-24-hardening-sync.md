# Hardening Charm KV Sync

## Status: Draft
## Author: Claude + Harper
## Date: 2025-12-24

---

## Problem Statement

The current Charm KV sync mechanism has several reliability gaps that can cause data loss, corruption, or confusing behavior under real-world conditions:

1. **Full-snapshot sync** - Every backup is a complete database. No incremental updates.
2. **Last-write-wins without conflict detection** - Silent data loss when devices race.
3. **No idempotency guarantees** - Retried requests can cause inconsistent state.
4. **No pagination** - Large sync histories can timeout.
5. **Burned sequence numbers** - Failed uploads leave gaps in sequence space.
6. **No local sync lock** - Concurrent Sync() calls can race.
7. **No "doctor" command** - No way to verify sync health.

---

## Current Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         CLIENT                                   │
├─────────────────────────────────────────────────────────────────┤
│  SQLite DB                                                       │
│  ┌─────────────┬─────────────────────────────────────┐          │
│  │ kv table    │ key BLOB PK, value BLOB             │          │
│  ├─────────────┼─────────────────────────────────────┤          │
│  │ meta table  │ max_version INTEGER                 │          │
│  └─────────────┴─────────────────────────────────────┘          │
│                                                                  │
│  Sync() flow:                                                    │
│  1. If pendingWrites > 0: backup to cloud                        │
│  2. Pull latest backup from cloud                                │
│  3. Overwrite local DB with cloud version                        │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                         SERVER                                   │
├─────────────────────────────────────────────────────────────────┤
│  POST /v1/seq/:name     → allocate next sequence number          │
│  POST /v1/fs/{name}/{seq} → store full SQLite backup             │
│  GET  /v1/fs/{name}/    → list all backup sequence files         │
│  GET  /v1/fs/{name}/{seq} → download specific backup             │
└─────────────────────────────────────────────────────────────────┘
```

### Failure Modes

| Scenario | Current Behavior | Impact |
|----------|-----------------|--------|
| Two devices write simultaneously | Both get seq, both upload, last upload wins | Silent data loss |
| Upload fails after seq allocated | Seq burned, gap in sequence space | Wasted sequences (minor) |
| Network timeout during pull | Partial state, retry needed | Confusing errors |
| Two Sync() calls race locally | Both read same max_version | Duplicate uploads |
| Old BadgerDB backup in cloud | Detected and cleaned up | Works correctly |
| Large sync history | All files listed at once | Timeouts |

---

## Proposed Architecture

### Phase 1: Local Durability & Sync Lock (Low Risk)

**Goal**: Make local operations reliable before touching the sync protocol.

#### 1.1 SQLite Hardening

```sql
-- Already have WAL mode, add:
PRAGMA synchronous = NORMAL;  -- or FULL for paranoid mode
PRAGMA busy_timeout = 5000;   -- 5 second wait on lock contention
```

#### 1.2 Local Sync Lock

Prevent concurrent Sync() calls from racing:

```go
// New table in schema
CREATE TABLE IF NOT EXISTS sync_lock (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    holder TEXT,
    acquired_at INTEGER,
    expires_at INTEGER
);

// Acquire lock before sync
func (kv *KV) acquireSyncLock() (bool, error) {
    now := time.Now().Unix()
    expiry := now + 60 // 60 second lease

    // Try to acquire or take over expired lock
    result, err := kv.db.Exec(`
        INSERT INTO sync_lock (id, holder, acquired_at, expires_at)
        VALUES (1, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            holder = excluded.holder,
            acquired_at = excluded.acquired_at,
            expires_at = excluded.expires_at
        WHERE expires_at < ?
    `, kv.instanceID, now, expiry, now)

    if err != nil {
        return false, err
    }
    rows, _ := result.RowsAffected()
    return rows > 0, nil
}
```

#### 1.3 Pending Writes Durability

Track pending writes in SQLite, not memory:

```sql
CREATE TABLE IF NOT EXISTS pending_ops (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    op_type TEXT NOT NULL,  -- 'set' or 'delete'
    key BLOB NOT NULL,
    value BLOB,
    created_at INTEGER NOT NULL
);
```

```go
func (kv *KV) Set(key, value []byte) error {
    tx, _ := kv.db.Begin()
    defer tx.Rollback()

    // Write to kv table
    tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, value)

    // Track pending op
    tx.Exec("INSERT INTO pending_ops (op_type, key, value, created_at) VALUES ('set', ?, ?, ?)",
        key, value, time.Now().Unix())

    return tx.Commit()
}
```

---

### Phase 2: Idempotent Backup Uploads (Medium Risk)

**Goal**: Make backup uploads safe to retry.

#### 2.1 Content-Addressed Backups

Instead of `{name}/{seq}`, use `{name}/{content_hash}`:

```go
func (kv *KV) backupSeq(seq uint64) error {
    buf := bytes.NewBuffer(nil)
    sqliteBackup(kv.dbPath, buf)

    // Content-address the backup
    hash := sha256.Sum256(buf.Bytes())
    contentKey := hex.EncodeToString(hash[:16]) // 128-bit prefix

    // Upload is now idempotent - same content = same key
    name := fmt.Sprintf("%s/%d-%s", kv.name, seq, contentKey)
    return kv.fs.WriteFile(name, ...)
}
```

#### 2.2 Manifest File

Track what backups exist and their relationships:

```json
// {name}/manifest.json
{
    "version": 1,
    "latest_seq": 42,
    "backups": [
        {"seq": 42, "hash": "abc123...", "created_at": "2025-12-24T12:00:00Z", "device_id": "device-1"},
        {"seq": 41, "hash": "def456...", "created_at": "2025-12-24T11:55:00Z", "device_id": "device-2"}
    ]
}
```

Upload flow becomes:
1. Upload backup to `{name}/{seq}-{hash}`
2. Download current manifest
3. Append our backup to manifest
4. Upload new manifest (optimistic concurrency via ETag if available)

---

### Phase 3: Op-Log Based Sync (High Effort, Maximum Reliability)

**Goal**: Convert from full-snapshot to incremental op-log sync.

This is the "make it bulletproof" option but requires significant changes.

#### 3.1 Op-Log Schema

```sql
CREATE TABLE IF NOT EXISTS op_log (
    op_id TEXT PRIMARY KEY,        -- UUID, stable across retries
    seq INTEGER NOT NULL,          -- local sequence number
    op_type TEXT NOT NULL,         -- 'set' or 'delete'
    key BLOB NOT NULL,
    value BLOB,
    hlc_timestamp INTEGER NOT NULL, -- hybrid logical clock
    device_id TEXT NOT NULL,
    synced INTEGER DEFAULT 0       -- 0=pending, 1=synced
);

CREATE INDEX idx_op_log_synced ON op_log(synced, seq);
CREATE INDEX idx_op_log_key ON op_log(key, hlc_timestamp DESC);
```

#### 3.2 Write Path

```go
func (kv *KV) Set(key, value []byte) error {
    opID := uuid.New().String()
    hlc := kv.hlc.Now()

    tx, _ := kv.db.Begin()
    defer tx.Rollback()

    // Log the operation
    tx.Exec(`INSERT INTO op_log (op_id, seq, op_type, key, value, hlc_timestamp, device_id)
             VALUES (?, ?, 'set', ?, ?, ?, ?)`,
        opID, kv.nextLocalSeq(), key, value, hlc, kv.deviceID)

    // Apply to kv table
    tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, value)

    return tx.Commit()
}
```

#### 3.3 Sync Protocol

**Push (client → server):**
```
POST /v1/kv/{name}/ops
{
    "ops": [
        {"op_id": "uuid-1", "op_type": "set", "key": "...", "value": "...", "hlc": 123, "device_id": "..."},
        {"op_id": "uuid-2", "op_type": "delete", "key": "...", "hlc": 124, "device_id": "..."}
    ]
}

Response:
{
    "accepted": ["uuid-1", "uuid-2"],
    "rejected": [],
    "server_cursor": 456
}
```

**Pull (server → client):**
```
GET /v1/kv/{name}/ops?after=123&limit=100

Response:
{
    "ops": [...],
    "cursor": 223,
    "has_more": true
}
```

#### 3.4 Conflict Resolution

Last-write-wins using HLC + device_id as tiebreaker:

```go
func (kv *KV) applyOp(op Op) error {
    // Check if we already applied this op
    var exists int
    kv.db.QueryRow("SELECT 1 FROM op_log WHERE op_id = ?", op.OpID).Scan(&exists)
    if exists == 1 {
        return nil // idempotent - already applied
    }

    // Check if there's a newer op for this key
    var newerHLC int64
    kv.db.QueryRow(`SELECT MAX(hlc_timestamp) FROM op_log
                    WHERE key = ? AND hlc_timestamp > ?`, op.Key, op.HLC).Scan(&newerHLC)
    if newerHLC > 0 {
        // Just log the op but don't apply (superseded by newer op)
        kv.db.Exec(`INSERT INTO op_log (...) VALUES (...)`)
        return nil
    }

    // Apply the op
    tx, _ := kv.db.Begin()
    tx.Exec(`INSERT INTO op_log (...) VALUES (...)`)
    if op.OpType == "set" {
        tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", op.Key, op.Value)
    } else {
        tx.Exec("DELETE FROM kv WHERE key = ?", op.Key)
    }
    return tx.Commit()
}
```

---

### Phase 4: Doctor Command & Invariants

**Goal**: Built-in health checking.

```go
type DoctorResult struct {
    IntegrityOK       bool
    PendingOps        int
    LastSyncTime      time.Time
    LastSyncSeq       uint64
    ServerSeq         uint64
    SyncLag           uint64
    OrphanedBackups   []string
    Errors            []string
}

func (kv *KV) Doctor() (*DoctorResult, error) {
    result := &DoctorResult{}

    // SQLite integrity
    var integrityResult string
    kv.db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult)
    result.IntegrityOK = integrityResult == "ok"

    // Pending ops count
    kv.db.QueryRow("SELECT COUNT(*) FROM pending_ops").Scan(&result.PendingOps)

    // Sync status
    result.LastSyncSeq = kv.maxVersion()
    serverSeq, _ := kv.getServerSeq()
    result.ServerSeq = serverSeq
    result.SyncLag = serverSeq - result.LastSyncSeq

    // Check for orphaned backups (backed up but not in manifest)
    // ...

    return result, nil
}
```

CLI:
```bash
$ charm kv doctor memo
✓ SQLite integrity: OK
✓ Pending ops: 0
✓ Last sync: 2 minutes ago (seq 42)
✓ Server seq: 42 (in sync)
✓ No orphaned backups
```

---

## Migration Path

### Phase 1 (can ship immediately)
- SQLite PRAGMA hardening
- Local sync lock
- No protocol changes, fully backward compatible

### Phase 2 (minor protocol extension)
- Content-addressed backups
- Manifest file
- Old clients continue to work (ignore manifest)
- New clients prefer manifest when available

### Phase 3 (major version bump)
- Op-log sync requires new server endpoints
- Migration: export current state as ops, start fresh
- Can run in parallel with old protocol during transition

### Phase 4 (anytime)
- Doctor command is client-only
- No server changes needed

---

## Recommended Order

1. **Ship Phase 1 now** - Zero risk, immediate reliability win
2. **Ship Phase 4 next** - Doctor command helps diagnose issues
3. **Evaluate Phase 2** - If manifest approach is enough, skip Phase 3
4. **Phase 3 only if needed** - Full op-log is the nuclear option

---

## Open Questions

1. **HLC vs server-assigned timestamps?** HLC is more robust for offline-first, but server timestamps are simpler.

2. **Snapshot compaction?** If we go op-log, we need periodic compaction to prevent unbounded growth.

3. **Encryption at op level?** Currently the whole backup is encrypted. Op-log would need per-op encryption.

4. **Backward compatibility period?** How long to support old full-snapshot clients?

---

## Appendix: SQLite Settings Reference

```sql
-- Durability
PRAGMA journal_mode = WAL;      -- Already set
PRAGMA synchronous = NORMAL;    -- Good balance (FULL for paranoid)
PRAGMA wal_autocheckpoint = 1000; -- Checkpoint every 1000 pages

-- Concurrency
PRAGMA busy_timeout = 5000;     -- Wait 5s on lock contention

-- Safety
PRAGMA foreign_keys = ON;       -- If we add FKs later
PRAGMA secure_delete = OFF;     -- Performance (we encrypt anyway)

-- Performance
PRAGMA cache_size = -2000;      -- 2MB cache
PRAGMA mmap_size = 268435456;   -- 256MB mmap
```
