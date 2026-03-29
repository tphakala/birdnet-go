// Package inferencestats provides lock-free atomic counters for tracking
// BirdNET model inference timing. Designed for the hot analysis path where
// contention-free recording is critical.
package inferencestats

import (
	"sync/atomic"
	"time"
)

// Counters tracks BirdNET inference timing using lock-free atomic operations.
// Safe for concurrent use from the hot analysis path.
type Counters struct {
	InvokeCount   atomic.Int64 // total invocations since startup
	InvokeTotalUs atomic.Int64 // cumulative invoke duration in microseconds
	InvokeMaxUs   atomic.Int64 // max single invoke duration since last snapshot
}

// RecordInvoke records a single model invocation duration in microseconds.
func (c *Counters) RecordInvoke(durationUs int64) {
	c.InvokeCount.Add(1)
	c.InvokeTotalUs.Add(durationUs)
	updateAtomicMax(&c.InvokeMaxUs, durationUs)
}

// Snapshot captures the current counter state. Max values are reset-on-read.
type Snapshot struct {
	InvokeCount   int64
	InvokeTotalUs int64
	InvokeMaxUs   int64 // reset to zero after read
	CollectedAt   time.Time
}

// Snapshot returns a point-in-time copy of all counters.
// InvokeMaxUs is atomically swapped to zero (reset-on-read).
func (c *Counters) Snapshot() Snapshot {
	return Snapshot{
		InvokeCount:   c.InvokeCount.Load(),
		InvokeTotalUs: c.InvokeTotalUs.Load(),
		InvokeMaxUs:   c.InvokeMaxUs.Swap(0),
		CollectedAt:   time.Now(),
	}
}

// updateAtomicMax atomically updates addr to val if val > current.
func updateAtomicMax(addr *atomic.Int64, val int64) {
	for {
		old := addr.Load()
		if val <= old {
			return
		}
		if addr.CompareAndSwap(old, val) {
			return
		}
	}
}
