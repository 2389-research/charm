// ABOUTME: Tests for the transactional Do/DoReadOnly API
// ABOUTME: Ensures short-lived connections work correctly for MCP servers

package kv

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// uniqueTestDBName generates a unique database name to avoid Charm Cloud sync conflicts.
func uniqueTestDBName(base string) string {
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

// TestDo verifies the Do function opens, executes, and closes properly.
func TestDo(t *testing.T) {
	dir := t.TempDir()
	dbName := uniqueTestDBName("do-basic")

	// Create directory structure
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	// Use Do to set a value
	err := Do(dbName, func(kv *KV) error {
		return kv.Set([]byte("key1"), []byte("value1"))
	}, WithPath(dir))

	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}

	// Use Do again to read the value (verifies data persists)
	var retrieved []byte
	err = Do(dbName, func(kv *KV) error {
		var getErr error
		retrieved, getErr = kv.Get([]byte("key1"))
		return getErr
	}, WithPath(dir))

	if err != nil {
		t.Fatalf("Do (read) failed: %v", err)
	}

	if string(retrieved) != "value1" {
		t.Errorf("expected 'value1', got '%s'", retrieved)
	}
}

// TestDoError verifies errors from the callback are propagated.
func TestDoError(t *testing.T) {
	dir := t.TempDir()
	dbName := uniqueTestDBName("do-error")

	expectedErr := errors.New("intentional error")

	err := Do(dbName, func(kv *KV) error {
		return expectedErr
	}, WithPath(dir))

	if err != expectedErr {
		t.Errorf("expected error to be propagated, got: %v", err)
	}
}

// TestDoReadOnly verifies DoReadOnly opens in read-only mode.
func TestDoReadOnly(t *testing.T) {
	dir := t.TempDir()
	dbName := uniqueTestDBName("do-readonly")

	// First, create some data with Do
	err := Do(dbName, func(kv *KV) error {
		return kv.Set([]byte("key1"), []byte("value1"))
	}, WithPath(dir))
	if err != nil {
		t.Fatalf("setup Do failed: %v", err)
	}

	// Use DoReadOnly to read (should work)
	var retrieved []byte
	err = DoReadOnly(dbName, func(kv *KV) error {
		if !kv.IsReadOnly() {
			return errors.New("expected read-only mode")
		}
		var getErr error
		retrieved, getErr = kv.Get([]byte("key1"))
		return getErr
	}, WithPath(dir))

	if err != nil {
		t.Fatalf("DoReadOnly failed: %v", err)
	}

	if string(retrieved) != "value1" {
		t.Errorf("expected 'value1', got '%s'", retrieved)
	}
}

// TestDoReadOnlyWriteFails verifies writes fail in read-only mode.
func TestDoReadOnlyWriteFails(t *testing.T) {
	dir := t.TempDir()
	dbName := uniqueTestDBName("do-readonly-write")

	// First create the database
	err := Do(dbName, func(kv *KV) error {
		return kv.Set([]byte("key1"), []byte("value1"))
	}, WithPath(dir))
	if err != nil {
		t.Fatalf("setup Do failed: %v", err)
	}

	// Try to write via DoReadOnly (should fail)
	err = DoReadOnly(dbName, func(kv *KV) error {
		return kv.Set([]byte("key2"), []byte("value2"))
	}, WithPath(dir))

	if err == nil {
		t.Error("expected write to fail in read-only mode")
	}

	if !IsReadOnly(err) {
		t.Errorf("expected read-only error, got: %v", err)
	}
}

// TestDoConnectionRelease verifies connections are released after Do completes.
func TestDoConnectionRelease(t *testing.T) {
	dir := t.TempDir()
	dbName := uniqueTestDBName("do-connection")

	// Do multiple sequential Do calls - all should succeed
	// (verifies connection is properly released)
	for i := 0; i < 5; i++ {
		err := Do(dbName, func(kv *KV) error {
			return kv.Set([]byte("key"), []byte("value"))
		}, WithPath(dir))
		if err != nil {
			t.Fatalf("Do iteration %d failed: %v", i, err)
		}
	}
}

// TestDoWithFallback verifies DoWithFallback behavior.
func TestDoWithFallback(t *testing.T) {
	dir := t.TempDir()
	dbName := uniqueTestDBName("do-fallback")

	// First, create some data
	err := Do(dbName, func(kv *KV) error {
		return kv.Set([]byte("key1"), []byte("value1"))
	}, WithPath(dir))
	if err != nil {
		t.Fatalf("setup Do failed: %v", err)
	}

	// DoWithFallback should get write access (no contention in this test)
	var gotReadOnly bool
	err = DoWithFallback(dbName, func(kv *KV) error {
		gotReadOnly = kv.IsReadOnly()
		return nil
	}, WithPath(dir))

	if err != nil {
		t.Fatalf("DoWithFallback failed: %v", err)
	}

	// Should have write access since nothing else is holding the lock
	if gotReadOnly {
		t.Error("expected write access, got read-only")
	}
}
