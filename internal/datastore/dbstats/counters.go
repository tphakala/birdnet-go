// Package dbstats provides lightweight atomic counters for tracking database
// operation latency and throughput. These counters are bumped from GORM
// callbacks on every query, so they must be lock-free and allocation-free
// in the hot path.
package dbstats

import (
	"sync/atomic"
	"time"
)

// SlowThresholdUs defines the threshold in microseconds above which a query
// is counted as "slow". Currently set to 100ms.
const SlowThresholdUs = 100_000

// Counters tracks database operation counts and latencies using lock-free
// atomic operations. Safe for concurrent use from multiple goroutines.
//
// Read operations correspond to SELECT queries; write operations correspond
// to INSERT, UPDATE, and DELETE queries.
type Counters struct {
	// Read operations (SELECT)
	ReadCount   atomic.Int64 // total reads since startup
	ReadTotalUs atomic.Int64 // cumulative read duration in microseconds
	ReadMaxUs   atomic.Int64 // max single read duration since last snapshot

	// Write operations (INSERT/UPDATE/DELETE)
	WriteCount   atomic.Int64 // total writes since startup
	WriteTotalUs atomic.Int64 // cumulative write duration in microseconds
	WriteMaxUs   atomic.Int64 // max single write duration since last snapshot

	// Slow queries (> SlowThresholdUs)
	SlowQueryCount atomic.Int64

	// SQLite-specific: SQLITE_BUSY error count
	BusyTimeouts atomic.Int64
}

// RecordRead records a completed read operation with the given duration.
func (c *Counters) RecordRead(durationUs int64) {
	c.ReadCount.Add(1)
	c.ReadTotalUs.Add(durationUs)
	updateAtomicMax(&c.ReadMaxUs, durationUs)
	if durationUs > SlowThresholdUs {
		c.SlowQueryCount.Add(1)
	}
}

// RecordWrite records a completed write operation with the given duration.
func (c *Counters) RecordWrite(durationUs int64) {
	c.WriteCount.Add(1)
	c.WriteTotalUs.Add(durationUs)
	updateAtomicMax(&c.WriteMaxUs, durationUs)
	if durationUs > SlowThresholdUs {
		c.SlowQueryCount.Add(1)
	}
}

// RecordBusyTimeout increments the SQLITE_BUSY error counter.
func (c *Counters) RecordBusyTimeout() {
	c.BusyTimeouts.Add(1)
}

// Snapshot represents a point-in-time capture of all counter values.
type Snapshot struct {
	ReadCount    int64
	ReadTotalUs  int64
	ReadMaxUs    int64 // max since last snapshot (reset-on-read)
	WriteCount   int64
	WriteTotalUs int64
	WriteMaxUs   int64 // max since last snapshot (reset-on-read)
	SlowQueries  int64
	BusyTimeouts int64
	CollectedAt  time.Time
}

// Snapshot returns a point-in-time capture of all counter values.
// ReadMaxUs and WriteMaxUs are reset to zero after reading (reset-on-read),
// so each snapshot captures the max for the interval since the last snapshot.
// All other counters are cumulative and never reset.
//
// NOTE: Snapshot must be called from a single goroutine (the collector).
// Concurrent callers would race on the Swap(0) for max values, causing
// the second caller to see zero.
func (c *Counters) Snapshot() Snapshot {
	return Snapshot{
		ReadCount:    c.ReadCount.Load(),
		ReadTotalUs:  c.ReadTotalUs.Load(),
		ReadMaxUs:    c.ReadMaxUs.Swap(0),
		WriteCount:   c.WriteCount.Load(),
		WriteTotalUs: c.WriteTotalUs.Load(),
		WriteMaxUs:   c.WriteMaxUs.Swap(0),
		SlowQueries:  c.SlowQueryCount.Load(),
		BusyTimeouts: c.BusyTimeouts.Load(),
		CollectedAt:  time.Now(),
	}
}

// updateAtomicMax uses a CAS loop to atomically update addr to val
// if val is greater than the current value.
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
