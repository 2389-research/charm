// ABOUTME: Tests for retry behavior in KV fallback functions
// ABOUTME: Ensures retry config and backoff logic work correctly

package kv

import (
	"errors"
	"testing"
	"time"
)

// TestRetryConfigDefaults verifies default retry settings are applied.
func TestRetryConfigDefaults(t *testing.T) {
	cfg := &Config{}
	applyRetryDefaults(cfg)

	if cfg.writeRetryAttempts != DefaultWriteRetryAttempts {
		t.Errorf("expected %d attempts, got %d", DefaultWriteRetryAttempts, cfg.writeRetryAttempts)
	}
	if cfg.writeRetryBaseDelay != DefaultWriteRetryBaseDelay {
		t.Errorf("expected %v base delay, got %v", DefaultWriteRetryBaseDelay, cfg.writeRetryBaseDelay)
	}
	if cfg.writeRetryMaxDelay != DefaultWriteRetryMaxDelay {
		t.Errorf("expected %v max delay, got %v", DefaultWriteRetryMaxDelay, cfg.writeRetryMaxDelay)
	}
}

// TestWithWriteRetryOption verifies custom retry settings are applied.
func TestWithWriteRetryOption(t *testing.T) {
	cfg := &Config{}
	opt := WithWriteRetry(5, 100*time.Millisecond, 1*time.Second)
	opt(cfg)

	if cfg.writeRetryAttempts != 5 {
		t.Errorf("expected 5 attempts, got %d", cfg.writeRetryAttempts)
	}
	if cfg.writeRetryBaseDelay != 100*time.Millisecond {
		t.Errorf("expected 100ms base delay, got %v", cfg.writeRetryBaseDelay)
	}
	if cfg.writeRetryMaxDelay != 1*time.Second {
		t.Errorf("expected 1s max delay, got %v", cfg.writeRetryMaxDelay)
	}
}

// TestWithNoWriteRetryOption verifies retry can be disabled.
func TestWithNoWriteRetryOption(t *testing.T) {
	// Test that WithNoWriteRetry works when applied before defaults
	// (matches actual usage in OpenWithFallback: options first, then defaults)
	cfg := &Config{}
	opt := WithNoWriteRetry()
	opt(cfg)                // Apply option
	applyRetryDefaults(cfg) // Then apply defaults - should NOT override

	if cfg.writeRetryAttempts != 0 {
		t.Errorf("expected 0 attempts after WithNoWriteRetry, got %d", cfg.writeRetryAttempts)
	}
	if cfg.writeRetryBaseDelay != 0 {
		t.Errorf("expected 0 base delay after WithNoWriteRetry, got %v", cfg.writeRetryBaseDelay)
	}
}

// TestWithNoWriteRetryNotOverridden verifies defaults don't override explicit config.
func TestWithNoWriteRetryNotOverridden(t *testing.T) {
	cfg := &Config{}
	WithNoWriteRetry()(cfg)
	applyRetryDefaults(cfg)

	// Defaults should NOT have been applied since retry was explicitly configured
	if cfg.writeRetryAttempts != 0 {
		t.Errorf("defaults incorrectly overrode WithNoWriteRetry: got %d attempts", cfg.writeRetryAttempts)
	}
}

