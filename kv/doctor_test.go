package kv

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDoctor_HealthyDatabase(t *testing.T) {
	// Create a healthy database
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create a minimal KV instance for testing
	kv := &KV{
		db:     db,
		dbPath: dbPath,
	}

	result, err := kv.Doctor()
	if err != nil {
		t.Fatalf("Doctor() returned error: %v", err)
	}

	if !result.IntegrityOK {
		t.Errorf("expected IntegrityOK=true, got false: %s", result.IntegrityDetails)
	}

	if result.PendingOpsCount != 0 {
		t.Errorf("expected PendingOpsCount=0, got %d", result.PendingOpsCount)
	}

	if !result.IsHealthy() {
		t.Errorf("expected IsHealthy()=true, got false. Errors: %v", result.Errors)
	}
}

func TestDoctor_WithPendingOps(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	kv := &KV{
		db:     db,
		dbPath: dbPath,
	}

	// Insert some pending ops
	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		_, err := db.Exec(`INSERT INTO pending_ops (op_type, key, value, created_at)
			VALUES ('set', ?, ?, ?)`, []byte("key"), []byte("value"), now-int64(i*10))
		if err != nil {
			t.Fatalf("failed to insert pending op: %v", err)
		}
	}

	result, err := kv.Doctor()
	if err != nil {
		t.Fatalf("Doctor() returned error: %v", err)
	}

	if result.PendingOpsCount != 5 {
		t.Errorf("expected PendingOpsCount=5, got %d", result.PendingOpsCount)
	}

	if result.OldestPendingOp.IsZero() {
		t.Error("expected OldestPendingOp to be set")
	}

	// Should still be healthy (pending ops are a warning, not an error)
	if !result.IsHealthy() {
		t.Errorf("expected IsHealthy()=true with pending ops, got false. Errors: %v", result.Errors)
	}
}

func TestDoctor_WithSyncLock(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	kv := &KV{
		db:     db,
		dbPath: dbPath,
	}

	// Insert an active sync lock
	now := time.Now().Unix()
	expiresAt := now + 60
	_, err = db.Exec(`INSERT INTO sync_lock (id, holder, acquired_at, expires_at)
		VALUES (1, 'test-holder', ?, ?)`, now, expiresAt)
	if err != nil {
		t.Fatalf("failed to insert sync lock: %v", err)
	}

	result, err := kv.Doctor()
	if err != nil {
		t.Fatalf("Doctor() returned error: %v", err)
	}

	if !result.SyncLockHeld {
		t.Error("expected SyncLockHeld=true")
	}

	if result.SyncLockHolder != "test-holder" {
		t.Errorf("expected SyncLockHolder='test-holder', got %q", result.SyncLockHolder)
	}

	if result.SyncLockExpiresAt.IsZero() {
		t.Error("expected SyncLockExpiresAt to be set")
	}
}

func TestDoctor_ExpiredSyncLock(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	kv := &KV{
		db:     db,
		dbPath: dbPath,
	}

	// Insert an expired sync lock
	now := time.Now().Unix()
	expiresAt := now - 60 // expired
	_, err = db.Exec(`INSERT INTO sync_lock (id, holder, acquired_at, expires_at)
		VALUES (1, 'expired-holder', ?, ?)`, now-120, expiresAt)
	if err != nil {
		t.Fatalf("failed to insert sync lock: %v", err)
	}

	result, err := kv.Doctor()
	if err != nil {
		t.Fatalf("Doctor() returned error: %v", err)
	}

	// Expired lock should not be considered "held"
	if result.SyncLockHeld {
		t.Error("expected SyncLockHeld=false for expired lock")
	}
}

func TestDoctor_WALFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Write something to trigger WAL
	_, err = db.Exec("INSERT INTO kv (key, value) VALUES (?, ?)", []byte("test"), []byte("data"))
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	kv := &KV{
		db:     db,
		dbPath: dbPath,
	}

	result, err := kv.Doctor()
	if err != nil {
		t.Fatalf("Doctor() returned error: %v", err)
	}

	// WAL might or might not exist depending on checkpoint behavior
	// Just verify we don't error
	t.Logf("WALSize: %d, SHMExists: %v", result.WALSize, result.SHMExists)
}

func TestDoctor_String(t *testing.T) {
	result := &DoctorResult{
		IntegrityOK:     true,
		PendingOpsCount: 3,
		LocalSeq:        42,
		WALSize:         1024,
		Warnings:        []string{"test warning"},
	}

	str := result.String()

	// Check that key elements are present
	if !containsSubstring(str, "SQLite integrity: OK") {
		t.Errorf("expected 'SQLite integrity: OK' in output, got: %s", str)
	}
	if !containsSubstring(str, "Pending ops: 3") {
		t.Errorf("expected 'Pending ops: 3' in output, got: %s", str)
	}
	if !containsSubstring(str, "Local seq: 42") {
		t.Errorf("expected 'Local seq: 42' in output, got: %s", str)
	}
	if !containsSubstring(str, "test warning") {
		t.Errorf("expected 'test warning' in output, got: %s", str)
	}
}

func TestDoctor_StringWithErrors(t *testing.T) {
	result := &DoctorResult{
		IntegrityOK:      false,
		IntegrityDetails: "corruption detected",
		Errors:           []string{"database corrupted"},
	}

	str := result.String()

	if !containsSubstring(str, "FAILED") {
		t.Errorf("expected 'FAILED' in output for corrupt database, got: %s", str)
	}
	if !containsSubstring(str, "database corrupted") {
		t.Errorf("expected error message in output, got: %s", str)
	}
}

func TestDoctor_IsHealthy(t *testing.T) {
	tests := []struct {
		name     string
		result   DoctorResult
		expected bool
	}{
		{
			name:     "healthy",
			result:   DoctorResult{IntegrityOK: true},
			expected: true,
		},
		{
			name:     "integrity failed",
			result:   DoctorResult{IntegrityOK: false},
			expected: false,
		},
		{
			name:     "has errors",
			result:   DoctorResult{IntegrityOK: true, Errors: []string{"error"}},
			expected: false,
		},
		{
			name:     "warnings are ok",
			result:   DoctorResult{IntegrityOK: true, Warnings: []string{"warning"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.IsHealthy(); got != tt.expected {
				t.Errorf("IsHealthy() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestDoctor_OldPendingOpsWarning(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	kv := &KV{
		db:     db,
		dbPath: dbPath,
	}

	// Insert pending ops older than 24 hours
	oldTime := time.Now().Add(-48 * time.Hour).Unix()
	_, err = db.Exec(`INSERT INTO pending_ops (op_type, key, value, created_at)
		VALUES ('set', ?, ?, ?)`, []byte("old-key"), []byte("value"), oldTime)
	if err != nil {
		t.Fatalf("failed to insert pending op: %v", err)
	}

	result, err := kv.Doctor()
	if err != nil {
		t.Fatalf("Doctor() returned error: %v", err)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warning for old pending ops")
	}

	foundWarning := false
	for _, w := range result.Warnings {
		if containsSubstring(w, "older than 24h") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected '24h' warning, got: %v", result.Warnings)
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstringHelper(s, substr))
}

func containsSubstringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Verify KV has the necessary fields for Doctor
func TestKV_HasDoctorRequirements(t *testing.T) {
	// This test just verifies the KV struct has the fields Doctor needs
	kv := &KV{}
	_ = kv.db     // *sql.DB
	_ = kv.dbPath // string
	// If this compiles, the fields exist
}

// Silence unused import warning for sql
var _ = sql.ErrNoRows
var _ = os.ErrNotExist
