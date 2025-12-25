package kv

import (
	"sync"
	"testing"
	"time"
)

func TestHLC_Now_Monotonic(t *testing.T) {
	hlc := NewHLC()

	var prev int64
	for i := 0; i < 1000; i++ {
		ts := hlc.Now()
		if ts <= prev {
			t.Errorf("HLC not monotonic: %d <= %d at iteration %d", ts, prev, i)
		}
		prev = ts
	}
}

func TestHLC_Now_CloseToPhysical(t *testing.T) {
	hlc := NewHLC()

	before := time.Now().UnixMilli()
	ts := hlc.Now()
	after := time.Now().UnixMilli()

	hlcTime := HLCTime(ts)

	if hlcTime < before || hlcTime > after+1 {
		t.Errorf("HLC time %d not close to physical time [%d, %d]", hlcTime, before, after)
	}
}

func TestHLC_Update_TakesMaximum(t *testing.T) {
	hlc := NewHLC()

	// Get a local timestamp first
	localTs := hlc.Now()

	// Simulate receiving a timestamp from the future
	futureTime := (time.Now().UnixMilli() + 10000) << 16 // 10 seconds in future
	updated := hlc.Update(futureTime)

	// Updated timestamp should be greater than both local and future
	if updated <= localTs {
		t.Errorf("update should advance past local: %d <= %d", updated, localTs)
	}
	if updated <= futureTime {
		t.Errorf("update should advance past future: %d <= %d", updated, futureTime)
	}
}

func TestHLC_Update_DoesNotGoBackward(t *testing.T) {
	hlc := NewHLC()

	// Get some timestamps
	ts1 := hlc.Now()
	ts2 := hlc.Now()

	// Update with an old timestamp
	pastTime := (time.Now().UnixMilli() - 10000) << 16 // 10 seconds ago
	updated := hlc.Update(pastTime)

	// Should still be greater than ts2
	if updated <= ts2 {
		t.Errorf("update with old time should not go backward: %d <= %d", updated, ts2)
	}

	// And future timestamps should continue increasing
	ts3 := hlc.Now()
	if ts3 <= updated {
		t.Errorf("Now() after Update should continue increasing: %d <= %d", ts3, updated)
	}

	_ = ts1 // Silence unused variable
}

func TestHLC_ConcurrentNow(t *testing.T) {
	hlc := NewHLC()

	var wg sync.WaitGroup
	results := make(chan int64, 100)

	// 10 goroutines each getting 10 timestamps
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				results <- hlc.Now()
			}
		}()
	}

	wg.Wait()
	close(results)

	// Collect all timestamps
	var timestamps []int64
	for ts := range results {
		timestamps = append(timestamps, ts)
	}

	// All timestamps should be unique
	seen := make(map[int64]bool)
	for _, ts := range timestamps {
		if seen[ts] {
			t.Errorf("duplicate HLC timestamp: %d", ts)
		}
		seen[ts] = true
	}
}

func TestCompareHLC(t *testing.T) {
	tests := []struct {
		a, b     int64
		expected int
	}{
		{100, 200, -1},
		{200, 100, 1},
		{100, 100, 0},
		{0, 0, 0},
	}

	for _, tt := range tests {
		result := CompareHLC(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("CompareHLC(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestHLCTime(t *testing.T) {
	now := time.Now().UnixMilli()
	packed := now<<16 | 42 // time with counter

	extracted := HLCTime(packed)
	if extracted != now {
		t.Errorf("HLCTime(%d) = %d, want %d", packed, extracted, now)
	}
}

func TestHLCToTime(t *testing.T) {
	now := time.Now()
	nowMilli := now.UnixMilli()
	packed := nowMilli << 16

	result := HLCToTime(packed)

	// Should be within 1ms of original
	diff := result.Sub(now)
	if diff > time.Millisecond || diff < -time.Millisecond {
		t.Errorf("HLCToTime difference too large: %v", diff)
	}
}

func TestHLC_CounterOverflow(t *testing.T) {
	hlc := NewHLC()

	// Set up HLC with counter at max value (65535)
	// First, call Now() to initialize, then manipulate internal state
	ts1 := hlc.Now()
	time1 := HLCTime(ts1)

	// Manually set counter to near max to test overflow
	// We need to use reflection or just test via behavior
	// Better approach: rapidly call Now() many times within same millisecond
	// The counter has 16 bits = 65535 max value

	// Instead of relying on timing, let's verify the counter portion is correct
	// by checking uniqueness even with rapid calls
	seen := make(map[int64]bool)
	for i := 0; i < 70000; i++ {
		ts := hlc.Now()
		if seen[ts] {
			t.Errorf("duplicate timestamp at iteration %d: %d", i, ts)
		}
		seen[ts] = true
	}

	// After 70000 calls, we should have advanced the time component
	// because the counter would have overflowed at least once
	tsLast := hlc.Now()
	timeLast := HLCTime(tsLast)

	if timeLast <= time1 {
		t.Errorf("time should have advanced after counter overflow: start=%d, end=%d", time1, timeLast)
	}
}
