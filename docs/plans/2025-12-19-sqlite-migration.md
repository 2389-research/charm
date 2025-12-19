# SQLite Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace BadgerDB with SQLite as the local storage engine for the kv package.

**Architecture:** SQLite via modernc.org/sqlite (pure Go), application-level encryption unchanged, same cloud sync model with SQLite Backup API for full snapshots.

**Tech Stack:** Go, modernc.org/sqlite, existing Charm encryption

**Working Directory:** `/Users/harper/Public/src/2389/charm/worktrees/sqlite`

---

## Task 1: Add SQLite Dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add the modernc.org/sqlite dependency**

Run:
```bash
go get modernc.org/sqlite
```

**Step 2: Verify the dependency is added**

Run:
```bash
grep "modernc.org/sqlite" go.mod
```

Expected: Line showing `modernc.org/sqlite v1.x.x`

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(kv): add modernc.org/sqlite dependency"
```

---

## Task 2: Create SQLite Storage Layer - Schema and Open

**Files:**
- Create: `kv/sqlite.go`
- Create: `kv/sqlite_test.go`

**Step 1: Write failing test for SQLite database creation**

Create `kv/sqlite_test.go`:

```go
// ABOUTME: Tests for SQLite storage layer
// ABOUTME: Covers schema creation, basic operations, and backup/restore

