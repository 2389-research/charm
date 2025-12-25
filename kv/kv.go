// Package kv provides a Charm Cloud backed key-value store.
package kv

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

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

	// Backup batching state
	backupMu      sync.Mutex
	pendingWrites int
	shutdown      chan struct{}
	shutdownOnce  sync.Once

	// Op-log state for Phase 3 incremental sync
	hlc        *HLC   // Hybrid logical clock for ordering
	localDevID string // Stable device identifier
}

// Config holds optional configuration for opening a KV store.
type Config struct {
	customPath string

	// Retry settings for write lock acquisition
	writeRetryAttempts  int           // Number of retries (0 = no retry)
	writeRetryBaseDelay time.Duration // Initial delay between retries
	writeRetryMaxDelay  time.Duration // Maximum delay cap
	retryConfigured     bool          // True if retry was explicitly configured
}

// Default retry settings
const (
	DefaultWriteRetryAttempts  = 3
	DefaultWriteRetryBaseDelay = 50 * time.Millisecond
	DefaultWriteRetryMaxDelay  = 500 * time.Millisecond
)

// Backup strategy constants
const (
	// Backup after this many writes have accumulated
	backupWriteThreshold = 10
)

// Option is a functional option for configuring KV store opening.
type Option func(*Config)

// WithPath sets a custom database path instead of using client.DataPath().
func WithPath(path string) Option {
	return func(c *Config) {
		c.customPath = path
	}
}

// WithWriteRetry configures retry behavior for acquiring write locks.
// attempts is the number of retries (0 = no retry, just fail or fallback).
// baseDelay is the initial delay between retries (doubles each attempt).
// maxDelay caps the maximum delay between retries.
func WithWriteRetry(attempts int, baseDelay, maxDelay time.Duration) Option {
	return func(c *Config) {
		c.writeRetryAttempts = attempts
		c.writeRetryBaseDelay = baseDelay
		c.writeRetryMaxDelay = maxDelay
		c.retryConfigured = true
	}
}

// WithNoWriteRetry disables retry behavior, falling back to read-only immediately.
// This preserves the pre-v0.18 behavior.
func WithNoWriteRetry() Option {
	return func(c *Config) {
		c.writeRetryAttempts = 0
		c.writeRetryBaseDelay = 0
		c.writeRetryMaxDelay = 0
		c.retryConfigured = true
	}
}

// applyRetryDefaults sets default retry values if not explicitly configured.
func applyRetryDefaults(cfg *Config) {
	if !cfg.retryConfigured {
		cfg.writeRetryAttempts = DefaultWriteRetryAttempts
		cfg.writeRetryBaseDelay = DefaultWriteRetryBaseDelay
		cfg.writeRetryMaxDelay = DefaultWriteRetryMaxDelay
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

	// Get device ID for op-log (use charm user ID if available)
	var devID string
	if user, err := cc.Auth(); err == nil && user != nil {
		devID = user.ID
	}

	kv := &KV{
		db:         db,
		dbPath:     dbPath,
		name:       name,
		cc:         cc,
		fs:         cfs,
		readOnly:   readOnly,
		shutdown:   make(chan struct{}),
		hlc:        NewHLC(),
		localDevID: devID,
	}

	return kv, nil
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
//
// By default, retries acquiring write access with exponential backoff before
// falling back to read-only. Use WithNoWriteRetry() to disable retry behavior.
func OpenWithFallback(cc *client.Client, name string, opts ...Option) (*KV, error) {
	// Parse config for retry settings
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}
	applyRetryDefaults(cfg)

	var lastErr error
	delay := cfg.writeRetryBaseDelay

	for attempt := 0; attempt <= cfg.writeRetryAttempts; attempt++ {
		kv, err := Open(cc, name, opts...)
		if err == nil {
			return kv, nil
		}

		if !IsLocked(err) {
			return nil, err // Non-lock error, fail immediately
		}

		lastErr = err
		if attempt < cfg.writeRetryAttempts {
			time.Sleep(delay)
			delay = min(delay*2, cfg.writeRetryMaxDelay)
		}
	}

	// All retries exhausted, fall back to read-only
	_ = lastErr // Acknowledge we're falling back due to lock
	return OpenReadOnly(cc, name, opts...)
}

// OpenWithDefaultsFallback opens a Charm Cloud managed key-value store with
// default settings, automatically falling back to read-only mode if another
// process holds the lock. Use IsReadOnly() to check which mode was used.
//
// By default, retries acquiring write access with exponential backoff before
// falling back to read-only. Use WithNoWriteRetry() to disable retry behavior.
func OpenWithDefaultsFallback(name string, opts ...Option) (*KV, error) {
	// Parse config for retry settings
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}
	applyRetryDefaults(cfg)

	var lastErr error
	delay := cfg.writeRetryBaseDelay

	for attempt := 0; attempt <= cfg.writeRetryAttempts; attempt++ {
		kv, err := OpenWithDefaults(name, opts...)
		if err == nil {
			return kv, nil
		}

		if !IsLocked(err) {
			return nil, err // Non-lock error, fail immediately
		}

		lastErr = err
		if attempt < cfg.writeRetryAttempts {
			time.Sleep(delay)
			delay = min(delay*2, cfg.writeRetryMaxDelay)
		}
	}

	// All retries exhausted, fall back to read-only
	_ = lastErr // Acknowledge we're falling back due to lock
	return OpenWithDefaultsReadOnly(name, opts...)
}

