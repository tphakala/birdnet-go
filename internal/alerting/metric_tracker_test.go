package alerting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetricTracker_ImmediateFire(t *testing.T) {
	tracker := NewMetricTracker()
	now := time.Now()

	// Single sample above threshold with 0 duration → fires immediately
	tracker.Record("cpu", 95.0, now)
	assert.True(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", 0, now))
}

func TestMetricTracker_SustainedThreshold(t *testing.T) {
	tracker := NewMetricTracker()
	base := time.Now().Add(-10 * time.Minute)

	// Record samples above 90% over 5 minutes
	for i := range 6 {
		tracker.Record("cpu", 95.0, base.Add(time.Duration(i)*time.Minute))
	}

	now := base.Add(5 * time.Minute)
	assert.True(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", 5*time.Minute, now))
}

func TestMetricTracker_DipBelowThreshold(t *testing.T) {
	tracker := NewMetricTracker()
	base := time.Now().Add(-10 * time.Minute)

	// Record samples with a dip below threshold mid-window
	tracker.Record("cpu", 95.0, base)
	tracker.Record("cpu", 95.0, base.Add(1*time.Minute))
	tracker.Record("cpu", 80.0, base.Add(2*time.Minute)) // dip
	tracker.Record("cpu", 95.0, base.Add(3*time.Minute))
	tracker.Record("cpu", 95.0, base.Add(4*time.Minute))
	tracker.Record("cpu", 95.0, base.Add(5*time.Minute))

	now := base.Add(5 * time.Minute)
	assert.False(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", 5*time.Minute, now),
		"should not fire when a dip occurs within the window")
}

func TestMetricTracker_RecoverAfterDip(t *testing.T) {
	tracker := NewMetricTracker()
	base := time.Now().Add(-15 * time.Minute)

	// Early dip, then sustained above threshold
	tracker.Record("cpu", 95.0, base)
	tracker.Record("cpu", 80.0, base.Add(1*time.Minute)) // dip
	// Now sustained from minute 5 onward
	for i := 5; i <= 12; i++ {
		tracker.Record("cpu", 95.0, base.Add(time.Duration(i)*time.Minute))
	}

	now := base.Add(12 * time.Minute)
	// 5-minute window from 7 to 12 should all be above threshold
	assert.True(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", 5*time.Minute, now))
}

func TestMetricTracker_DifferentMetrics(t *testing.T) {
	tracker := NewMetricTracker()
	now := time.Now()

	tracker.Record("cpu", 95.0, now)
	tracker.Record("memory", 50.0, now)

	assert.True(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", 0, now))
	assert.False(t, tracker.IsSustained("memory", OperatorGreaterThan, "90", 0, now))
}

func TestMetricTracker_NoSamples(t *testing.T) {
	tracker := NewMetricTracker()
	now := time.Now()

	assert.False(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", 5*time.Minute, now))
}

func TestMetricTracker_OldSamplesEvicted(t *testing.T) {
	tracker := NewMetricTracker()
	now := time.Now()

	// Record a sample that's older than maxSampleAge
	old := now.Add(-maxSampleAge - time.Minute)
	tracker.Record("cpu", 95.0, old)

	// Record a new sample to trigger eviction
	tracker.Record("cpu", 95.0, now)

	// The old sample should have been evicted, so sustained check
	// should fail because there's no sample covering the full 30-minute window
	assert.False(t, tracker.IsSustained("cpu", OperatorGreaterThan, "90", maxSampleAge, now))
}

func TestMetricTracker_InvalidThreshold(t *testing.T) {
	tracker := NewMetricTracker()
	now := time.Now()

	tracker.Record("cpu", 95.0, now)
	assert.False(t, tracker.IsSustained("cpu", OperatorGreaterThan, "not_a_number", 0, now))
}

func TestMetricTracker_LessThanOperator(t *testing.T) {
	tracker := NewMetricTracker()
	base := time.Now().Add(-6 * time.Minute)

	for i := range 7 {
		tracker.Record("disk_free", 10.0, base.Add(time.Duration(i)*time.Minute))
	}

	now := base.Add(6 * time.Minute)
	assert.True(t, tracker.IsSustained("disk_free", OperatorLessThan, "20", 5*time.Minute, now))
}