// TestRetryWithBackoff tests the retry loop logic with exponential backoff.
func TestRetryWithBackoff(t *testing.T) {
	// Simulate a function that fails N times then succeeds
	failCount := 0
	maxFails := 2
	lockErr := errors.New("database is locked")

	tryOpen := func() error {
		failCount++
		if failCount <= maxFails {
			return lockErr
		}
		return nil
	}

	cfg := &Config{
		writeRetryAttempts:  3,
		writeRetryBaseDelay: 10 * time.Millisecond,
		writeRetryMaxDelay:  100 * time.Millisecond,
	}

	start := time.Now()
	err := retryWithBackoff(cfg, tryOpen, func(e error) bool {
		return e != nil && e.Error() == "database is locked"
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected success after retries, got error: %v", err)
	}

	// Should have taken at least 10ms + 20ms = 30ms for the delays
	if elapsed < 20*time.Millisecond {
		t.Errorf("expected retry delays, but completed in %v", elapsed)
	}

	if failCount != 3 { // 2 failures + 1 success
		t.Errorf("expected 3 attempts, got %d", failCount)
	}
}

// TestRetryExhausted tests that retry returns last error when exhausted.
func TestRetryExhausted(t *testing.T) {
	attemptCount := 0
	lockErr := errors.New("database is locked")

	tryOpen := func() error {
		attemptCount++
		return lockErr
	}

	cfg := &Config{
		writeRetryAttempts:  2,
		writeRetryBaseDelay: 5 * time.Millisecond,
		writeRetryMaxDelay:  50 * time.Millisecond,
	}

	err := retryWithBackoff(cfg, tryOpen, func(e error) bool {
		return e != nil && e.Error() == "database is locked"
	})

	if err == nil {
		t.Error("expected error after exhausted retries")
	}

	// 1 initial + 2 retries = 3 attempts
	if attemptCount != 3 {
		t.Errorf("expected 3 attempts, got %d", attemptCount)
	}
}

// TestRetryNonRetryableError tests that non-retryable errors fail immediately.
func TestRetryNonRetryableError(t *testing.T) {
	attemptCount := 0
	otherErr := errors.New("file not found")

	tryOpen := func() error {
		attemptCount++
		return otherErr
	}

	cfg := &Config{
		writeRetryAttempts:  5,
		writeRetryBaseDelay: 10 * time.Millisecond,
		writeRetryMaxDelay:  100 * time.Millisecond,
	}

	err := retryWithBackoff(cfg, tryOpen, func(e error) bool {
		return e != nil && e.Error() == "database is locked"
	})

	if err == nil {
		t.Error("expected error for non-retryable failure")
	}

	// Should fail immediately without retrying
	if attemptCount != 1 {
		t.Errorf("expected 1 attempt for non-retryable error, got %d", attemptCount)
	}
}

// TestRetryNoRetryConfigured tests behavior when retry is disabled.
func TestRetryNoRetryConfigured(t *testing.T) {
	attemptCount := 0
	lockErr := errors.New("database is locked")

	tryOpen := func() error {
		attemptCount++
		return lockErr
	}

	cfg := &Config{
		writeRetryAttempts:  0, // No retries
		writeRetryBaseDelay: 0,
		writeRetryMaxDelay:  0,
	}

	err := retryWithBackoff(cfg, tryOpen, func(e error) bool {
		return e != nil && e.Error() == "database is locked"
	})

	if err == nil {
		t.Error("expected error with no retries configured")
	}

	// Only 1 attempt when retries disabled
	if attemptCount != 1 {
		t.Errorf("expected 1 attempt with no retries, got %d", attemptCount)
	}
}

// TestRetryBackoffCap tests that delay is capped at maxDelay.
func TestRetryBackoffCap(t *testing.T) {
	attemptCount := 0
	lockErr := errors.New("database is locked")
	var delays []time.Duration
	lastTime := time.Now()

	tryOpen := func() error {
		now := time.Now()
		if attemptCount > 0 {
			delays = append(delays, now.Sub(lastTime))
		}
		lastTime = now
		attemptCount++
		if attemptCount < 5 {
			return lockErr
		}
		return nil
	}

	cfg := &Config{
		writeRetryAttempts:  5,
		writeRetryBaseDelay: 10 * time.Millisecond,
		writeRetryMaxDelay:  25 * time.Millisecond, // Cap at 25ms
	}

	err := retryWithBackoff(cfg, tryOpen, func(e error) bool {
		return e != nil && e.Error() == "database is locked"
	})

	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}

	// Delays should be: 10ms, 20ms, 25ms (capped), 25ms (capped)
	// Allow some tolerance for timing
	for i, d := range delays {
		if d > 35*time.Millisecond { // maxDelay + tolerance
			t.Errorf("delay %d was %v, expected <= 35ms (cap + tolerance)", i, d)
		}
	}
}

// retryWithBackoff is the core retry logic extracted for testing.
// This will be used by OpenWithFallback and OpenWithDefaultsFallback.
func retryWithBackoff(cfg *Config, tryFn func() error, isRetryable func(error) bool) error {
	var lastErr error
	delay := cfg.writeRetryBaseDelay

	for attempt := 0; attempt <= cfg.writeRetryAttempts; attempt++ {
		err := tryFn()
		if err == nil {
			return nil
		}

		if !isRetryable(err) {
			return err // Non-retryable error, fail immediately
		}

		lastErr = err
		if attempt < cfg.writeRetryAttempts {
			time.Sleep(delay)
			delay = min(delay*2, cfg.writeRetryMaxDelay)
		}
	}

	return lastErr
}