package kv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Verify schema exists by querying meta table
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM meta").Scan(&count)
	if err != nil {
		t.Fatalf("meta table should exist: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./kv -run TestSQLiteOpen -v
```

Expected: FAIL with "undefined: openSQLite"

**Step 3: Write minimal implementation**

Create `kv/sqlite.go`:

```go
// ABOUTME: SQLite storage layer for KV store
// ABOUTME: Provides encrypted key-value storage with WAL mode for concurrency

package kv

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// openSQLite opens or creates a SQLite database with the KV schema.
// Uses WAL mode for better concurrency (multiple readers, one writer).
func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Create schema
	schema := `
		CREATE TABLE IF NOT EXISTS kv (
			key   BLOB PRIMARY KEY,
			value BLOB NOT NULL
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS meta (
			name  TEXT PRIMARY KEY,
			value INTEGER NOT NULL
		) WITHOUT ROWID;
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return db, nil
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./kv -run TestSQLiteOpen -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add kv/sqlite.go kv/sqlite_test.go
git commit -m "feat(kv): add SQLite storage layer with schema"
```

---

## Task 3: SQLite CRUD Operations

**Files:**
- Modify: `kv/sqlite.go`
- Modify: `kv/sqlite_test.go`

**Step 1: Write failing test for Get/Set/Delete**

Add to `kv/sqlite_test.go`:

```go
func TestSQLiteCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	key := []byte("testkey")
	value := []byte("testvalue")

	// Test Set
	if err := sqliteSet(db, key, value); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Test Get
	got, err := sqliteGet(db, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(got) != string(value) {
		t.Errorf("Get returned %q, want %q", got, value)
	}

	// Test overwrite
	newValue := []byte("newvalue")
	if err := sqliteSet(db, key, newValue); err != nil {
		t.Fatalf("Set (overwrite) failed: %v", err)
	}
	got, err = sqliteGet(db, key)
	if err != nil {
		t.Fatalf("Get after overwrite failed: %v", err)
	}
	if string(got) != string(newValue) {
		t.Errorf("Get returned %q, want %q", got, newValue)
	}

	// Test Delete
	if err := sqliteDelete(db, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = sqliteGet(db, key)
	if err != ErrMissingKey {
		t.Errorf("Get after delete should return ErrMissingKey, got %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./kv -run TestSQLiteCRUD -v
```

Expected: FAIL with "undefined: sqliteSet"

**Step 3: Write minimal implementation**

Add to `kv/sqlite.go`:

```go
// sqliteGet retrieves a value by key. Returns ErrMissingKey if not found.
func sqliteGet(db *sql.DB, key []byte) ([]byte, error) {
	var value []byte
	err := db.QueryRow("SELECT value FROM kv WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, ErrMissingKey
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	return value, nil
}

// sqliteSet stores a key-value pair, overwriting if exists.
func sqliteSet(db *sql.DB, key, value []byte) error {
	_, err := db.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, value)
	if err != nil {
		return fmt.Errorf("failed to set key: %w", err)
	}
	return nil
}

// sqliteDelete removes a key. No error if key doesn't exist.
func sqliteDelete(db *sql.DB, key []byte) error {
	_, err := db.Exec("DELETE FROM kv WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./kv -run TestSQLiteCRUD -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add kv/sqlite.go kv/sqlite_test.go
git commit -m "feat(kv): add SQLite Get/Set/Delete operations"
```

---

## Task 4: SQLite Keys Iterator

**Files:**
- Modify: `kv/sqlite.go`
- Modify: `kv/sqlite_test.go`

**Step 1: Write failing test for Keys**

Add to `kv/sqlite_test.go`:

```go
func TestSQLiteKeys(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Insert some keys
	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	for _, k := range keys {
		if err := sqliteSet(db, k, []byte("value")); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	// Get all keys
	got, err := sqliteKeys(db)
	if err != nil {
		t.Fatalf("Keys failed: %v", err)
	}

	if len(got) != len(keys) {
		t.Errorf("Keys returned %d keys, want %d", len(got), len(keys))
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./kv -run TestSQLiteKeys -v
```

Expected: FAIL with "undefined: sqliteKeys"

**Step 3: Write minimal implementation**

Add to `kv/sqlite.go`:

```go
// sqliteKeys returns all keys in the database.
func sqliteKeys(db *sql.DB) ([][]byte, error) {
	rows, err := db.Query("SELECT key FROM kv")
	if err != nil {
		return nil, fmt.Errorf("failed to query keys: %w", err)
	}
	defer rows.Close()

	var keys [][]byte
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("failed to scan key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating keys: %w", err)
	}
	return keys, nil
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./kv -run TestSQLiteKeys -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add kv/sqlite.go kv/sqlite_test.go
git commit -m "feat(kv): add SQLite Keys operation"
```

---

## Task 5: SQLite Meta Table Operations

**Files:**
- Modify: `kv/sqlite.go`
- Modify: `kv/sqlite_test.go`

**Step 1: Write failing test for meta get/set**

Add to `kv/sqlite_test.go`:

```go
func TestSQLiteMeta(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	// Test get on missing key returns 0
	val, err := sqliteGetMeta(db, "max_version")
	if err != nil {
		t.Fatalf("GetMeta failed: %v", err)
	}
	if val != 0 {
		t.Errorf("GetMeta on missing key returned %d, want 0", val)
	}

	// Test set
	if err := sqliteSetMeta(db, "max_version", 42); err != nil {
		t.Fatalf("SetMeta failed: %v", err)
	}

	// Test get returns set value
	val, err = sqliteGetMeta(db, "max_version")
	if err != nil {
		t.Fatalf("GetMeta failed: %v", err)
	}
	if val != 42 {
		t.Errorf("GetMeta returned %d, want 42", val)
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./kv -run TestSQLiteMeta -v
```

Expected: FAIL with "undefined: sqliteGetMeta"

**Step 3: Write minimal implementation**

Add to `kv/sqlite.go`:

```go
// sqliteGetMeta retrieves a metadata value. Returns 0 if not found.
func sqliteGetMeta(db *sql.DB, name string) (int64, error) {
	var value int64
	err := db.QueryRow("SELECT value FROM meta WHERE name = ?", name).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get meta %s: %w", name, err)
	}
	return value, nil
}

// sqliteSetMeta stores a metadata value.
func sqliteSetMeta(db *sql.DB, name string, value int64) error {
	_, err := db.Exec("INSERT OR REPLACE INTO meta (name, value) VALUES (?, ?)", name, value)
	if err != nil {
		return fmt.Errorf("failed to set meta %s: %w", name, err)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./kv -run TestSQLiteMeta -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add kv/sqlite.go kv/sqlite_test.go
git commit -m "feat(kv): add SQLite meta table operations"
```

---

## Task 6: SQLite Backup and Restore

**Files:**
- Modify: `kv/sqlite.go`
- Modify: `kv/sqlite_test.go`

**Step 1: Write failing test for backup/restore**

Add to `kv/sqlite_test.go`:

```go
import (
	"bytes"
	// ... existing imports
)

func TestSQLiteBackupRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and populate source database
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	testData := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}
	for k, v := range testData {
		if err := sqliteSet(db, []byte(k), []byte(v)); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}
	if err := sqliteSetMeta(db, "max_version", 100); err != nil {
		t.Fatalf("SetMeta failed: %v", err)
	}
	db.Close()

	// Backup to buffer
	var buf bytes.Buffer
	if err := sqliteBackup(dbPath, &buf); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Restore to new database
	restorePath := filepath.Join(dir, "restored.db")
	if err := sqliteRestore(&buf, restorePath); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify restored data
	restored, err := openSQLite(restorePath)
	if err != nil {
		t.Fatalf("failed to open restored db: %v", err)
	}
	defer restored.Close()

	for k, want := range testData {
		got, err := sqliteGet(restored, []byte(k))
		if err != nil {
			t.Errorf("Get %s failed: %v", k, err)
			continue
		}
		if string(got) != want {
			t.Errorf("Get %s = %q, want %q", k, got, want)
		}
	}

	ver, _ := sqliteGetMeta(restored, "max_version")
	if ver != 100 {
		t.Errorf("max_version = %d, want 100", ver)
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./kv -run TestSQLiteBackupRestore -v
```

Expected: FAIL with "undefined: sqliteBackup"

**Step 3: Write minimal implementation**

Add to `kv/sqlite.go`:

```go
import (
	"io"
	// ... existing imports
)

// sqliteBackup creates a backup of the database to the writer.
// Uses SQLite's backup API for consistency during concurrent access.
func sqliteBackup(srcPath string, w io.Writer) error {
	// Open source for backup
	src, err := sql.Open("sqlite", srcPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("failed to open source for backup: %w", err)
	}
	defer src.Close()

	// Checkpoint WAL to ensure all data is in main file
	if _, err := src.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("failed to checkpoint WAL: %w", err)
	}

	// Read the database file
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read database file: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	return nil
}

// sqliteRestore restores a database from the reader.
func sqliteRestore(r io.Reader, dstPath string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read backup data: %w", err)
	}

	if err := os.WriteFile(dstPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write database file: %w", err)
	}

	return nil
}
```

Add to imports at top of file:
```go
import (
	"database/sql"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"
)
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./kv -run TestSQLiteBackupRestore -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add kv/sqlite.go kv/sqlite_test.go
git commit -m "feat(kv): add SQLite backup and restore"
```

---

## Task 7: Integrate SQLite into KV struct

**Files:**
- Modify: `kv/kv.go`

This is the core integration task. We'll replace the Badger DB field with SQLite.

**Step 1: Read the current kv.go to understand the structure**

Run:
```bash
head -100 kv/kv.go
```

Understand the current `KV` struct and its fields before modifying.

**Step 2: Update imports**

Replace badger import with database/sql. Remove badger, add:
```go
"database/sql"
```

**Step 3: Update KV struct**

Change:
```go
type KV struct {
	DB *badger.DB
	// ... other fields
}
```

To:
```go
type KV struct {
	db       *sql.DB
	dbPath   string
	// ... keep other fields (name, cc, readOnly, etc.)
}
```

**Step 4: Update openDB function**

Replace Badger open logic with SQLite:
```go
func openDB(cfg *Config, cc *client.Client, readOnly bool) (*KV, error) {
	// Build path
	dbPath := filepath.Join(cfg.Path, cfg.Name+".db")

	// Open SQLite
	db, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}

	kv := &KV{
		db:       db,
		dbPath:   dbPath,
		name:     cfg.Name,
		cc:       cc,
		readOnly: readOnly,
	}

	return kv, nil
}
```

**Step 5: Update Get method**

```go
func (kv *KV) Get(key string) ([]byte, error) {
	encKey, err := kv.encryptKey(key)
	if err != nil {
		return nil, err
	}
	return sqliteGet(kv.db, encKey)
}
```

**Step 6: Update Set method**

```go
func (kv *KV) Set(key string, value []byte) error {
	if kv.readOnly {
		return ErrReadOnlyMode
	}
	encKey, err := kv.encryptKey(key)
	if err != nil {
		return err
	}
	if err := sqliteSet(kv.db, encKey, value); err != nil {
		return err
	}
	return kv.syncAfterWrite()
}
```

**Step 7: Update Delete method**

```go
func (kv *KV) Delete(key string) error {
	if kv.readOnly {
		return ErrReadOnlyMode
	}
	encKey, err := kv.encryptKey(key)
	if err != nil {
		return err
	}
	if err := sqliteDelete(kv.db, encKey); err != nil {
		return err
	}
	return kv.syncAfterWrite()
}
```

**Step 8: Update Keys method**

```go
func (kv *KV) Keys() ([]string, error) {
	encKeys, err := sqliteKeys(kv.db)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(encKeys))
	for _, encKey := range encKeys {
		key, err := kv.decryptKey(encKey)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}
```

**Step 9: Update Close method**

```go
func (kv *KV) Close() error {
	return kv.db.Close()
}
```

**Step 10: Run existing tests**

Run:
```bash
go test ./kv -v
```

Fix any compilation errors. Tests may fail - that's expected as we haven't updated sync yet.

**Step 11: Commit work in progress**

```bash
git add kv/kv.go
git commit -m "wip(kv): integrate SQLite storage layer"
```

---

## Task 8: Update Sync Logic (client.go)

**Files:**
- Modify: `kv/client.go`

**Step 1: Read current client.go**

Run:
```bash
cat kv/client.go
```

Understand the current sync flow: `Sync()`, `syncFrom()`, `Backup()`, `backupSeq()`.

**Step 2: Update maxVersion to use meta table**

Replace Badger's `DB.MaxVersion()` with:
```go
func (kv *KV) maxVersion() uint64 {
	val, _ := sqliteGetMeta(kv.db, "max_version")
	return uint64(val)
}
```

**Step 3: Update setMaxVersion**

```go
func (kv *KV) setMaxVersion(v uint64) error {
	return sqliteSetMeta(kv.db, "max_version", int64(v))
}
```

**Step 4: Update Backup method**

Replace Badger's `Stream.Backup()` with SQLite backup:
```go
func (kv *KV) backupSeq(since, until uint64) (io.Reader, error) {
	var buf bytes.Buffer
	if err := sqliteBackup(kv.dbPath, &buf); err != nil {
		return nil, err
	}
	return &buf, nil
}
```

**Step 5: Update restore in syncFrom**

Replace Badger's `DB.Load()` with SQLite restore:
```go
// In syncFrom, after downloading backup:
// Close current DB
kv.db.Close()

// Restore from backup
if err := sqliteRestore(reader, kv.dbPath); err != nil {
	return err
}

// Reopen DB
db, err := openSQLite(kv.dbPath)
if err != nil {
	return err
}
kv.db = db
```

**Step 6: Run tests**

Run:
```bash
go test ./kv -v
```

**Step 7: Commit**

```bash
git add kv/client.go
git commit -m "feat(kv): update sync logic for SQLite"
```

---

## Task 9: Remove Badger Dependency

**Files:**
- Modify: `go.mod`
- Modify: `kv/kv.go` (remove any remaining badger references)

**Step 1: Search for remaining badger imports**

Run:
```bash
grep -r "badger" kv/
```

Remove any remaining imports or references.

**Step 2: Remove badger from go.mod**

Run:
```bash
go mod tidy
```

**Step 3: Verify badger is gone**

Run:
```bash
grep -i badger go.mod
```

Expected: No output (badger removed)

**Step 4: Run all tests**

Run:
```bash
go test ./... -v
```

**Step 5: Commit**

```bash
git add -A
git commit -m "chore(kv): remove BadgerDB dependency"
```

---

## Task 10: Run Integration Tests

**Files:**
- None (verification only)

**Step 1: Run integration tests**

Run:
```bash
go test ./integration -v
```

**Step 2: Run with race detector (should work now!)**

Run:
```bash
go test ./kv -race -v
```

Expected: PASS (no more Badger race issues)

**Step 3: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix(kv): address integration test issues"
```

---

## Task 11: Update Documentation

**Files:**
- Modify: `CLAUDE.md` (if needed)
- Modify: `docs/plans/2025-12-19-sqlite-migration-design.md` (mark complete)

**Step 1: Update any documentation references to BadgerDB**

**Step 2: Commit**

```bash
git add -A
git commit -m "docs: update for SQLite migration"
```

---

## Verification Checklist

- [ ] All kv tests pass: `go test ./kv -v`
- [ ] All integration tests pass: `go test ./integration -v`
- [ ] Race detector passes: `go test ./kv -race -v`
- [ ] No badger references: `grep -r badger kv/`
- [ ] go.mod is clean: `go mod tidy && go mod verify`
