// Package inferencestats tracks BirdNET model inference timing on the hot
// analysis path. Count, total, and max are lock-free atomics; a small
// low-contention mutex additionally guards a per-model ring buffer of recent
// durations used to compute a rolling-window p95 latency for health checks.
// Inference is rate-limited by audio clip cadence, so the lock is effectively
// uncontended.
package inferencestats

import (
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// latencyWindowSize is the number of recent inference durations retained per
// model for rolling-window percentile computation. 1024 int64 samples is 8 KiB
// per model and keeps a meaningful window (~30s) even under heavy multi-stream
// load, while sorting it on read stays well under a millisecond.
const latencyWindowSize = 1024

// healthLatencyPercentile is the percentile of recent inference latency reported
// to the latency health check. p95 forgives the occasional GC pause or scheduling
// hiccup (the slowest ~5% of inferences) so the check does not flap, while still
// catching a sustained inability to keep up with real time.
const healthLatencyPercentile = 0.95

// Counters tracks BirdNET inference timing using lock-free atomic operations.
// Safe for concurrent use from the hot analysis path.
type Counters struct {
	InvokeCount   atomic.Int64 // total invocations since startup
	InvokeTotalUs atomic.Int64 // cumulative invoke duration in microseconds
	// InvokeMaxUs is the windowed peak: max single invoke duration since the last
	// Snapshot read, reset-on-read. It exists so the metrics collector's Snapshot
	// path can expose a per-interval maximum, mirroring dbstats; no collector
	// metric currently consumes it (the live consumers are the lifetime max and
	// the p95 ring below), so it is retained as windowed-max infrastructure only.
	InvokeMaxUs atomic.Int64
	// InvokeMaxUsLifetime is the all-time peak single invoke duration since
	// startup. It is never reset on read, so non-destructive status/health views
	// (PeekAll) report a max that is always >= the lifetime average.
	InvokeMaxUsLifetime atomic.Int64
	InvokeErrors        atomic.Int64 // total invocation errors since startup

	// latMu guards the rolling-window latency ring below. It is held only for the
	// O(1) ring write in RecordInvoke and the O(window) copy in recentPercentileUs;
	// the atomic counters above stay lock-free.
	latMu sync.Mutex
	// latRing holds the most recent inference durations (microseconds) as a
	// circular buffer. latLen samples are valid; latPos is the next write index.
	latRing [latencyWindowSize]int64
	latPos  int
	latLen  int
}

// RecordInvoke records a single model invocation duration in microseconds.
func (c *Counters) RecordInvoke(durationUs int64) {
	c.InvokeCount.Add(1)
	c.InvokeTotalUs.Add(durationUs)
	updateAtomicMax(&c.InvokeMaxUs, durationUs)
	updateAtomicMax(&c.InvokeMaxUsLifetime, durationUs)

	c.latMu.Lock()
	c.latRing[c.latPos] = durationUs
	c.latPos = (c.latPos + 1) % latencyWindowSize
	if c.latLen < latencyWindowSize {
		c.latLen++
	}
	c.latMu.Unlock()
}

// recentPercentileUs returns the nearest-rank p-th percentile (p in [0,1]) of the
// retained recent inference durations in microseconds, or 0 when no samples have
// been recorded. The ring contents are copied under the lock and sorted on read;
// sample order does not affect the result.
func (c *Counters) recentPercentileUs(p float64) int64 {
	c.latMu.Lock()
	n := c.latLen
	if n == 0 {
		c.latMu.Unlock()
		return 0
	}
	samples := make([]int64, n)
	copy(samples, c.latRing[:n])
	c.latMu.Unlock()

	slices.Sort(samples)
	idx := int(math.Ceil(p*float64(n))) - 1
	idx = max(idx, 0)
	idx = min(idx, n-1)
	return samples[idx]
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
// used by status and health views. InvokeMaxUsLifetime is the all-time peak for
// the model card, where reporting the lifetime peak keeps the displayed max
// latency >= the lifetime average. RecentP95Us is the rolling-window 95th
// percentile latency for the health check: a recency-sensitive signal that is
// robust to one-off warm-up or GC spikes and independent of the collector's
// reset-on-read cycle. PeekAll never resets any counter.
type PeekSnapshot struct {
	InvokeCount         int64
	InvokeTotalUs       int64
	InvokeMaxUsLifetime int64 // all-time max single invoke duration; never reset
	RecentP95Us         int64 // rolling-window p95 latency over recent invocations
	InvokeErrors        int64 // cumulative error count; not reset on read
}

// PeekAll returns a non-destructive snapshot of all per-model counters. Unlike
// SnapshotAll, it never resets any counter (it Loads instead of Swap(0)), so the
// collector's reset-on-read windowed max is unaffected. It reports the never-reset
// lifetime max for the model card and the rolling-window p95 for the health check.
func (m *CounterMap) PeekAll() map[string]PeekSnapshot {
	result := make(map[string]PeekSnapshot)
	m.models.Range(func(key, value any) bool {
		c := value.(*Counters)
		result[key.(string)] = PeekSnapshot{
			InvokeCount:         c.InvokeCount.Load(),
			InvokeTotalUs:       c.InvokeTotalUs.Load(),
			InvokeMaxUsLifetime: c.InvokeMaxUsLifetime.Load(),
			RecentP95Us:         c.recentPercentileUs(healthLatencyPercentile),
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
