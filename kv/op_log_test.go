package kv

import (
	"path/filepath"
	"testing"
)

func TestLogOp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	op := &Op{
		OpID:         newOpID(),
		Seq:          1,
		OpType:       "set",
		Key:          []byte("test-key"),
		Value:        []byte("test-value"),
		HLCTimestamp: 12345,
		DeviceID:     "device-1",
		Synced:       false,
	}

	if err := logOp(tx, op); err != nil {
		t.Fatalf("logOp failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify the op was logged
	exists, err := hasOp(db, op.OpID)
	if err != nil {
		t.Fatalf("hasOp failed: %v", err)
	}
	if !exists {
		t.Error("expected op to exist after logging")
	}
}

func TestHasOp_NotFound(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	exists, err := hasOp(db, "non-existent-uuid")
	if err != nil {
		t.Fatalf("hasOp failed: %v", err)
	}
	if exists {
		t.Error("expected op to not exist")
	}
}

func TestGetUnsyncedOps(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert some ops
	for i := 1; i <= 5; i++ {
		tx, _ := db.Begin()
		op := &Op{
			OpID:         newOpID(),
			Seq:          int64(i),
			OpType:       "set",
			Key:          []byte("key"),
			Value:        []byte("value"),
			HLCTimestamp: int64(i * 1000),
			DeviceID:     "device-1",
			Synced:       i%2 == 0, // Even ops are synced
		}
		if err := logOp(tx, op); err != nil {
			t.Fatalf("logOp failed: %v", err)
		}
		_ = tx.Commit()
	}

	// Get unsynced ops
	ops, err := getUnsyncedOps(db, 10)
	if err != nil {
		t.Fatalf("getUnsyncedOps failed: %v", err)
	}

	// Should have 3 unsynced ops (1, 3, 5)
	if len(ops) != 3 {
		t.Errorf("expected 3 unsynced ops, got %d", len(ops))
	}

	// Check they're in order
	for i, op := range ops {
		expectedSeq := int64(i*2 + 1) // 1, 3, 5
		if op.Seq != expectedSeq {
			t.Errorf("expected seq %d, got %d", expectedSeq, op.Seq)
		}
		if op.Synced {
			t.Errorf("expected unsynced op, got synced")
		}
	}
}

func TestMarkOpsSynced(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert ops
	var opIDs []string
	for i := 1; i <= 3; i++ {
		tx, _ := db.Begin()
		opID := newOpID()
		opIDs = append(opIDs, opID)
		op := &Op{
			OpID:         opID,
			Seq:          int64(i),
			OpType:       "set",
			Key:          []byte("key"),
			Value:        []byte("value"),
			HLCTimestamp: int64(i * 1000),
			DeviceID:     "device-1",
			Synced:       false,
		}
		_ = logOp(tx, op)
		_ = tx.Commit()
	}

	// Initially should have 3 unsynced
	ops, _ := getUnsyncedOps(db, 10)
	if len(ops) != 3 {
		t.Fatalf("expected 3 unsynced ops initially, got %d", len(ops))
	}

	// Mark first two as synced
	if err := markOpsSynced(db, opIDs[:2]); err != nil {
		t.Fatalf("markOpsSynced failed: %v", err)
	}

	// Should have 1 unsynced now
	ops, _ = getUnsyncedOps(db, 10)
	if len(ops) != 1 {
		t.Errorf("expected 1 unsynced op after marking, got %d", len(ops))
	}
}

func TestGetOpsAfter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert ops with seq 1-5
	for i := 1; i <= 5; i++ {
		tx, _ := db.Begin()
		op := &Op{
			OpID:         newOpID(),
			Seq:          int64(i),
			OpType:       "set",
			Key:          []byte("key"),
			Value:        []byte("value"),
			HLCTimestamp: int64(i * 1000),
			DeviceID:     "device-1",
			Synced:       false,
		}
		_ = logOp(tx, op)
		_ = tx.Commit()
	}

	// Get ops after seq 3
	ops, err := getOpsAfter(db, 3, 10)
	if err != nil {
		t.Fatalf("getOpsAfter failed: %v", err)
	}

	// Should have 2 ops (seq 4, 5)
	if len(ops) != 2 {
		t.Errorf("expected 2 ops after seq 3, got %d", len(ops))
	}

	for _, op := range ops {
		if op.Seq <= 3 {
			t.Errorf("got op with seq %d, expected > 3", op.Seq)
		}
	}
}

