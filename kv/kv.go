// Package kv provides a Charm Cloud backed key-value store.
package kv

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/charm/client"
	"github.com/charmbracelet/charm/fs"
	charm "github.com/charmbracelet/charm/proto"
	"github.com/jacobsa/crypto/siv"
)

// KV provides a Charm Cloud backed key-value store.
//
// KV supports cloud sync, backing up the data to the Charm Cloud.
// It will allow for syncing across machines linked with a Charm account.
// All data is encrypted on the local disk using a Charm user's encryption keys.
// Backups are also encrypted locally before being synced to the Charm Cloud.
type KV struct {
	db       *sql.DB
	dbPath   string
	name     string
	cc       *client.Client
	fs       *fs.FS
	readOnly bool
}

// Config holds optional configuration for opening a KV store.
type Config struct {
	customPath string
}

// Option is a functional option for configuring KV store opening.
type Option func(*Config)

// WithPath sets a custom database path instead of using client.DataPath().
func WithPath(path string) Option {
	return func(c *Config) {
		c.customPath = path
	}
}

// openKV opens a KV store with the given client, name, read-only mode, and options.
func openKV(cc *client.Client, name string, readOnly bool, opts ...Option) (*KV, error) {
	// Apply options
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Get data path
	var dd string
	var err error
	if cfg.customPath != "" {
		dd = cfg.customPath
	} else {
		dd, err = cc.DataPath()
		if err != nil {
			return nil, err
		}
	}

	// Build database path
	kvDir := filepath.Join(dd, "kv")
	if err := os.MkdirAll(kvDir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(kvDir, name+".db")

	// Open SQLite database
	db, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}

	// Create filesystem
	cfs, err := fs.NewFSWithClient(cc)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &KV{
		db:       db,
		dbPath:   dbPath,
		name:     name,
		cc:       cc,
		fs:       cfs,
		readOnly: readOnly,
	}, nil
}

// Open a Charm Cloud managed key-value store.
func Open(cc *client.Client, name string, opts ...Option) (*KV, error) {
	return openKV(cc, name, false, opts...)
}

// OpenWithDefaults opens a Charm Cloud managed key-value store with the
// default settings pulled from environment variables.
func OpenWithDefaults(name string, opts ...Option) (*KV, error) {
	cc, err := client.NewClientWithDefaults()
	if err != nil {
		return nil, err
	}
	return Open(cc, name, opts...)
}

// OpenReadOnly opens a Charm Cloud managed key-value store in read-only mode.
// This allows concurrent access when another process holds the write lock.
// Write operations (Set, Delete) will return ErrReadOnlyMode.
// Cloud sync is disabled in read-only mode.
func OpenReadOnly(cc *client.Client, name string, opts ...Option) (*KV, error) {
	return openKV(cc, name, true, opts...)
}

// OpenWithDefaultsReadOnly opens a Charm Cloud managed key-value store in
// read-only mode with default settings. This is useful when another process
// holds the database lock and you only need to read data.
func OpenWithDefaultsReadOnly(name string, opts ...Option) (*KV, error) {
	cc, err := client.NewClientWithDefaults()
	if err != nil {
		return nil, err
	}
	return OpenReadOnly(cc, name, opts...)
}

// IsReadOnly returns true if this KV instance was opened in read-only mode.
func (kv *KV) IsReadOnly() bool {
	return kv.readOnly
}

// OpenWithFallback opens a Charm Cloud managed key-value store, automatically
// falling back to read-only mode if another process holds the lock.
// Use IsReadOnly() to check which mode was used.
func OpenWithFallback(cc *client.Client, name string, opts ...Option) (*KV, error) {
	kv, err := Open(cc, name, opts...)
	if err == nil {
		return kv, nil
	}
	if IsLocked(err) {
		return OpenReadOnly(cc, name, opts...)
	}
	return nil, err
}

// OpenWithDefaultsFallback opens a Charm Cloud managed key-value store with
// default settings, automatically falling back to read-only mode if another
// process holds the lock. Use IsReadOnly() to check which mode was used.
func OpenWithDefaultsFallback(name string, opts ...Option) (*KV, error) {
	kv, err := OpenWithDefaults(name, opts...)
	if err == nil {
		return kv, nil
	}
	if IsLocked(err) {
		return OpenWithDefaultsReadOnly(name, opts...)
	}
	return nil, err
}

// Sync synchronizes the local database with any updates from the Charm Cloud.
func (kv *KV) Sync() error {
	return kv.syncFrom(kv.maxVersion())
}

// syncAfterWrite performs a cloud sync after a local write operation.
func (kv *KV) syncAfterWrite() error {
	// First sync any remote changes
	mv := kv.maxVersion()
	err := kv.syncFrom(mv)
	if err != nil {
		return err
	}

	// Get next sequence number
	seq, err := kv.nextSeq(kv.name)
	if err != nil {
		return err
	}

	// Update local max version
	if err := kv.setMaxVersion(seq); err != nil {
		return err
	}

	// Always do a full backup
	return kv.backupSeq(0, seq)
}

