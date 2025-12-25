package kv

import (
	"testing"
	"time"
)

func TestContentHash(t *testing.T) {
	// Same content should produce same hash
	data := []byte("test data for hashing")
	hash1 := contentHash(data)
	hash2 := contentHash(data)

	if hash1 != hash2 {
		t.Errorf("same content produced different hashes: %s vs %s", hash1, hash2)
	}

	// Hash should be 32 hex chars (128 bits)
	if len(hash1) != 32 {
		t.Errorf("expected 32 char hash, got %d: %s", len(hash1), hash1)
	}

	// Different content should produce different hash
	hash3 := contentHash([]byte("different data"))
	if hash1 == hash3 {
		t.Errorf("different content produced same hash")
	}
}

func TestBackupEntry_StorageKey(t *testing.T) {
	entry := BackupEntry{
		Seq:  42,
		Hash: "abc123def456abc123def456abc123de",
	}

	key := entry.StorageKey("mydb")
	expected := "mydb/42-abc123def456abc123def456abc123de"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestManifestKey(t *testing.T) {
	key := manifestKey("testdb")
	expected := "testdb/manifest.json"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestManifest_AddBackup(t *testing.T) {
	m := newManifest()

	// Add first backup
	m.AddBackup(BackupEntry{Seq: 1, Hash: "hash1", CreatedAt: time.Now()})

	if len(m.Backups) != 1 {
		t.Errorf("expected 1 backup, got %d", len(m.Backups))
	}
	if m.LatestSeq != 1 {
		t.Errorf("expected LatestSeq=1, got %d", m.LatestSeq)
	}

	// Add second backup with higher seq
	m.AddBackup(BackupEntry{Seq: 5, Hash: "hash5", CreatedAt: time.Now()})

	if len(m.Backups) != 2 {
		t.Errorf("expected 2 backups, got %d", len(m.Backups))
	}
	if m.LatestSeq != 5 {
		t.Errorf("expected LatestSeq=5, got %d", m.LatestSeq)
	}

	// Backups should be sorted descending
	if m.Backups[0].Seq != 5 || m.Backups[1].Seq != 1 {
		t.Errorf("backups not sorted correctly: %v", m.Backups)
	}
}

func TestManifest_AddBackup_Idempotent(t *testing.T) {
	m := newManifest()

	entry := BackupEntry{Seq: 1, Hash: "hash1", CreatedAt: time.Now()}

	// Add same entry twice
	m.AddBackup(entry)
	m.AddBackup(entry)

	// Should only have one entry
	if len(m.Backups) != 1 {
		t.Errorf("expected 1 backup (idempotent), got %d", len(m.Backups))
	}
}

func TestManifest_LatestBackup(t *testing.T) {
	m := newManifest()

	// Empty manifest
	if m.LatestBackup() != nil {
		t.Error("expected nil for empty manifest")
	}

	// Add backups
	m.AddBackup(BackupEntry{Seq: 3, Hash: "hash3"})
	m.AddBackup(BackupEntry{Seq: 1, Hash: "hash1"})
	m.AddBackup(BackupEntry{Seq: 5, Hash: "hash5"})

	latest := m.LatestBackup()
	if latest == nil {
		t.Fatal("expected non-nil latest backup")
	}
	if latest.Seq != 5 {
		t.Errorf("expected Seq=5, got %d", latest.Seq)
	}
}

func TestManifest_BackupsAfter(t *testing.T) {
	m := newManifest()
	m.AddBackup(BackupEntry{Seq: 1, Hash: "hash1"})
	m.AddBackup(BackupEntry{Seq: 3, Hash: "hash3"})
	m.AddBackup(BackupEntry{Seq: 5, Hash: "hash5"})
	m.AddBackup(BackupEntry{Seq: 7, Hash: "hash7"})

	after := m.BackupsAfter(3)

	if len(after) != 2 {
		t.Errorf("expected 2 backups after seq 3, got %d", len(after))
	}

	// Should only include seq 5 and 7
	for _, b := range after {
		if b.Seq <= 3 {
			t.Errorf("backup %d should not be included (after 3)", b.Seq)
		}
	}
}

func TestManifest_JSON(t *testing.T) {
	m := newManifest()
	m.AddBackup(BackupEntry{
		Seq:       42,
		Hash:      "abc123",
		CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		DeviceID:  "device1",
	})

	// Marshal
	data, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	// Unmarshal
	m2, err := UnmarshalManifest(data)
	if err != nil {
		t.Fatalf("UnmarshalManifest failed: %v", err)
	}

	if m2.Version != ManifestVersion {
		t.Errorf("expected version %d, got %d", ManifestVersion, m2.Version)
	}
	if m2.LatestSeq != 42 {
		t.Errorf("expected LatestSeq=42, got %d", m2.LatestSeq)
	}
	if len(m2.Backups) != 1 {
		t.Errorf("expected 1 backup, got %d", len(m2.Backups))
	}
	if m2.Backups[0].DeviceID != "device1" {
		t.Errorf("expected DeviceID='device1', got %q", m2.Backups[0].DeviceID)
	}
}

func TestUnmarshalManifest_InvalidVersion(t *testing.T) {
	// Future version should fail
	data := []byte(`{"version": 999, "latest_seq": 1, "backups": []}`)

	_, err := UnmarshalManifest(data)
	if err == nil {
		t.Error("expected error for future version")
	}
}

func TestUnmarshalManifest_InvalidJSON(t *testing.T) {
	_, err := UnmarshalManifest([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestNewManifest(t *testing.T) {
	m := newManifest()

	if m.Version != ManifestVersion {
		t.Errorf("expected version %d, got %d", ManifestVersion, m.Version)
	}
	if m.LatestSeq != 0 {
		t.Errorf("expected LatestSeq=0, got %d", m.LatestSeq)
	}
	if m.Backups == nil {
		t.Error("expected non-nil Backups slice")
	}
	if len(m.Backups) != 0 {
		t.Errorf("expected empty Backups, got %d", len(m.Backups))
	}
}
