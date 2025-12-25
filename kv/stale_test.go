// ABOUTME: Tests for stale sync functionality.
// ABOUTME: Verifies LastSyncTime, IsStale, and SyncIfStale methods.
package kv

import (
	"testing"
	"time"
)

func TestLastSyncTime_NeverSynced(t *testing.T) {
	// Create a test KV without syncing
	kv := &KV{} // Minimal KV for testing

	lastSync := kv.LastSyncTime()
	if !lastSync.IsZero() {
		t.Errorf("expected zero time for never synced, got %v", lastSync)
	}
}

func TestIsStale_DisabledWithZeroThreshold(t *testing.T) {
	kv := &KV{}

	// With zero threshold, should never be stale
	if kv.IsStale(0) {
		t.Error("expected IsStale(0) to return false (disabled)")
	}
}

func TestIsStale_NeverSyncedIsStale(t *testing.T) {
	kv := &KV{}

	// Never synced should be considered stale
	if !kv.IsStale(time.Hour) {
		t.Error("expected never-synced to be considered stale")
	}
}

func TestDefaultStaleThreshold(t *testing.T) {
	if DefaultStaleThreshold != time.Hour {
		t.Errorf("expected DefaultStaleThreshold to be 1 hour, got %v", DefaultStaleThreshold)
	}
}

func TestMetaLastSyncConstant(t *testing.T) {
	if MetaLastSync != "_meta:last_sync" {
		t.Errorf("expected MetaLastSync to be '_meta:last_sync', got %s", MetaLastSync)
	}
}
