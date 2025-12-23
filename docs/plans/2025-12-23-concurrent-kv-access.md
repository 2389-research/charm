# Concurrent KV Access Design

## Problem

Currently, charm KV uses exclusive locking that prevents concurrent write access:

1. First process to call `OpenWithDefaults()` gets write lock
2. Subsequent processes fall back to read-only mode via `OpenWithDefaultsFallback()`
3. Read-only processes cannot write, even when the write-holder is idle

This causes issues with long-running processes like MCP servers:
- MCP server starts, grabs write lock
- CLI commands fall back to read-only
- User can't add entries from CLI while MCP is running

## Solution

Two complementary approaches:

1. **Retry with backoff** - `OpenWithDefaultsFallback` retries before giving up
2. **Transactional API** - `Do()` function for short-lived connections

## Current Behavior

```go
// kv/kv.go
func OpenWithDefaultsFallback(name string, opts ...Option) (*KV, error) {
    // Try read-write first
    kv, err := OpenWithDefaults(name, opts...)
    if err == nil {
        return kv, nil
    }

    // Fall back to read-only if locked
    if IsLocked(err) {
        return OpenWithDefaultsReadOnly(name, opts...)
    }
    return nil, err
}
```

## SQLite WAL Mode Reality

SQLite WAL mode already supports:
- Multiple concurrent readers
- One writer at a time (but writes are fast)
- Writers don't block readers
- Readers don't block writers

The "lock" is only held during active transactions, not between them. The current fallback to read-only is overly conservative.

## Proposed Solution: Retry with Backoff

Instead of immediately falling back to read-only, retry the write operation with exponential backoff.

### API Changes

```go
// kv/config.go
type Config struct {
    // ... existing fields ...

    // WriteRetryAttempts is the number of times to retry acquiring
    // write access before falling back to read-only. Default: 3
    WriteRetryAttempts int

    // WriteRetryBaseDelay is the base delay between retry attempts.
    // Uses exponential backoff. Default: 50ms
    WriteRetryBaseDelay time.Duration

    // WriteRetryMaxDelay caps the backoff delay. Default: 500ms
    WriteRetryMaxDelay time.Duration
}

// Option functions
func WithWriteRetry(attempts int, baseDelay, maxDelay time.Duration) Option
func WithNoWriteRetry() Option  // Immediately fall back (current behavior)
```

### Implementation

```go
// kv/kv.go
func OpenWithDefaultsFallback(name string, opts ...Option) (*KV, error) {
    cfg := defaultConfig()
    for _, opt := range opts {
        opt(cfg)
    }

    var lastErr error
    delay := cfg.WriteRetryBaseDelay

    for attempt := 0; attempt <= cfg.WriteRetryAttempts; attempt++ {
        kv, err := OpenWithDefaults(name, opts...)
        if err == nil {
            return kv, nil
        }

        if !IsLocked(err) {
            return nil, err  // Non-lock error, fail immediately
        }

        lastErr = err
        if attempt < cfg.WriteRetryAttempts {
            time.Sleep(delay)
            delay = min(delay*2, cfg.WriteRetryMaxDelay)
        }
    }

    // All retries exhausted, fall back to read-only
    return OpenWithDefaultsReadOnly(name, opts...)
}
```

### Per-Operation Retry for Writes

For write operations on an already-open connection:

```go
// kv/kv.go
func (k *KV) Set(key, value []byte) error {
    if k.readOnly {
        return ErrReadOnlyMode{}
    }

    return k.withWriteRetry(func() error {
        return k.db.Set(key, value)
    })
}

func (k *KV) withWriteRetry(fn func() error) error {
    var lastErr error
    delay := k.cfg.WriteRetryBaseDelay

    for attempt := 0; attempt <= k.cfg.WriteRetryAttempts; attempt++ {
        err := fn()
        if err == nil {
            return nil
        }

        if !isBusyError(err) {
            return err
        }

        lastErr = err
        if attempt < k.cfg.WriteRetryAttempts {
            time.Sleep(delay)
            delay = min(delay*2, k.cfg.WriteRetryMaxDelay)
        }
    }

    return fmt.Errorf("write failed after %d attempts: %w",
        k.cfg.WriteRetryAttempts+1, lastErr)
}

func isBusyError(err error) bool {
    // SQLite busy/locked error codes
    var sqliteErr *sqlite.Error
    if errors.As(err, &sqliteErr) {
        return sqliteErr.Code() == sqlite.SQLITE_BUSY ||
               sqliteErr.Code() == sqlite.SQLITE_LOCKED
    }
    return false
}
```

## Transactional API

For MCP servers and other long-running processes, use short-lived connections:

```go
// Do opens a KV store, executes the function, and closes it.
// The lock is only held for the duration of fn.
func Do(name string, fn func(*KV) error, opts ...Option) error {
    kv, err := OpenWithDefaults(name, opts...)
    if err != nil {
        return err
    }
    defer kv.Close()
    return fn(kv)
}

// DoReadOnly opens in read-only mode for read operations.
func DoReadOnly(name string, fn func(*KV) error, opts ...Option) error {
    kv, err := OpenWithDefaultsReadOnly(name, opts...)
    if err != nil {
        return err
    }
    defer kv.Close()
    return fn(kv)
}
```

### Migration for MCP Servers

```go
// Before (holds lock forever):
kv, _ := kv.OpenWithDefaults("notes")
defer kv.Close()
// ... server runs for hours holding lock

// After (lock only during operation):
kv.Do("notes", func(k *kv.KV) error {
    return k.Set(key, value)
})
```

## Implementation Order

1. Add retry config to `Config` struct
2. Add `WithWriteRetry()` and `WithNoWriteRetry()` options
3. Update `OpenWithFallback` and `OpenWithDefaultsFallback` with retry logic
4. Add `Do()` and `DoReadOnly()` functions
5. Add tests for retry behavior and transactional API

## Default Values

```go
const (
    DefaultWriteRetryAttempts  = 3
    DefaultWriteRetryBaseDelay = 50 * time.Millisecond
    DefaultWriteRetryMaxDelay  = 500 * time.Millisecond
)
```

With these defaults:
- Attempt 1: immediate
- Attempt 2: after 50ms
- Attempt 3: after 100ms
- Attempt 4: after 200ms
- Total max wait: 350ms before falling back to read-only

## Testing

1. **Unit tests**: Mock SQLite busy errors, verify retry behavior
2. **Integration tests**: Two processes writing concurrently
3. **Stress test**: Many concurrent writers, verify no data loss

## Migration

Existing code using `OpenWithDefaultsFallback()` gets retry behavior automatically with defaults. No breaking changes.

To opt out (preserve current behavior):
```go
kv.OpenWithDefaultsFallback(name, kv.WithNoWriteRetry())
```
