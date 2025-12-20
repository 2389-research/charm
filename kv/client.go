package kv

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
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

	// Close current DB
	if err := kv.db.Close(); err != nil {
		return err
	}

	// Restore from backup
	if err := sqliteRestore(r, kv.dbPath); err != nil {
		return err
	}

	// Reopen DB
	db, err := openSQLite(kv.dbPath)
	if err != nil {
		return err
	}
	kv.db = db
	return nil
}

func (kv *KV) nextSeq(name string) (uint64, error) {
	var sm charm.SeqMsg
	encName, err := kv.fs.EncryptPath(name)
	if err != nil {
		return 0, err
	}
	err = kv.cc.AuthedJSONRequest("POST", fmt.Sprintf("/v1/seq/%s", encName), nil, &sm)
	if err != nil {
		return 0, err
	}
	return sm.Seq, nil
}

func (kv *KV) syncFrom(mv uint64) error {
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

	// Sort by sequence number to ensure backups are applied in order.
	// This is critical for overwrites: if backup 2 overwrites a key from
	// backup 1, we must restore backup 1 first, then backup 2.
	sort.Slice(seqs, func(i, j int) bool {
		return seqs[i] < seqs[j]
	})

	// Track highest sequence restored
	var maxSeq uint64

	// Restore in sequence order
	for _, seq := range seqs {
		err = kv.restoreSeq(seq)
		if err != nil {
			return err
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}

	// Update max_version to reflect the highest sequence we restored
	if maxSeq > 0 {
		if err := kv.setMaxVersion(maxSeq); err != nil {
			return err
		}
	}

	return nil
}