// Sync synchronizes the local database with any updates from the Charm Cloud.
// This also flushes any pending writes to ensure they're backed up.
func (kv *KV) Sync() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return kv.SyncWithContext(ctx)
}

// SyncWithContext synchronizes the local database with any updates from the Charm Cloud with context.
// This also flushes any pending writes to ensure they're backed up.
// Uses a sync lock to prevent concurrent Sync() calls from racing.
func (kv *KV) SyncWithContext(ctx context.Context) error {
	// Acquire sync lock to prevent concurrent sync operations.
	// This is important for cross-process safety.
	return withSyncLock(kv.db, func() error {
		return kv.syncWithContextLocked(ctx)
	})
}

// syncWithContextLocked performs the actual sync work (must be called with sync lock held).
func (kv *KV) syncWithContextLocked(ctx context.Context) error {
	// Check both in-memory counter and durable pending_ops table.
	// In-memory catches writes from this session, pending_ops catches any
	// that might have survived a crash.
	kv.backupMu.Lock()
	hasPendingInMemory := kv.pendingWrites > 0
	if hasPendingInMemory {
		kv.pendingWrites = 0
	}
	kv.backupMu.Unlock()

	hasPendingDurable, err := hasPendingOps(kv.db)
	if err != nil {
		return fmt.Errorf("failed to check pending ops: %w", err)
	}

	hasPendingWrites := hasPendingInMemory || hasPendingDurable

	// If we had pending writes, perform a backup now
	if hasPendingWrites && !kv.readOnly {
		if err := kv.performBackupWithContext(ctx); err != nil {
			return err
		}
		// Clear pending ops after successful backup
		if err := clearPendingOps(kv.db); err != nil {
			return fmt.Errorf("failed to clear pending ops: %w", err)
		}
	}

	// Then sync from cloud
	return kv.syncFromWithContext(ctx, kv.maxVersion())
}

// syncAfterWrite tracks writes and triggers backup when threshold is reached.
// Instead of backing up on every write, this batches writes and only syncs
// when backupWriteThreshold is reached. This dramatically improves write
// performance while maintaining safety through explicit Sync() calls.
func (kv *KV) syncAfterWrite() error {
	kv.backupMu.Lock()
	kv.pendingWrites++
	shouldBackup := kv.pendingWrites >= backupWriteThreshold
	if shouldBackup {
		kv.pendingWrites = 0
	}
	kv.backupMu.Unlock()

	// Backup synchronously when threshold is reached
	if shouldBackup {
		return kv.performBackup()
	}

	return nil
}

// performBackup executes the actual backup operation.
// This syncs from cloud, gets a new sequence number, and backs up the database.
func (kv *KV) performBackup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return kv.performBackupWithContext(ctx)
}

// performBackupWithContext executes the actual backup operation with context.
// This syncs from cloud, gets a new sequence number, and backs up the database.
func (kv *KV) performBackupWithContext(ctx context.Context) error {
	return kv.doBackup(ctx, true)
}

