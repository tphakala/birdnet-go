package alerting

import (
	"strconv"
	"sync"
	"time"
)

const (
	// maxSamplesPerMetric is the maximum number of samples retained per metric.
	maxSamplesPerMetric = 120
	// maxSampleAge is the maximum age of a sample before eviction.
	maxSampleAge = 30 * time.Minute
)

// metricSample is a single timestamped metric value.
type metricSample struct {
	value     float64
	timestamp time.Time
}

// MetricTracker maintains per-metric ring buffers of recent samples
// for evaluating sustained threshold conditions.
type MetricTracker struct {
	buffers map[string][]metricSample
	mu      sync.RWMutex
}

// NewMetricTracker creates a new MetricTracker.
func NewMetricTracker() *MetricTracker {
	return &MetricTracker{
		buffers: make(map[string][]metricSample),
	}
}

// Record adds a new metric sample and evicts stale entries.
func (t *MetricTracker) Record(metricName string, value float64, timestamp time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	samples := t.buffers[metricName]
	samples = append(samples, metricSample{value: value, timestamp: timestamp})

	// Evict samples older than maxSampleAge
	cutoff := timestamp.Add(-maxSampleAge)
	start := 0
	for start < len(samples) && samples[start].timestamp.Before(cutoff) {
		start++
	}
	samples = samples[start:]

	// Cap buffer size
	if len(samples) > maxSamplesPerMetric {
		samples = samples[len(samples)-maxSamplesPerMetric:]
	}

	t.buffers[metricName] = samples
}

// IsSustained checks whether the given condition has been continuously true
// for the specified duration, based on recorded samples.
// Returns false if there are no samples within the duration window.
func (t *MetricTracker) IsSustained(metricName, operator, value string, duration time.Duration, now time.Time) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	samples := t.buffers[metricName]
	if len(samples) == 0 {
		return false
	}

	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return false
	}

	windowStart := now.Add(-duration)

	// Find samples within the duration window
	var inWindow []metricSample
	for _, s := range samples {
		if !s.timestamp.Before(windowStart) && !s.timestamp.After(now) {
			inWindow = append(inWindow, s)
		}
	}

	// Need at least one sample in the window
	if len(inWindow) == 0 {
		return false
	}

	// The earliest sample must be at or near the window start
	// to confirm the condition has been sustained for the full duration.
	// Allow a 20% grace period to accommodate typical collection intervals
	// (e.g., 60s intervals with a 5m duration need at least 60s grace).
	grace := duration / 5
	if inWindow[0].timestamp.After(windowStart.Add(grace)) {
		return false
	}

	// All samples in the window must satisfy the condition
	for _, s := range inWindow {
		if !compareFloat(s.value, operator, threshold) {
			return false
		}
	}

	return true
}

func compareFloat(value float64, operator string, threshold float64) bool {
	switch operator {
	case OperatorGreaterThan:
		return value > threshold
	case OperatorLessThan:
		return value < threshold
	case OperatorGreaterOrEqual:
		return value >= threshold
	case OperatorLessOrEqual:
		return value <= threshold
	default:
		return false
	}
}
