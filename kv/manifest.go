// ABOUTME: Manifest file for tracking backup metadata
// ABOUTME: Enables idempotent uploads and better backup coordination

package kv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"time"
)

// ManifestVersion is the current manifest format version.
const ManifestVersion = 1

// Manifest tracks all backups for a KV database.
type Manifest struct {
	// Version is the manifest format version.
	Version int `json:"version"`

	// LatestSeq is the highest sequence number with a successful backup.
	LatestSeq uint64 `json:"latest_seq"`

	// Backups is a list of all known backups, sorted by sequence number descending.
	Backups []BackupEntry `json:"backups"`
}

// BackupEntry describes a single backup.
type BackupEntry struct {
	// Seq is the sequence number for this backup.
	Seq uint64 `json:"seq"`

	// Hash is the SHA-256 hash prefix (128 bits = 32 hex chars) of the backup content.
	Hash string `json:"hash"`

	// CreatedAt is when this backup was created.
	CreatedAt time.Time `json:"created_at"`

	// DeviceID identifies which device created this backup.
	DeviceID string `json:"device_id,omitempty"`
}

// StorageKey returns the storage path for this backup.
func (e *BackupEntry) StorageKey(name string) string {
	return fmt.Sprintf("%s/%d-%s", name, e.Seq, e.Hash)
}

// manifestKey returns the storage path for the manifest file.
func manifestKey(name string) string {
	return fmt.Sprintf("%s/manifest.json", name)
}

// newManifest creates an empty manifest.
func newManifest() *Manifest {
	return &Manifest{
		Version: ManifestVersion,
		Backups: []BackupEntry{},
	}
}

// AddBackup adds a new backup entry to the manifest.
// Maintains backups sorted by sequence number descending.
func (m *Manifest) AddBackup(entry BackupEntry) {
	// Check if we already have this exact backup (idempotent)
	for _, existing := range m.Backups {
		if existing.Seq == entry.Seq && existing.Hash == entry.Hash {
			return // Already exists, no-op
		}
	}

	m.Backups = append(m.Backups, entry)

	// Sort by sequence descending (newest first)
	sort.Slice(m.Backups, func(i, j int) bool {
		return m.Backups[i].Seq > m.Backups[j].Seq
	})

	// Update latest seq
	if entry.Seq > m.LatestSeq {
		m.LatestSeq = entry.Seq
	}
}

// LatestBackup returns the most recent backup, or nil if none exist.
func (m *Manifest) LatestBackup() *BackupEntry {
	if len(m.Backups) == 0 {
		return nil
	}
	return &m.Backups[0]
}

// BackupsAfter returns all backups with sequence numbers greater than seq.
func (m *Manifest) BackupsAfter(seq uint64) []BackupEntry {
	var result []BackupEntry
	for _, b := range m.Backups {
		if b.Seq > seq {
			result = append(result, b)
		}
	}
	return result
}

// MarshalJSON serializes the manifest to JSON.
func (m *Manifest) MarshalJSON() ([]byte, error) {
	type manifestAlias Manifest
	return json.MarshalIndent((*manifestAlias)(m), "", "  ")
}

// UnmarshalManifest parses a manifest from JSON.
func UnmarshalManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Validate version
	if m.Version > ManifestVersion {
		return nil, fmt.Errorf("manifest version %d is newer than supported version %d", m.Version, ManifestVersion)
	}

	return &m, nil
}

// contentHash computes a SHA-256 hash prefix for content addressing.
// Returns a 32-character hex string (128 bits).
func contentHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:16]) // 128-bit prefix
}

// loadManifest reads the manifest from cloud storage.
// Returns a new empty manifest if none exists.
func (kv *KV) loadManifest() (*Manifest, error) {
	key := manifestKey(kv.name)

	r, err := kv.fs.Open(key)
	if err != nil {
		// If manifest doesn't exist, return empty manifest
		// This handles migration from old backup format
		return newManifest(), nil
	}
	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	return UnmarshalManifest(data)
}

// saveManifest writes the manifest to cloud storage.
func (kv *KV) saveManifest(m *Manifest) error {
	data, err := m.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	key := manifestKey(kv.name)
	src := &kvFile{
		data: bytes.NewBuffer(data),
		info: &kvFileInfo{
			name:    key,
			size:    int64(len(data)),
			mode:    fs.FileMode(0o660),
			modTime: time.Now(),
		},
	}

	return kv.fs.WriteFile(key, src)
}

// backupWithManifest creates a content-addressed backup and updates the manifest.
// This is idempotent - uploading the same content twice is safe.
func (kv *KV) backupWithManifest(seq uint64) error {
	// Create the backup
	buf := bytes.NewBuffer(nil)
	if err := sqliteBackup(kv.dbPath, buf); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	backupData := buf.Bytes()
	hash := contentHash(backupData)

	// Create backup entry
	entry := BackupEntry{
		Seq:       seq,
		Hash:      hash,
		CreatedAt: time.Now().UTC(),
		DeviceID:  kv.deviceID(),
	}

	// Upload backup with content-addressed key
	// This is idempotent - same content = same key
	backupKey := entry.StorageKey(kv.name)
	src := &kvFile{
		data: bytes.NewBuffer(backupData),
		info: &kvFileInfo{
			name:    backupKey,
			size:    int64(len(backupData)),
			mode:    fs.FileMode(0o660),
			modTime: time.Now(),
		},
	}
	if err := kv.fs.WriteFile(backupKey, src); err != nil {
		return fmt.Errorf("failed to upload backup: %w", err)
	}

	// Load current manifest
	manifest, err := kv.loadManifest()
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	// Add our backup
	manifest.AddBackup(entry)

	// Save updated manifest
	if err := kv.saveManifest(manifest); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	return nil
}

// deviceID returns a stable identifier for this device/instance.
// Used for debugging which device created a backup.
func (kv *KV) deviceID() string {
	// Use the charm user ID if available, otherwise empty
	if kv.cc != nil {
		if user, err := kv.cc.Auth(); err == nil {
			return user.ID
		}
	}
	return ""
}
