// ABOUTME: Durable pending operations tracking for sync reliability
// ABOUTME: Tracks writes in SQLite so they survive process restarts

package kv

import (
	"database/sql"
	"fmt"
	"time"
)

// PendingOp represents a write operation waiting to be synced.
type PendingOp struct {
	ID        int64
	OpType    string // "set" or "delete"
	Key       []byte
	Value     []byte // nil for deletes
	CreatedAt time.Time
}

// recordPendingOp logs a write operation to the pending_ops table.
// This is called within the same transaction as the actual write.
// opType must be "set" or "delete".
func recordPendingOp(tx *sql.Tx, opType string, key, value []byte) error {
	// Validate opType before SQL to provide clear error messages
	if opType != "set" && opType != "delete" {
		return fmt.Errorf("invalid pending op type: %q (must be 'set' or 'delete')", opType)
	}

	_, err := tx.Exec(`
		INSERT INTO pending_ops (op_type, key, value, created_at)
		VALUES (?, ?, ?, ?)
	`, opType, key, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("failed to record pending op: %w", err)
	}
	return nil
}

// countPendingOps returns the number of pending operations.
func countPendingOps(db *sql.DB) (int64, error) {
	var count int64
	err := db.QueryRow(`SELECT COUNT(*) FROM pending_ops`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count pending ops: %w", err)
	}
	return count, nil
}

// clearPendingOps removes all pending operations after a successful sync.
func clearPendingOps(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM pending_ops`)
	if err != nil {
		return fmt.Errorf("failed to clear pending ops: %w", err)
	}
	return nil
}

// getPendingOps retrieves all pending operations in order.
//
//nolint:unused // Reserved for Phase 3 op-log sync implementation
func getPendingOps(db *sql.DB) ([]PendingOp, error) {
	rows, err := db.Query(`
		SELECT id, op_type, key, value, created_at
		FROM pending_ops
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending ops: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ops []PendingOp
	for rows.Next() {
		var op PendingOp
		var createdAtUnix int64
		if err := rows.Scan(&op.ID, &op.OpType, &op.Key, &op.Value, &createdAtUnix); err != nil {
			return nil, fmt.Errorf("failed to scan pending op: %w", err)
		}
		op.CreatedAt = time.Unix(createdAtUnix, 0)
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pending ops: %w", err)
	}
	return ops, nil
}

// hasPendingOps returns true if there are any pending operations.
func hasPendingOps(db *sql.DB) (bool, error) {
	count, err := countPendingOps(db)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