// doBackup performs the actual backup. If checkShutdown is true, it will
// skip the backup if the KV is shutting down.
func (kv *KV) doBackup(ctx context.Context, checkShutdown bool) error {
	// Check if we're shutting down (unless this is a close-time flush)
	if checkShutdown {
		select {
		case <-kv.shutdown:
			return nil
		default:
		}
	}

	// First sync any remote changes
	mv := kv.maxVersion()
	err := kv.syncFromWithContext(ctx, mv)
	if err != nil {
		return err
	}

	// Get next sequence number
	seq, err := kv.nextSeqWithContext(ctx, kv.name)
	if err != nil {
		return err
	}

	// Update local max version
	if err := kv.setMaxVersion(seq); err != nil {
		return err
	}

	// Do the full backup
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

// Close flushes any pending backups and closes the underlying database.
func (kv *KV) Close() error {
	// Signal shutdown FIRST to prevent any new backups from starting
	kv.shutdownOnce.Do(func() {
		close(kv.shutdown)
	})

	// Check if there are pending writes to flush
	kv.backupMu.Lock()
	pendingWrites := kv.pendingWrites
	kv.pendingWrites = 0
	kv.backupMu.Unlock()

	// If there are pending writes, flush them now before closing
	// Use doBackup with checkShutdown=false since we intentionally want to flush
	if pendingWrites > 0 && !kv.readOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_ = kv.doBackup(ctx, false) // Best effort - ignore errors during close
		cancel()
	}

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
	// Use transactional set that records pending op and op-log entry
	if err := kv.setWithOpLog(key, encValue); err != nil {
		return err
	}
	return kv.syncAfterWrite()
}

// setWithOpLog stores a key-value pair with both pending_ops and op_log tracking.
func (kv *KV) setWithOpLog(key, encValue []byte) error {
	tx, err := kv.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Store the key-value pair
	_, err = tx.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, encValue)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to set key: %w", err)
	}

	// Record pending op (for current full-backup sync)
	if err := recordPendingOp(tx, "set", key, encValue); err != nil {
		_ = tx.Rollback()
		return err
	}

	// Record op-log entry (for future incremental sync)
	seq, err := getNextSeq(kv.db)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to get next seq: %w", err)
	}

	op := &Op{
		OpID:         newOpID(),
		Seq:          seq,
		OpType:       "set",
		Key:          key,
		Value:        encValue,
		HLCTimestamp: kv.hlc.Now(),
		DeviceID:     kv.localDevID,
		Synced:       false,
	}
	if err := logOp(tx, op); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
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
	// Use transactional delete that records pending op and op-log entry
	if err := kv.deleteWithOpLog(key); err != nil {
		return err
	}
	return kv.syncAfterWrite()
}

// deleteWithOpLog removes a key with both pending_ops and op_log tracking.
func (kv *KV) deleteWithOpLog(key []byte) error {
	tx, err := kv.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Delete the key
	_, err = tx.Exec("DELETE FROM kv WHERE key = ?", key)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to delete key: %w", err)
	}

	// Record pending op (for current full-backup sync)
	if err := recordPendingOp(tx, "delete", key, nil); err != nil {
		_ = tx.Rollback()
		return err
	}

	// Record op-log entry (for future incremental sync)
	seq, err := getNextSeq(kv.db)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to get next seq: %w", err)
	}

	op := &Op{
		OpID:         newOpID(),
		Seq:          seq,
		OpType:       "delete",
		Key:          key,
		Value:        nil,
		HLCTimestamp: kv.hlc.Now(),
		DeviceID:     kv.localDevID,
		Synced:       false,
	}
	if err := logOp(tx, op); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
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

	// Close current database
	if err := kv.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	// Remove database file and WAL files
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// Try to reopen the old database to keep the KV usable
			if db, reopenErr := openSQLite(dbPath); reopenErr == nil {
				kv.db = db
			}
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	// Reopen database - if this fails, the KV is left in an unusable state
	// but we've already removed the files, so we can't recover
	db, err := openSQLite(dbPath)
	if err != nil {
		return fmt.Errorf("failed to reopen database after reset: %w", err)
	}
	kv.db = db
	return kv.Sync()
}

// Do opens a KV store, executes the provided function, and closes the store.
// This is the recommended API for MCP servers and other processes that should
// not hold a database connection open for extended periods.
//
// The lock is only held for the duration of fn, allowing other processes to
// write between calls.
func Do(name string, fn func(*KV) error, opts ...Option) (err error) {
	kv, err := OpenWithDefaults(name, opts...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := kv.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return fn(kv)
}

// DoReadOnly opens a KV store in read-only mode, executes the function, and closes.
// Use this for read operations when you don't need to write.
func DoReadOnly(name string, fn func(*KV) error, opts ...Option) (err error) {
	kv, err := OpenWithDefaultsReadOnly(name, opts...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := kv.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return fn(kv)
}

// DoWithFallback opens a KV store with fallback behavior, executes the function,
// and closes. If write access cannot be acquired after retries, falls back to
// read-only mode. Use kv.IsReadOnly() inside fn to check which mode was obtained.
func DoWithFallback(name string, fn func(*KV) error, opts ...Option) (err error) {
	kv, err := OpenWithDefaultsFallback(name, opts...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := kv.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return fn(kv)
}
