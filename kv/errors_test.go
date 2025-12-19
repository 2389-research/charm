// ABOUTME: Unit tests for kv/errors.go, covering lock detection and read-only mode errors.
// ABOUTME: Tests verify error messages, Unwrap behavior, and error type detection helpers.
package kv

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrDatabaseLocked_Error(t *testing.T) {
	innerErr := errors.New("Cannot acquire directory lock on '/tmp/test'")
	err := &ErrDatabaseLocked{
		Path: "/tmp/test/kv/mydb",
		Err:  innerErr,
	}

	msg := err.Error()

	// Verify error contains path
	if !contains(msg, "/tmp/test/kv/mydb") {
		t.Errorf("error message should contain path, got: %s", msg)
	}

	// Verify error contains inner error
	if !contains(msg, "Cannot acquire directory lock") {
		t.Errorf("error message should contain inner error, got: %s", msg)
	}

	// Verify helpful suggestions are present
	if !contains(msg, "Another process") {
		t.Errorf("error message should mention another process, got: %s", msg)
	}
	if !contains(msg, "OpenReadOnly()") {
		t.Errorf("error message should suggest OpenReadOnly(), got: %s", msg)
	}
}

func TestErrDatabaseLocked_Unwrap(t *testing.T) {
	innerErr := errors.New("lock error")
	err := &ErrDatabaseLocked{
		Path: "/tmp/test",
		Err:  innerErr,
	}

	unwrapped := err.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, innerErr)
	}

	// Verify errors.Is works
	if !errors.Is(err, innerErr) {
		t.Error("errors.Is should find inner error")
	}
}

func TestErrReadOnlyMode_Error(t *testing.T) {
	tests := []struct {
		operation string
		wantPart  string
	}{
		{"set key", "cannot set key"},
		{"delete key", "cannot delete key"},
		{"create update transaction", "cannot create update transaction"},
	}

	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			err := &ErrReadOnlyMode{Operation: tt.operation}
			msg := err.Error()

			if !contains(msg, tt.wantPart) {
				t.Errorf("error message should contain %q, got: %s", tt.wantPart, msg)
			}

			if !contains(msg, "read-only mode") {
				t.Errorf("error message should mention read-only mode, got: %s", msg)
			}

			if !contains(msg, "stop the other process") {
				t.Errorf("error message should suggest stopping other process, got: %s", msg)
			}
		})
	}
}

func TestIsLocked_WithErrDatabaseLocked(t *testing.T) {
	err := &ErrDatabaseLocked{
		Path: "/tmp/test",
		Err:  errors.New("inner"),
	}

	if !IsLocked(err) {
		t.Error("IsLocked should return true for ErrDatabaseLocked")
	}
}

func TestIsLocked_WithWrappedErrDatabaseLocked(t *testing.T) {
	inner := &ErrDatabaseLocked{
		Path: "/tmp/test",
		Err:  errors.New("inner"),
	}
	wrapped := fmt.Errorf("failed to open: %w", inner)

	if !IsLocked(wrapped) {
		t.Error("IsLocked should return true for wrapped ErrDatabaseLocked")
	}
}

func TestIsLocked_WithLockIndicatorStrings(t *testing.T) {
	tests := []struct {
		name   string
		errMsg string
		want   bool
	}{
		{
			name:   "directory lock error",
			errMsg: "Cannot acquire directory lock on '/tmp/test'",
			want:   true,
		},
		{
			name:   "resource unavailable",
			errMsg: "resource temporarily unavailable",
			want:   true,
		},
		{
			name:   "LOCK file error",
			errMsg: "Cannot open LOCK file",
			want:   true,
		},
		{
			name:   "another process message",
			errMsg: "database is held by another process",
			want:   true,
		},
		{
			name:   "unrelated error",
			errMsg: "file not found",
			want:   false,
		},
		{
			name:   "encryption error",
			errMsg: "encryption key is too short",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.errMsg)
			got := IsLocked(err)
			if got != tt.want {
				t.Errorf("IsLocked(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestIsLocked_WithNilError(t *testing.T) {
	if IsLocked(nil) {
		t.Error("IsLocked(nil) should return false")
	}
}

func TestIsReadOnly_WithErrReadOnlyMode(t *testing.T) {
	err := &ErrReadOnlyMode{Operation: "set key"}

	if !IsReadOnly(err) {
		t.Error("IsReadOnly should return true for ErrReadOnlyMode")
	}
}

func TestIsReadOnly_WithWrappedErrReadOnlyMode(t *testing.T) {
	inner := &ErrReadOnlyMode{Operation: "delete key"}
	wrapped := fmt.Errorf("operation failed: %w", inner)

	if !IsReadOnly(wrapped) {
		t.Error("IsReadOnly should return true for wrapped ErrReadOnlyMode")
	}
}

func TestIsReadOnly_WithOtherErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"nil error", nil},
		{"generic error", errors.New("some error")},
		{"lock error", &ErrDatabaseLocked{Path: "/tmp", Err: errors.New("lock")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsReadOnly(tt.err) {
				t.Errorf("IsReadOnly(%v) should return false", tt.err)
			}
		})
	}
}

// contains is a helper to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
