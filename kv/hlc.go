// ABOUTME: Hybrid Logical Clock (HLC) implementation for ordering distributed events
// ABOUTME: Combines physical time with logical counter for monotonic ordering

package kv

import (
	"sync"
	"time"
)

// HLC represents a Hybrid Logical Clock.
// It provides timestamps that are:
// - Monotonically increasing (never go backward)
// - Comparable across devices (with device_id as tiebreaker)
// - Close to physical time (never falls behind wall clock)
//
// Format: upper 48 bits = milliseconds since epoch, lower 16 bits = counter.
// This gives ~8900 years of range with 65535 events per millisecond.
type HLC struct {
	mu       sync.Mutex
	lastTime int64 // Last timestamp in milliseconds
	counter  uint16
}

// NewHLC creates a new Hybrid Logical Clock.
func NewHLC() *HLC {
	return &HLC{}
}

// Now returns the current HLC timestamp for a local event.
// Guarantees monotonically increasing values.
func (h *HLC) Now() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	physicalTime := time.Now().UnixMilli()

	if physicalTime > h.lastTime {
		// Physical time moved forward - reset counter
		h.lastTime = physicalTime
		h.counter = 0
	} else {
		// Same or earlier physical time - increment counter
		h.counter++
		if h.counter == 0 {
			// Counter overflow - force time forward
			h.lastTime++
		}
	}

	return h.pack()
}

// Update updates the HLC based on a received timestamp.
// Used when receiving events from other devices.
// Returns the updated timestamp.
func (h *HLC) Update(received int64) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	physicalTime := time.Now().UnixMilli()
	receivedTime, receivedCounter := unpack(received)

	// Take max of physical time, our last time, and received time
	if physicalTime > h.lastTime && physicalTime > receivedTime {
		h.lastTime = physicalTime
		h.counter = 0
	} else if h.lastTime > receivedTime {
		// Our time is ahead - just increment counter
		h.counter++
		if h.counter == 0 {
			h.lastTime++
		}
	} else if receivedTime > h.lastTime {
		// Received time is ahead - adopt it
		h.lastTime = receivedTime
		h.counter = receivedCounter + 1
		if h.counter == 0 {
			h.lastTime++
		}
	} else {
		// Same time - take max counter + 1
		if receivedCounter >= h.counter {
			h.counter = receivedCounter + 1
			if h.counter == 0 {
				h.lastTime++
			}
		} else {
			h.counter++
			if h.counter == 0 {
				h.lastTime++
			}
		}
	}

	return h.pack()
}

// pack combines lastTime and counter into a single int64.
func (h *HLC) pack() int64 {
	return (h.lastTime << 16) | int64(h.counter)
}

// unpack splits an HLC timestamp into time and counter components.
func unpack(ts int64) (time int64, counter uint16) {
	return ts >> 16, uint16(ts & 0xFFFF)
}

// CompareHLC compares two HLC timestamps.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func CompareHLC(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// HLCTime extracts the physical time component from an HLC timestamp.
// Returns milliseconds since Unix epoch.
func HLCTime(ts int64) int64 {
	t, _ := unpack(ts)
	return t
}

// HLCToTime converts an HLC timestamp to a time.Time.
func HLCToTime(ts int64) time.Time {
	return time.UnixMilli(HLCTime(ts))
}
