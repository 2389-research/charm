// ABOUTME: Op-log implementation for incremental sync
// ABOUTME: Records write operations for efficient replication and conflict resolution

package kv

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Op represents a single write operation in the op-log.
type Op struct {
	// OpID is a UUID that uniquely identifies this operation.
	// Used for idempotency - applying the same op twice is a no-op.
	OpID string `json:"op_id"`

	// Seq is the local sequence number when this op was created.
	Seq int64 `json:"seq"`

	// OpType is "set" or "delete".
	OpType string `json:"op_type"`

	// Key is the key being modified.
	Key []byte `json:"key"`

	// Value is the new value (nil for delete operations).
	Value []byte `json:"value,omitempty"`

	// HLCTimestamp is the hybrid logical clock timestamp.
	// Used for ordering and conflict resolution.
	HLCTimestamp int64 `json:"hlc_timestamp"`

	// DeviceID identifies which device created this operation.
	DeviceID string `json:"device_id"`

	// Synced indicates if this op has been synced to the server.
	Synced bool `json:"synced"`
}

// logOp records an operation in the op_log table.
func logOp(tx *sql.Tx, op *Op) error {
	_, err := tx.Exec(`
		INSERT INTO op_log (op_id, seq, op_type, key, value, hlc_timestamp, device_id, synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, op.OpID, op.Seq, op.OpType, op.Key, op.Value, op.HLCTimestamp, op.DeviceID, boolToInt(op.Synced))
	if err != nil {
		return fmt.Errorf("failed to log op: %w", err)
	}
	return nil
}

// hasOp checks if an operation with the given ID already exists.
// Used for idempotency checks.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func hasOp(db *sql.DB, opID string) (bool, error) {
	var exists int
	err := db.QueryRow("SELECT 1 FROM op_log WHERE op_id = ?", opID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check op: %w", err)
	}
	return true, nil
}

// getUnsyncedOps returns all ops from op_log that haven't been synced yet.
// Ops are returned in sequence order.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func getUnsyncedOps(db *sql.DB, limit int) ([]Op, error) {
	rows, err := db.Query(`
		SELECT op_id, seq, op_type, key, value, hlc_timestamp, device_id, synced
		FROM op_log
		WHERE synced = 0
		ORDER BY seq ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query unsynced ops: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanOps(rows)
}

// getOpsAfter returns all ops after the given sequence number.
// Used for incremental sync.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func getOpsAfter(db *sql.DB, afterSeq int64, limit int) ([]Op, error) {
	rows, err := db.Query(`
		SELECT op_id, seq, op_type, key, value, hlc_timestamp, device_id, synced
		FROM op_log
		WHERE seq > ?
		ORDER BY seq ASC
		LIMIT ?
	`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query ops: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanOps(rows)
}

// markOpsSynced marks the given ops as synced.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func markOpsSynced(db *sql.DB, opIDs []string) error {
	if len(opIDs) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	stmt, err := tx.Prepare("UPDATE op_log SET synced = 1 WHERE op_id = ?")
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, opID := range opIDs {
		if _, err := stmt.Exec(opID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to mark op synced: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	return nil
}

// getLatestHLCForKey returns the latest HLC timestamp for a key.
// Returns 0 if no ops exist for the key.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func getLatestHLCForKey(db *sql.DB, key []byte) (int64, error) {
	var hlc sql.NullInt64
	err := db.QueryRow(`
		SELECT MAX(hlc_timestamp) FROM op_log WHERE key = ?
	`, key).Scan(&hlc)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get latest HLC: %w", err)
	}
	if !hlc.Valid {
		return 0, nil
	}
	return hlc.Int64, nil
}

// getNextSeq returns the next local sequence number.
func getNextSeq(db *sql.DB) (int64, error) {
	var maxSeq sql.NullInt64
	err := db.QueryRow("SELECT MAX(seq) FROM op_log").Scan(&maxSeq)
	if err != nil {
		return 0, fmt.Errorf("failed to get max seq: %w", err)
	}
	if !maxSeq.Valid {
		return 1, nil
	}
	return maxSeq.Int64 + 1, nil
}

// applyOp applies a remote operation to the local database.
// Uses last-write-wins conflict resolution based on HLC timestamp.
// Returns true if the operation was applied, false if it was superseded.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func applyOp(db *sql.DB, op *Op) (bool, error) {
	// Check if we already have this op (idempotency)
	exists, err := hasOp(db, op.OpID)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil // Already applied, no-op
	}

	// Check if there's a newer op for this key
	latestHLC, err := getLatestHLCForKey(db, op.Key)
	if err != nil {
		return false, err
	}

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Always log the op for history
	if err := logOp(tx, op); err != nil {
		_ = tx.Rollback()
		return false, err
	}

	// Only apply if this op is newer than existing
	if op.HLCTimestamp > latestHLC || latestHLC == 0 {
		// Apply the operation
		if op.OpType == "set" {
			if _, err := tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", op.Key, op.Value); err != nil {
				_ = tx.Rollback()
				return false, fmt.Errorf("failed to apply set: %w", err)
			}
		} else if op.OpType == "delete" {
			if _, err := tx.Exec("DELETE FROM kv WHERE key = ?", op.Key); err != nil {
				_ = tx.Rollback()
				return false, fmt.Errorf("failed to apply delete: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit: %w", err)
	}

	// Return true if we actually modified the KV store
	return op.HLCTimestamp > latestHLC || latestHLC == 0, nil
}

// newOpID generates a new unique operation ID.
func newOpID() string {
	return uuid.New().String()
}

// scanOps scans rows into Op structs.
//
//nolint:unused // Reserved for Phase 3 incremental sync implementation
func scanOps(rows *sql.Rows) ([]Op, error) {
	var ops []Op
	for rows.Next() {
		var op Op
		var syncedInt int
		if err := rows.Scan(&op.OpID, &op.Seq, &op.OpType, &op.Key, &op.Value, &op.HLCTimestamp, &op.DeviceID, &syncedInt); err != nil {
			return nil, fmt.Errorf("failed to scan op: %w", err)
		}
		op.Synced = syncedInt == 1
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating ops: %w", err)
	}
	return ops, nil
}

// boolToInt converts a bool to int for SQLite.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
