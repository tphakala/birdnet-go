package observability

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// defaultBucketCount is 168 hourly buckets covering 7 days of retention.
const defaultBucketCount = 168

// Metric key prefixes for health counters. Used by both the collector
// (recording) and health checks (querying) to ensure consistency.
const (
	MetricPrefixAudioDrops     = "audio.drops."
	MetricPrefixAudioOverruns  = "audio.overruns."
	MetricPrefixStreamRestarts = "stream.restarts."
)

// HourlyBucket holds the aggregated event count for a single hour.
type HourlyBucket struct {
	Start time.Time `json:"t"`
	Count int64     `json:"v"`
}

// hourlyRing is a fixed-size circular buffer of hourly buckets.
type hourlyRing struct {
	buckets []HourlyBucket
	head    int
	size    int
	last    time.Time // last event timestamp (any delta > 0)
}

func newHourlyRing(capacity int) *hourlyRing {
	return &hourlyRing{
		buckets: make([]HourlyBucket, capacity),
	}
}

// bucketStart returns the start-of-hour for a given time.
func bucketStart(t time.Time) time.Time {
	return t.Truncate(time.Hour)
}

// record adds a delta to the current hour's bucket, rolling over as needed.
func (r *hourlyRing) record(delta int64, now time.Time) {
	hour := bucketStart(now)

	if r.size == 0 {
		r.buckets[r.head] = HourlyBucket{Start: hour, Count: delta}
		r.size = 1
		if delta > 0 {
			r.last = now
		}
		return
	}

	cur := r.current()
	if cur.Start.Equal(hour) {
		cur.Count += delta
		if delta > 0 {
			r.last = now
		}
		return
	}

	// Advance head to a new bucket
	r.head = (r.head + 1) % len(r.buckets)
	r.buckets[r.head] = HourlyBucket{Start: hour, Count: delta}
	if r.size < len(r.buckets) {
		r.size++
	}
	if delta > 0 {
		r.last = now
	}
}

// current returns a pointer to the most recent bucket.
func (r *hourlyRing) current() *HourlyBucket {
	return &r.buckets[r.head]
}

// sum returns the total count of events within the given window ending at now.
func (r *hourlyRing) sum(window time.Duration, now time.Time) int64 {
	if r.size == 0 {
		return 0
	}
	cutoff := now.Add(-window)
	var total int64
	for i := range r.size {
		idx := (r.head - i + len(r.buckets)) % len(r.buckets)
		b := &r.buckets[idx]
		if b.Start.Before(cutoff) {
			break
		}
		total += b.Count
	}
	return total
}

// recentBuckets returns the last n hourly buckets in chronological order.
// Returns fewer than n if the ring has fewer entries.
func (r *hourlyRing) recentBuckets(n int) []HourlyBucket {
	if r.size == 0 {
		return nil
	}
	count := n
	if count > r.size {
		count = r.size
	}
	result := make([]HourlyBucket, count)
	for i := range count {
		idx := (r.head - count + 1 + i + len(r.buckets)) % len(r.buckets)
		result[i] = r.buckets[idx]
	}
	return result
}

// HealthMetricsStore provides thread-safe, hourly-bucketed aggregation of
// health counter metrics with 7-day retention. Each metric key maps to an
// independent ring of hourly buckets.
type HealthMetricsStore struct {
	mu     sync.RWMutex
	series map[string]*hourlyRing
	size   int
}

// NewHealthMetricsStore creates a store with the default 7-day (168 hourly buckets) retention.
func NewHealthMetricsStore() *HealthMetricsStore {
	return NewHealthMetricsStoreWithSize(defaultBucketCount)
}

// NewHealthMetricsStoreWithSize creates a store with a custom bucket count per metric.
func NewHealthMetricsStoreWithSize(bucketsPerMetric int) *HealthMetricsStore {
	if bucketsPerMetric <= 0 {
		bucketsPerMetric = defaultBucketCount
	}
	return &HealthMetricsStore{
		series: make(map[string]*hourlyRing),
		size:   bucketsPerMetric,
	}
}

// Record adds a delta to the specified metric key, aggregating into the
// current hour's bucket. Thread-safe.
func (s *HealthMetricsStore) Record(key string, delta int64) {
	s.RecordAt(key, delta, time.Now())
}

// RecordAt adds a delta at a specific time. Primarily for testing.
func (s *HealthMetricsStore) RecordAt(key string, delta int64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ring, ok := s.series[key]
	if !ok {
		ring = newHourlyRing(s.size)
		s.series[key] = ring
	}
	ring.record(delta, now)
}

// Sum returns the total event count for a metric within the given window.
func (s *HealthMetricsStore) Sum(key string, window time.Duration) int64 {
	return s.SumAt(key, window, time.Now())
}

// SumAt returns the total event count for a metric within the given window ending at now.
func (s *HealthMetricsStore) SumAt(key string, window time.Duration, now time.Time) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ring, ok := s.series[key]
	if !ok {
		return 0
	}
	return ring.sum(window, now)
}

// Buckets returns the last n hourly buckets for a metric in chronological order.
func (s *HealthMetricsStore) Buckets(key string, n int) []HourlyBucket {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ring, ok := s.series[key]
	if !ok {
		return nil
	}
	return ring.recentBuckets(n)
}

// LastEventTime returns the time of the most recent non-zero delta for a metric.
// Returns the zero time if no events have been recorded.
func (s *HealthMetricsStore) LastEventTime(key string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ring, ok := s.series[key]
	if !ok {
		return time.Time{}
	}
	return ring.last
}

// LatestBucketTime returns the start time of the most recent bucket for a metric.
// Returns the zero time if no data exists.
func (s *HealthMetricsStore) LatestBucketTime(key string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ring, ok := s.series[key]
	if !ok || ring.size == 0 {
		return time.Time{}
	}
	return ring.current().Start
}

// Keys returns all metric keys with data in the store.
func (s *HealthMetricsStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return slices.Collect(maps.Keys(s.series))
}

// KeysWithPrefix returns all metric keys matching the given prefix.
func (s *HealthMetricsStore) KeysWithPrefix(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	for k := range s.series {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys
}

// LifetimeTotal returns the sum of all buckets for a metric (entire retention window).
func (s *HealthMetricsStore) LifetimeTotal(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ring, ok := s.series[key]
	if !ok || ring.size == 0 {
		return 0
	}

	var total int64
	for i := range ring.size {
		idx := (ring.head - i + len(ring.buckets)) % len(ring.buckets)
		total += ring.buckets[idx].Count
	}
	return total
}
