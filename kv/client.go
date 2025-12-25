package kv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	charm "github.com/charmbracelet/charm/proto"
)

type kvFile struct {
	data *bytes.Buffer
	info *kvFileInfo
}

type kvFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (f *kvFileInfo) Name() string {
	return f.name
}

func (f *kvFileInfo) Size() int64 {
	return f.size
}

func (f *kvFileInfo) Mode() fs.FileMode {
	return f.mode
}

func (f *kvFileInfo) ModTime() time.Time {
	return f.modTime
}

func (f *kvFileInfo) IsDir() bool {
	return f.mode&fs.ModeDir != 0
}

func (f *kvFileInfo) Sys() interface{} {
	return nil
}

func (f *kvFile) Stat() (fs.FileInfo, error) {
	if f.info == nil {
		return nil, fmt.Errorf("file info not set")
	}
	return f.info, nil
}

func (f *kvFile) Close() error {
	return nil
}

func (f *kvFile) Read(p []byte) (n int, err error) {
	return f.data.Read(p)
}

func (kv *KV) seqStorageKey(seq uint64) string {
	return strings.Join([]string{kv.name, fmt.Sprintf("%d", seq)}, "/")
}

func (kv *KV) backupSeq(from uint64, at uint64) error {
	// Use manifest-based backup for idempotent uploads
	return kv.backupWithManifest(at)
}

func (kv *KV) restoreSeq(seq uint64) error {
	// there is never a zero seq
	if seq == 0 {
		return nil
	}

	// Try to find the backup key using manifest first, then fall back to old format
	backupKey, err := kv.findBackupKey(seq)
	if err != nil {
		return err
	}

	r, err := kv.fs.Open(backupKey)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	// Read the backup data into memory first to validate it
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}

	// Validate SQLite magic bytes before restoring.
	// Old BadgerDB backups from before the SQLite migration will fail here.
	if len(data) < len(sqliteMagic) || string(data[:len(sqliteMagic)]) != string(sqliteMagic) {
		// This is an old BadgerDB backup - skip it and clean up from cloud
		_ = kv.fs.Remove(backupKey)
		return ErrNotSQLite
	}

	// Close current DB
	if err := kv.db.Close(); err != nil {
		return err
	}

	// Write the validated SQLite backup
	if err := os.WriteFile(kv.dbPath, data, 0600); err != nil {
		// Try to reopen the original database
		if db, reopenErr := openSQLite(kv.dbPath); reopenErr == nil {
			kv.db = db
		}
		return fmt.Errorf("failed to write backup: %w", err)
	}

	// Reopen DB
	db, err := openSQLite(kv.dbPath)
	if err != nil {
		return err
	}
	kv.db = db
	return nil
}

// findBackupKey finds the storage key for a backup by sequence number.
// Checks manifest first (new format), then falls back to old format.
func (kv *KV) findBackupKey(seq uint64) (string, error) {
	// Try manifest first
	manifest, err := kv.loadManifest()
	if err == nil && len(manifest.Backups) > 0 {
		for _, b := range manifest.Backups {
			if b.Seq == seq {
				return b.StorageKey(kv.name), nil
			}
		}
	}

	// Fall back to old format: {name}/{seq}
	return kv.seqStorageKey(seq), nil
}

func (kv *KV) nextSeqWithContext(ctx context.Context, name string) (uint64, error) {
	var sm charm.SeqMsg
	encName, err := kv.fs.EncryptPath(name)
	if err != nil {
		return 0, err
	}
	err = kv.cc.AuthedJSONRequestWithContext(ctx, "POST", fmt.Sprintf("/v1/seq/%s", encName), nil, &sm)
	if err != nil {
		return 0, err
	}
	return sm.Seq, nil
}

// syncFromWithContext syncs from cloud backups with sequence numbers greater than mv.
//
// IMPORTANT: This is a FULL SNAPSHOT sync mechanism, not incremental.
// Each backup is a complete database snapshot. When multiple backups exist,
// only the LATEST one matters - earlier ones are superseded.
//
// This means: last-write-wins semantics. If device A writes at seq 10 and
// device B writes at seq 11, syncing will result in only seq 11's data.
// This is intentional - each write backs up the full database state.
//
// If old BadgerDB backups are found (from before the SQLite migration),
// they are automatically cleaned up and skipped.
func (kv *KV) syncFromWithContext(ctx context.Context, mv uint64) error {
	// Try manifest-based sync first (new format)
	manifest, manifestErr := kv.loadManifest()
	if manifestErr == nil && manifest.LatestSeq > mv {
		return kv.syncFromManifest(manifest, mv)
	}

	// Fall back to directory scan for backward compatibility with old backups
	return kv.syncFromDirectoryScan(mv)
}

// syncFromManifest syncs using the manifest file (new format).
func (kv *KV) syncFromManifest(manifest *Manifest, mv uint64) error {
	// Get the latest backup that's newer than our version
	latest := manifest.LatestBackup()
	if latest == nil || latest.Seq <= mv {
		return nil // No new backups
	}

	// Restore the latest backup
	if err := kv.restoreSeq(latest.Seq); err != nil {
		if err == ErrNotSQLite {
			// Corrupted backup in manifest - this shouldn't happen with new backups
			// but handle it gracefully
			return nil
		}
		return err
	}

	// Update max_version to reflect the sequence we restored
	if err := kv.setMaxVersion(latest.Seq); err != nil {
		return err
	}

	return nil
}

// syncFromDirectoryScan syncs using directory listing (old format, backward compatible).
func (kv *KV) syncFromDirectoryScan(mv uint64) error {
	seqDir, err := kv.fs.ReadDir(kv.name)
	if err != nil {
		return err
	}

	// Collect sequence numbers to restore
	var seqs []uint64
	for _, de := range seqDir {
		name := de.Name()
		// Skip manifest.json and content-addressed backups (contain '-')
		if name == "manifest.json" || strings.Contains(name, "-") {
			continue
		}
		seq, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			// Skip files that aren't sequence numbers
			continue
		}
		if seq > mv {
			seqs = append(seqs, seq)
		}
	}

	// No new backups to restore
	if len(seqs) == 0 {
		return nil
	}

	// Since each backup is a FULL snapshot, we only need to restore the
	// highest sequence number. Earlier backups are superseded by later ones.
	var maxSeq uint64
	for _, seq := range seqs {
		if seq > maxSeq {
			maxSeq = seq
		}
	}

	// Restore only the latest backup
	if err := kv.restoreSeq(maxSeq); err != nil {
		// If this is an old BadgerDB backup, skip it and clean up all old backups
		if err == ErrNotSQLite {
			// Clean up remaining old backups
			for _, seq := range seqs {
				if seq != maxSeq { // maxSeq was already cleaned in restoreSeq
					_ = kv.fs.Remove(kv.seqStorageKey(seq))
				}
			}
			// Continue with the current (fresh) database
			return nil
		}
		return err
	}

	// Update max_version to reflect the sequence we restored
	if err := kv.setMaxVersion(maxSeq); err != nil {
		return err
	}

	return nil
}