// maxVersion returns the current max version from the meta table.
func (kv *KV) maxVersion() uint64 {
	val, _ := sqliteGetMeta(kv.db, "max_version")
	return uint64(val)
}

// setMaxVersion updates the max version in the meta table.
func (kv *KV) setMaxVersion(v uint64) error {
	return sqliteSetMeta(kv.db, "max_version", int64(v))
}

// Close closes the underlying database.
func (kv *KV) Close() error {
	return kv.db.Close()
}

// encryptValue encrypts a value using the client's encryption keys.
// Uses deterministic SIV encryption to ensure the same value always encrypts
// to the same ciphertext, matching BadgerDB's security model.
func (kv *KV) encryptValue(value []byte) ([]byte, error) {
	// Get encryption keys from client
	eks, err := kv.cc.EncryptKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to get encryption keys: %w", err)
	}
	if len(eks) == 0 {
		return nil, fmt.Errorf("no encryption keys available")
	}

	// Use first key for encryption (same as crypt package)
	var key *charm.EncryptKey
	key = eks[0]
	if len(key.Key) < 32 {
		return nil, fmt.Errorf("encryption key too short: %d bytes, need 32", len(key.Key))
	}

	// Encrypt using SIV (deterministic encryption)
	ct, err := siv.Encrypt(nil, []byte(key.Key[:32]), value, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt value: %w", err)
	}

	// Return hex-encoded ciphertext
	return []byte(hex.EncodeToString(ct)), nil
}

// decryptValue decrypts a value using the client's encryption keys.
// Tries all available keys to handle key rotation.
func (kv *KV) decryptValue(encValue []byte) ([]byte, error) {
	// Get encryption keys from client
	eks, err := kv.cc.EncryptKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to get encryption keys: %w", err)
	}
	if len(eks) == 0 {
		return nil, fmt.Errorf("no encryption keys available")
	}

	// Decode hex-encoded ciphertext
	ct, err := hex.DecodeString(string(encValue))
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted value: %w", err)
	}

	// Try all keys (for key rotation support)
	var pt []byte
	for _, k := range eks {
		if len(k.Key) < 32 {
			continue
		}
		pt, err = siv.Decrypt([]byte(k.Key[:32]), ct, nil)
		if err == nil {
			break
		}
	}

	if len(pt) == 0 {
		return nil, fmt.Errorf("failed to decrypt value with any available key")
	}

	return pt, nil
}

// Set is a convenience method for setting a key and value.
// Returns ErrReadOnlyMode if the database is open in read-only mode.
func (kv *KV) Set(key []byte, value []byte) error {
	if kv.readOnly {
		return &ErrReadOnlyMode{Operation: "set key"}
	}
	// Encrypt the value before storing
	encValue, err := kv.encryptValue(value)
	if err != nil {
		return err
	}
	if err := sqliteSet(kv.db, key, encValue); err != nil {
		return err
	}
	return kv.syncAfterWrite()
}

// SetReader is a convenience method to set the value for a key to the data
// read from the provided io.Reader.
func (kv *KV) SetReader(key []byte, value io.Reader) error {
	v, err := io.ReadAll(value)
	if err != nil {
		return err
	}
	return kv.Set(key, v)
}

// Get is a convenience method for getting a value from the key value store.
func (kv *KV) Get(key []byte) ([]byte, error) {
	encValue, err := sqliteGet(kv.db, key)
	if err != nil {
		return nil, err
	}
	// Decrypt the value before returning
	return kv.decryptValue(encValue)
}

// Delete is a convenience method for deleting a value from the key value store.
// Returns ErrReadOnlyMode if the database is open in read-only mode.
func (kv *KV) Delete(key []byte) error {
	if kv.readOnly {
		return &ErrReadOnlyMode{Operation: "delete key"}
	}
	if err := sqliteDelete(kv.db, key); err != nil {
		return err
	}
	return kv.syncAfterWrite()
}

// Keys returns a list of all keys for this key value store.
func (kv *KV) Keys() ([][]byte, error) {
	return sqliteKeys(kv.db)
}

// Client returns the underlying *client.Client.
func (kv *KV) Client() *client.Client {
	return kv.cc
}

// Reset deletes the local database and rebuilds with a fresh sync
// from the Charm Cloud.
func (kv *KV) Reset() error {
	dbPath := kv.dbPath
	err := kv.db.Close()
	if err != nil {
		return err
	}

	// Remove database file and WAL files
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	walPath := dbPath + "-wal"
	if err := os.Remove(walPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	shmPath := dbPath + "-shm"
	if err := os.Remove(shmPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Reopen database
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	kv.db = db
	return kv.Sync()
}
