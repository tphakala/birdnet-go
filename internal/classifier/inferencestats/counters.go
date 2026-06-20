// Package inferencestats provides lock-free atomic counters for tracking
// BirdNET model inference timing. Designed for the hot analysis path where
// contention-free recording is critical.
package inferencestats

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Counters tracks BirdNET inference timing using lock-free atomic operations.
// Safe for concurrent use from the hot analysis path.
type Counters struct {
	InvokeCount   atomic.Int64 // total invocations since startup
	InvokeTotalUs atomic.Int64 // cumulative invoke duration in microseconds
	// InvokeMaxUs is the windowed peak: max single invoke duration since the last
	// Snapshot read, reset-on-read so the collector can emit per-interval maxima.
	InvokeMaxUs atomic.Int64
	// InvokeMaxUsLifetime is the all-time peak single invoke duration since
	// startup. It is never reset on read, so non-destructive status/health views
	// (PeekAll) report a max that is always >= the lifetime average.
	InvokeMaxUsLifetime atomic.Int64
	InvokeErrors        atomic.Int64 // total invocation errors since startup
}

// RecordInvoke records a single model invocation duration in microseconds.
func (c *Counters) RecordInvoke(durationUs int64) {
	c.InvokeCount.Add(1)
	c.InvokeTotalUs.Add(durationUs)
	updateAtomicMax(&c.InvokeMaxUs, durationUs)
	updateAtomicMax(&c.InvokeMaxUsLifetime, durationUs)
}

// RecordError increments the cumulative error counter by one.
func (c *Counters) RecordError() {
	c.InvokeErrors.Add(1)
}

// Snapshot captures the current counter state. Max values are reset-on-read.
type Snapshot struct {
	InvokeCount   int64
	InvokeTotalUs int64
	InvokeMaxUs   int64 // reset to zero after read
	InvokeErrors  int64 // cumulative error count; not reset on read
	CollectedAt   time.Time
}

// Snapshot returns a point-in-time copy of all counters.
// InvokeMaxUs is atomically swapped to zero (reset-on-read).
// InvokeErrors is cumulative and is not reset on read.
func (c *Counters) Snapshot() Snapshot {
	return Snapshot{
		InvokeCount:   c.InvokeCount.Load(),
		InvokeTotalUs: c.InvokeTotalUs.Load(),
		InvokeMaxUs:   c.InvokeMaxUs.Swap(0),
		InvokeErrors:  c.InvokeErrors.Load(),
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

// SanitizeModelID replaces non-alphanumeric/non-underscore characters with underscores.
func SanitizeModelID(modelID string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, modelID)
}

// MetricKey returns the metrics store key for a model's average inference time.
func MetricKey(modelID string) string {
	return "inference." + SanitizeModelID(modelID) + ".avg_ms"
}

// RTFMetricKey returns the ring-buffer series key for a model's real-time factor.
// Single source of truth shared by the collector (writer) and the inference
// status endpoint (which advertises it in metricKeys). Pure helper.
func RTFMetricKey(modelID string) string {
	return "inference." + SanitizeModelID(modelID) + ".rtf"
}

// ThroughputMetricKey returns the ring-buffer series key for a model's throughput.
// Single source of truth shared by the collector (writer) and the inference
// status endpoint (which advertises it in metricKeys). Pure helper.
func ThroughputMetricKey(modelID string) string {
	return "inference." + SanitizeModelID(modelID) + ".throughput"
}

// ErrorRateMetricKey returns the ring-buffer series key for a model's error rate.
// Single source of truth shared by the collector (writer) and the inference
// status endpoint (which advertises it in metricKeys). Pure helper.
func ErrorRateMetricKey(modelID string) string {
	return "inference." + SanitizeModelID(modelID) + ".error_rate"
}

// CounterMap tracks per-model inference counters. Safe for concurrent use.
type CounterMap struct {
	models sync.Map // model ID (string) -> *Counters
}

// RecordInvoke records a single invocation duration for the given model ID.
func (m *CounterMap) RecordInvoke(modelID string, durationUs int64) {
	if v, ok := m.models.Load(modelID); ok {
		v.(*Counters).RecordInvoke(durationUs)
		return
	}
	c, _ := m.models.LoadOrStore(modelID, &Counters{})
	c.(*Counters).RecordInvoke(durationUs)
}

// RecordError increments the error counter for the given model ID.
func (m *CounterMap) RecordError(modelID string) {
	if v, ok := m.models.Load(modelID); ok {
		v.(*Counters).RecordError()
		return
	}
	c, _ := m.models.LoadOrStore(modelID, &Counters{})
	c.(*Counters).RecordError()
}

// SnapshotAll returns a snapshot of all per-model counters. Each model's max
// is reset on read, consistent with Counters.Snapshot behaviour.
func (m *CounterMap) SnapshotAll() map[string]Snapshot {
	result := make(map[string]Snapshot)
	m.models.Range(func(key, value any) bool {
		result[key.(string)] = value.(*Counters).Snapshot()
		return true
	})
	return result
}

// PeekSnapshot is a non-destructive point-in-time view of a model's counters,
// used by status and health views. It carries both maxima so each consumer
// reads the one that fits: InvokeMaxUs is the windowed peak (max since the last
// collector tick, matching Snapshot.InvokeMaxUs) for recency-sensitive checks
// like the latency health check, while InvokeMaxUsLifetime is the all-time peak
// for the model card, where reporting the lifetime peak keeps the displayed max
// latency >= the lifetime average latency. PeekAll never resets either counter.
type PeekSnapshot struct {
	InvokeCount         int64
	InvokeTotalUs       int64
	InvokeMaxUs         int64 // windowed max since last collector tick; not reset by Peek
	InvokeMaxUsLifetime int64 // all-time max single invoke duration; never reset
	InvokeErrors        int64 // cumulative error count; not reset on read
}

// PeekAll returns a non-destructive snapshot of all per-model counters. Unlike
// SnapshotAll, it never resets any counter (it Loads instead of Swap(0)), so the
// collector's reset-on-read windowed max is unaffected. It exposes both the
// windowed max (InvokeMaxUs) and the never-reset lifetime max (InvokeMaxUsLifetime).
func (m *CounterMap) PeekAll() map[string]PeekSnapshot {
	result := make(map[string]PeekSnapshot)
	m.models.Range(func(key, value any) bool {
		c := value.(*Counters)
		result[key.(string)] = PeekSnapshot{
			InvokeCount:         c.InvokeCount.Load(),
			InvokeTotalUs:       c.InvokeTotalUs.Load(),
			InvokeMaxUs:         c.InvokeMaxUs.Load(),
			InvokeMaxUsLifetime: c.InvokeMaxUsLifetime.Load(),
			InvokeErrors:        c.InvokeErrors.Load(),
		}
		return true
	})
	return result
}

// Delete removes the counters for the given model ID.
func (m *CounterMap) Delete(modelID string) {
	m.models.Delete(modelID)
}
