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
	buf := bytes.NewBuffer(nil)
	err := sqliteBackup(kv.dbPath, buf)
	if err != nil {
		return err
	}
	name := kv.seqStorageKey(at)
	src := &kvFile{
		data: buf,
		info: &kvFileInfo{
			name:    name,
			size:    int64(buf.Len()),
			mode:    fs.FileMode(0o660),
			modTime: time.Now(),
		},
	}
	return kv.fs.WriteFile(name, src)
}

func (kv *KV) restoreSeq(seq uint64) error {
	// there is never a zero seq
	if seq == 0 {
		return nil
	}
	r, err := kv.fs.Open(kv.seqStorageKey(seq))
	if err != nil {
		return err
	}
	defer r.Close() // nolint:errcheck

	// Read the backup data into memory first to validate it
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}

	// Validate SQLite magic bytes before restoring.
	// Old BadgerDB backups from before the SQLite migration will fail here.
	if len(data) < len(sqliteMagic) || string(data[:len(sqliteMagic)]) != string(sqliteMagic) {
		// This is an old BadgerDB backup - skip it and clean up from cloud
		_ = kv.fs.Remove(kv.seqStorageKey(seq))
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
	seqDir, err := kv.fs.ReadDir(kv.name)
	if err != nil {
		return err
	}

	// Collect sequence numbers to restore
	var seqs []uint64
	for _, de := range seqDir {
		seq, err := strconv.ParseUint(de.Name(), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid sequence file name %q: %w", de.Name(), err)
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
