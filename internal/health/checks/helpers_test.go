package checks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// makeBuckets builds a slice of HourlyBuckets anchored to a reference time.
// The last bucket starts at now.Truncate(time.Hour). Earlier buckets are
// spaced one hour apart, oldest first. len(counts) buckets are produced.
func makeBuckets(t *testing.T, counts []int64, now time.Time) []observability.HourlyBucket {
	t.Helper()
	if len(counts) == 0 {
		return nil
	}
	n := len(counts)
	baseHour := now.Truncate(time.Hour).Add(-time.Duration(n-1) * time.Hour)
	buckets := make([]observability.HourlyBucket, n)
	for i, c := range counts {
		buckets[i] = observability.HourlyBucket{
			Start: baseHour.Add(time.Duration(i) * time.Hour),
			Count: c,
		}
	}
	return buckets
}

// TestCountActiveHours verifies that countActiveHours counts only in-window
// buckets with a non-zero Count.
//
// Boundary semantics: a bucket is "in-window" when b.Start+1h >= cutoff,
// i.e., when any part of its hour-long interval overlaps [cutoff, now].
func TestCountActiveHours(t *testing.T) {
	t.Parallel()

	// Use a truncated time to make boundary reasoning exact.
	now := time.Now().Truncate(time.Hour)

	tests := []struct {
		name   string
		counts []int64
		window time.Duration
		want   int
	}{
		{
			name:   "zero buckets",
			counts: nil,
			window: time.Hour,
			want:   0,
		},
		{
			name:   "all-zero counts",
			counts: []int64{0, 0, 0},
			window: 3 * time.Hour,
			want:   0,
		},
		{
			name:   "some nonzero in window",
			counts: []int64{0, 5, 0, 3},
			window: 4 * time.Hour,
			want:   2,
		},
		{
			// 4 buckets (starts: now-3h, now-2h, now-1h, now).
			// 3h window: cutoff = now-3h.
			// Oldest bucket end = (now-3h)+1h = now-2h, which is >= cutoff (now-3h). Included.
			// All 4 buckets are in-window; counts 10, 5, 3 are non-zero = 3 active hours.
			name:   "four bucket window boundary all included",
			counts: []int64{10, 5, 0, 3},
			window: 3 * time.Hour,
			want:   3,
		},
		{
			// 5 buckets (starts: now-4h, now-3h, now-2h, now-1h, now).
			// 3h window: cutoff = now-3h.
			// Oldest bucket end = (now-4h)+1h = now-3h, which equals cutoff exactly.
			// The condition is !Before(cutoff), so now-3h is NOT before now-3h -> included.
			// That bucket has count 10, so all 5 buckets are in window; counts {10,5,0,3} active = 4.
			// NOTE: we use count 0 in slot 1 to verify exclusion only happens beyond strict cutoff.
			name:   "oldest bucket end equals cutoff is included",
			counts: []int64{10, 5, 0, 3, 7},
			window: 4 * time.Hour,
			want:   4,
		},
		{
			// Sub-hour window: cutoff = now - 15m.
			// Oldest (n-1) bucket ends at now-3h+1h = now-2h < cutoff (now-15m). Excluded.
			// Only the newest bucket (now, now+1h) overlaps. count=9 -> 1 active.
			name:   "sub-hour window covers only current bucket",
			counts: []int64{7, 0, 0, 9},
			window: 15 * time.Minute,
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buckets := makeBuckets(t, tt.counts, now)
			got := countActiveHours(buckets, tt.window, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDetectVelocity verifies that detectVelocity correctly classifies the
// trend of the two most recent in-window buckets.
func TestDetectVelocity(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Hour)

	tests := []struct {
		name   string
		counts []int64
		window time.Duration
		want   velocityTrend
	}{
		{
			name:   "insufficient data - empty",
			counts: nil,
			window: time.Hour,
			want:   velocityStable,
		},
		{
			name:   "insufficient data - one bucket",
			counts: []int64{10},
			window: time.Hour,
			want:   velocityStable,
		},
		{
			name:   "increasing",
			counts: []int64{5, 10},
			window: 2 * time.Hour,
			want:   velocityIncreasing,
		},
		{
			name:   "decreasing",
			counts: []int64{10, 5},
			window: 2 * time.Hour,
			want:   velocityDecreasing,
		},
		{
			name:   "stable",
			counts: []int64{7, 7},
			window: 2 * time.Hour,
			want:   velocityStable,
		},
		{
			// Pattern [100, 0, 0, 10]: last two in-window buckets are [0, 10].
			// current=10 > previous=0, so velocity is increasing.
			name:   "zero-gap pattern increasing at end",
			counts: []int64{100, 0, 0, 10},
			window: 4 * time.Hour,
			want:   velocityIncreasing,
		},
		{
			// 5 buckets, 2h window: cutoff = now-2h.
			// Oldest bucket starts now-4h, ends now-3h; NOT in window.
			// In-window buckets (3): counts [5, 3] -> last two [5, 3] -> decreasing.
			// Use 2h window so oldest 2 buckets are excluded.
			name:   "old bucket excluded by window uses last two in-window",
			counts: []int64{100, 99, 5, 3, 0},
			window: 2 * time.Hour,
			// Last 2 in-window: counts 3, 0 -> decreasing.
			want: velocityDecreasing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buckets := makeBuckets(t, tt.counts, now)
			got := detectVelocity(buckets, tt.window, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestMaxBucketCount verifies that maxBucketCount returns the peak in-window
// bucket count.
func TestMaxBucketCount(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Hour)

	tests := []struct {
		name   string
		counts []int64
		window time.Duration
		want   int64
	}{
		{
			name:   "empty buckets",
			counts: nil,
			window: time.Hour,
			want:   0,
		},
		{
			name:   "single bucket",
			counts: []int64{42},
			window: time.Hour,
			want:   42,
		},
		{
			name:   "peak in middle",
			counts: []int64{5, 100, 20},
			window: 3 * time.Hour,
			want:   100,
		},
		{
			// 6 buckets; 2h window: cutoff = now-2h.
			// Bucket[0].end = now-4h < cutoff -> excluded.
			// Bucket[1].end = now-3h < cutoff -> excluded.
			// In-window: 4 buckets with counts [3, 7, 2, 5]. Peak = 7.
			name:   "window filtering excludes old peak",
			counts: []int64{999, 888, 3, 7, 2, 5},
			window: 2 * time.Hour,
			want:   7,
		},
		{
			name:   "all zeros",
			counts: []int64{0, 0, 0},
			window: 3 * time.Hour,
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buckets := makeBuckets(t, tt.counts, now)
			got := maxBucketCount(buckets, tt.window, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

// dropsConfig builds a windowedStatsConfig matching BufferDropsCheck thresholds.
func dropsConfig(window time.Duration) *windowedStatsConfig {
	return &windowedStatsConfig{
		baseWarnThreshold: 10,
		baseCritThreshold: 50,
		sustainedHours:    3,
		metricPrefix:      observability.MetricPrefixAudioDrops,
		window:            window,
	}
}

// recordDropsPerHour records the given drop counts into the store, one per
// hour. counts[0] is the oldest; counts[len-1] lands at endTime.Truncate(hour).
func recordDropsPerHour(t *testing.T, store *observability.HealthMetricsStore, key string, counts []int64, endTime time.Time) {
	t.Helper()
	base := endTime.Truncate(time.Hour)
	n := len(counts)
	for i, c := range counts {
		ts := base.Add(-time.Duration(n-1-i) * time.Hour)
		store.RecordAt(key, c, ts)
	}
}

// TestEvalWindowedStats_SeverityMatrix exercises the four-signal severity
// evaluation across representative operational scenarios.
func TestEvalWindowedStats_SeverityMatrix(t *testing.T) {
	t.Parallel()

	baseNow := time.Now().Truncate(time.Hour)

	tests := []struct {
		name        string
		window      time.Duration
		setup       func(t *testing.T, store *observability.HealthMetricsStore, now time.Time)
		wantStatus  health.Status
		wantPattern string
	}{
		{
			name:       "ZeroDrops",
			window:     time.Hour,
			setup:      nil,
			wantStatus: health.StatusSkipped,
		},
		{
			name:   "TransientBelowThreshold",
			window: time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				store.RecordAt("audio.drops.src1", 8, now)
			},
			wantStatus:  health.StatusHealthy,
			wantPattern: "transient",
		},
		{
			name:   "TransientAboveWarn",
			window: time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				store.RecordAt("audio.drops.src1", 15, now)
			},
			wantStatus:  health.StatusWarning,
			wantPattern: "transient",
		},
		{
			name:   "TransientAboveCrit",
			window: time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				store.RecordAt("audio.drops.src1", 55, now)
			},
			wantStatus:  health.StatusCritical,
			wantPattern: "transient",
		},
		{
			name:   "SubHourNoRecurrence",
			window: 15 * time.Minute,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				recordDropsPerHour(t, store, "audio.drops.src1", []int64{5, 5, 3}, now)
			},
			wantStatus:  health.StatusHealthy,
			wantPattern: "transient",
		},
		{
			name:   "SustainedAboveFloor",
			window: 6 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				recordDropsPerHour(t, store, "audio.drops.src1", []int64{0, 0, 10, 10, 10, 10}, now)
			},
			wantStatus:  health.StatusWarning,
			wantPattern: "sustained",
		},
		{
			name:   "SustainedBelowFloor",
			window: 6 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				recordDropsPerHour(t, store, "audio.drops.src1", []int64{0, 0, 1, 1, 1, 1}, now)
			},
			wantStatus:  health.StatusHealthy,
			wantPattern: "transient",
		},
		{
			name:   "SustainedWorsening",
			window: 6 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				recordDropsPerHour(t, store, "audio.drops.src1", []int64{0, 0, 0, 5, 10, 20}, now)
			},
			wantStatus:  health.StatusCritical,
			wantPattern: "sustained",
		},
		{
			name:   "SustainedCritical",
			window: 6 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				recordDropsPerHour(t, store, "audio.drops.src1", []int64{0, 60, 60, 60, 60, 60}, now)
			},
			wantStatus:  health.StatusCritical,
			wantPattern: "sustained",
		},
		{
			name:   "PeakOverridesLargeWindow",
			window: 7 * 24 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				store.RecordAt("audio.drops.src1", 2000, now)
			},
			wantStatus:  health.StatusCritical,
			wantPattern: "transient",
		},
		{
			name:   "PeakWarnInLargeWindow",
			window: 24 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				store.RecordAt("audio.drops.src1", 15, now)
			},
			wantStatus:  health.StatusWarning,
			wantPattern: "transient",
		},
		{
			name:   "WindowScaling",
			window: 24 * time.Hour,
			setup: func(t *testing.T, store *observability.HealthMetricsStore, now time.Time) {
				t.Helper()
				for i := range 12 {
					ts := now.Add(-time.Duration(11-i) * time.Hour)
					store.RecordAt("audio.drops.src1", 10, ts)
				}
			},
			wantStatus:  health.StatusWarning,
			wantPattern: "sustained",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newTestHealthStore(t)
			now := baseNow

			if tt.setup != nil {
				tt.setup(t, store, now)
			}

			cfg := dropsConfig(tt.window)
			result := evalWindowedStats("buffer_drops", health.CategoryAudio, store, nil, cfg, time.Now())

			assert.Equal(t, tt.wantStatus, result.Status, "status mismatch: message=%q", result.Message)

			if tt.wantStatus == health.StatusSkipped {
				return
			}

			require.NotNil(t, result.Details)
			assert.Equal(t, tt.wantPattern, result.Details["pattern"], "pattern mismatch")
		})
	}
}