func TestGetNextSeq(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Initially should be 1
	seq, err := getNextSeq(db)
	if err != nil {
		t.Fatalf("getNextSeq failed: %v", err)
	}
	if seq != 1 {
		t.Errorf("expected initial seq 1, got %d", seq)
	}

	// Insert an op with seq 5
	tx, _ := db.Begin()
	op := &Op{
		OpID:         newOpID(),
		Seq:          5,
		OpType:       "set",
		Key:          []byte("key"),
		Value:        []byte("value"),
		HLCTimestamp: 1000,
		DeviceID:     "device-1",
		Synced:       false,
	}
	_ = logOp(tx, op)
	_ = tx.Commit()

	// Next seq should be 6
	seq, err = getNextSeq(db)
	if err != nil {
		t.Fatalf("getNextSeq failed: %v", err)
	}
	if seq != 6 {
		t.Errorf("expected seq 6 after inserting 5, got %d", seq)
	}
}

func TestApplyOp_NewOp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	op := &Op{
		OpID:         newOpID(),
		Seq:          1,
		OpType:       "set",
		Key:          []byte("test-key"),
		Value:        []byte("test-value"),
		HLCTimestamp: 1000,
		DeviceID:     "remote-device",
		Synced:       true,
	}

	applied, err := applyOp(db, op)
	if err != nil {
		t.Fatalf("applyOp failed: %v", err)
	}
	if !applied {
		t.Error("expected op to be applied")
	}

	// Verify key was set
	value, err := sqliteGet(db, op.Key)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if string(value) != string(op.Value) {
		t.Errorf("expected value %q, got %q", op.Value, value)
	}
}

func TestApplyOp_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	op := &Op{
		OpID:         newOpID(),
		Seq:          1,
		OpType:       "set",
		Key:          []byte("test-key"),
		Value:        []byte("test-value"),
		HLCTimestamp: 1000,
		DeviceID:     "remote-device",
		Synced:       true,
	}

	// Apply first time
	applied1, err := applyOp(db, op)
	if err != nil {
		t.Fatalf("first applyOp failed: %v", err)
	}
	if !applied1 {
		t.Error("expected first apply to succeed")
	}

	// Apply second time (should be no-op)
	applied2, err := applyOp(db, op)
	if err != nil {
		t.Fatalf("second applyOp failed: %v", err)
	}
	if applied2 {
		t.Error("expected second apply to be no-op")
	}
}

func TestApplyOp_ConflictResolution(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Apply newer op first
	newerOp := &Op{
		OpID:         newOpID(),
		Seq:          2,
		OpType:       "set",
		Key:          []byte("test-key"),
		Value:        []byte("newer-value"),
		HLCTimestamp: 2000,
		DeviceID:     "device-a",
		Synced:       true,
	}
	applied1, err := applyOp(db, newerOp)
	if err != nil {
		t.Fatalf("applyOp newer failed: %v", err)
	}
	if !applied1 {
		t.Error("expected newer op to be applied")
	}

	// Apply older op (should be logged but not applied)
	olderOp := &Op{
		OpID:         newOpID(),
		Seq:          1,
		OpType:       "set",
		Key:          []byte("test-key"),
		Value:        []byte("older-value"),
		HLCTimestamp: 1000,
		DeviceID:     "device-b",
		Synced:       true,
	}
	applied2, err := applyOp(db, olderOp)
	if err != nil {
		t.Fatalf("applyOp older failed: %v", err)
	}
	if applied2 {
		t.Error("expected older op to NOT modify the value")
	}

	// Value should still be from newer op
	value, err := sqliteGet(db, []byte("test-key"))
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if string(value) != "newer-value" {
		t.Errorf("expected 'newer-value', got %q", value)
	}

	// But older op should still be in the log
	exists, _ := hasOp(db, olderOp.OpID)
	if !exists {
		t.Error("older op should still be logged")
	}
}

func TestApplyOp_Delete(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// First set a value
	if err := sqliteSet(db, []byte("key"), []byte("value")); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	// Apply delete op
	op := &Op{
		OpID:         newOpID(),
		Seq:          1,
		OpType:       "delete",
		Key:          []byte("key"),
		Value:        nil,
		HLCTimestamp: 1000,
		DeviceID:     "remote-device",
		Synced:       true,
	}

	applied, err := applyOp(db, op)
	if err != nil {
		t.Fatalf("applyOp failed: %v", err)
	}
	if !applied {
		t.Error("expected delete op to be applied")
	}

	// Verify key was deleted
	_, err = sqliteGet(db, op.Key)
	if err != ErrMissingKey {
		t.Errorf("expected ErrMissingKey, got %v", err)
	}
}

func TestNewOpID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newOpID()
		if seen[id] {
			t.Errorf("duplicate op ID generated: %s", id)
		}
		seen[id] = true
	}
}
