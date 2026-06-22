package inferencestats

import (
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounters_RecordInvoke(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordInvoke(500)  // 500µs
	c.RecordInvoke(1200) // 1200µs

	snap := c.Snapshot()
	assert.Equal(t, int64(2), snap.InvokeCount)
	assert.Equal(t, int64(1700), snap.InvokeTotalUs)
	assert.Equal(t, int64(1200), snap.InvokeMaxUs)
}

func TestCounters_Snapshot_ResetsMax(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordInvoke(1000)
	snap1 := c.Snapshot()
	assert.Equal(t, int64(1000), snap1.InvokeMaxUs)

	// Max should be reset after snapshot
	snap2 := c.Snapshot()
	assert.Equal(t, int64(0), snap2.InvokeMaxUs)

	// Count and total should persist (cumulative)
	assert.Equal(t, int64(1), snap2.InvokeCount)
	assert.Equal(t, int64(1000), snap2.InvokeTotalUs)
}

func TestCounters_Snapshot_CollectedAt(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	before := time.Now()
	snap := c.Snapshot()
	after := time.Now()

	require.False(t, snap.CollectedAt.IsZero())
	assert.False(t, snap.CollectedAt.Before(before))
	assert.False(t, snap.CollectedAt.After(after))
}

func TestCounters_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	const goroutines = 10
	const iterations = 1000

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				c.RecordInvoke(100)
			}
		})
	}
	wg.Wait()

	snap := c.Snapshot()
	assert.Equal(t, int64(goroutines*iterations), snap.InvokeCount)
}

func TestCounterMap_RecordInvoke(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	m.RecordInvoke("model_a", 500)
	m.RecordInvoke("model_a", 1200)
	m.RecordInvoke("model_b", 300)

	snaps := m.SnapshotAll()
	require.Len(t, snaps, 2)

	a := snaps["model_a"]
	assert.Equal(t, int64(2), a.InvokeCount)
	assert.Equal(t, int64(1700), a.InvokeTotalUs)
	assert.Equal(t, int64(1200), a.InvokeMaxUs)

	b := snaps["model_b"]
	assert.Equal(t, int64(1), b.InvokeCount)
	assert.Equal(t, int64(300), b.InvokeTotalUs)
}

func TestCounterMap_SnapshotAll_ResetsMax(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	m.RecordInvoke("model_a", 5000)
	snap1 := m.SnapshotAll()
	assert.Equal(t, int64(5000), snap1["model_a"].InvokeMaxUs)

	snap2 := m.SnapshotAll()
	assert.Equal(t, int64(0), snap2["model_a"].InvokeMaxUs)
	assert.Equal(t, int64(1), snap2["model_a"].InvokeCount)
	assert.Equal(t, int64(5000), snap2["model_a"].InvokeTotalUs)
}

func TestCounterMap_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	const goroutines = 10
	const iterations = 1000
	models := []string{"model_a", "model_b", "model_c"}

	var wg sync.WaitGroup
	for _, modelID := range models {
		for range goroutines {
			wg.Go(func() {
				for range iterations {
					m.RecordInvoke(modelID, 100)
				}
			})
		}
	}
	wg.Wait()

	snaps := m.SnapshotAll()
	require.Len(t, snaps, 3)
	for _, modelID := range models {
		assert.Equal(t, int64(goroutines*iterations), snaps[modelID].InvokeCount)
	}
}

func TestCounterMap_EmptySnapshot(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	snaps := m.SnapshotAll()
	assert.Empty(t, snaps)
}

func TestCounterMap_PeekAll_NonDestructive(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	m.RecordInvoke("model_a", 500)
	m.RecordInvoke("model_a", 1200)
	m.RecordInvoke("model_b", 300)

	peek1 := m.PeekAll()
	require.Len(t, peek1, 2)

	a := peek1["model_a"]
	assert.Equal(t, int64(2), a.InvokeCount)
	assert.Equal(t, int64(1700), a.InvokeTotalUs)
	assert.Equal(t, int64(1200), a.InvokeMaxUsLifetime)

	b := peek1["model_b"]
	assert.Equal(t, int64(1), b.InvokeCount)
	assert.Equal(t, int64(300), b.InvokeTotalUs)
	assert.Equal(t, int64(300), b.InvokeMaxUsLifetime)

	// PeekAll must NOT reset the lifetime max
	peek2 := m.PeekAll()
	assert.Equal(t, int64(1200), peek2["model_a"].InvokeMaxUsLifetime)
	assert.Equal(t, int64(300), peek2["model_b"].InvokeMaxUsLifetime)
}

func TestCounterMap_PeekAll_Empty(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	peek := m.PeekAll()
	assert.Empty(t, peek)
}

func TestCounterMap_PeekAll_RecentP95(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	// 100 samples with durations 1000, 2000, ... 100000 us.
	for k := int64(1); k <= 100; k++ {
		m.RecordInvoke("model_a", k*1000)
	}
	peek := m.PeekAll()["model_a"]
	// Nearest-rank p95 over 100 samples: idx = ceil(0.95*100)-1 = 94 (0-based),
	// which is the 95th smallest value = 95000 us.
	assert.Equal(t, int64(95_000), peek.RecentP95Us, "p95 of 1..100 (x1000) is the 95th value")
}

func TestCounterMap_PeekAll_RecentP95_Empty(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	m.RecordError("model_a") // no RecordInvoke, so no latency samples
	peek := m.PeekAll()["model_a"]
	assert.Equal(t, int64(0), peek.RecentP95Us, "p95 is zero with no recorded invocations")
}

func TestCounterMap_PeekAll_RecentP95_IgnoresOutliers(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	// 96 fast inferences plus 4 very slow ones (4% outliers, below the p95 cut).
	for range 96 {
		m.RecordInvoke("model_a", 1_000)
	}
	for range 4 {
		m.RecordInvoke("model_a", 8_000_000)
	}
	peek := m.PeekAll()["model_a"]
	// p95 (index 94 of 100) falls in the fast bucket, so the outliers do not move it.
	assert.Equal(t, int64(1_000), peek.RecentP95Us, "p95 ignores the slowest 5% of samples")
	// The lifetime max still captures the outlier for the model card.
	assert.Equal(t, int64(8_000_000), peek.InvokeMaxUsLifetime, "lifetime max still captures the outlier")
}

func TestCounterMap_PeekAll_RecentP95_EvictsOldSamples(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	// Fill the ring with slow samples, then overwrite the whole window with fast ones.
	for range latencyWindowSize {
		m.RecordInvoke("model_a", 9_000_000)
	}
	for range latencyWindowSize {
		m.RecordInvoke("model_a", 1_000)
	}
	peek := m.PeekAll()["model_a"]
	// Every slow sample has been evicted, so the rolling p95 reflects only the
	// recent fast window, not the all-time history.
	assert.Equal(t, int64(1_000), peek.RecentP95Us, "old slow samples must be evicted from the rolling window")
}

func TestRecentPercentileUs_Boundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		samples []int64
		p       float64
		want    int64
	}{
		{name: "empty returns zero", samples: nil, p: 0.95, want: 0},
		{name: "single sample", samples: []int64{42}, p: 0.95, want: 42},
		// ceil(0.95*7) = ceil(6.65) = 7 -> idx 6 -> the largest of 7.
		{name: "odd count nearest-rank", samples: []int64{10, 20, 30, 40, 50, 60, 70}, p: 0.95, want: 70},
		// ceil(1.0*4) = 4 -> idx 3 (upper clamp boundary) -> the largest.
		{name: "p100 upper boundary", samples: []int64{1, 2, 3, 4}, p: 1.0, want: 4},
		// ceil(0.5*5) = 3 -> idx 2 -> the median.
		{name: "median", samples: []int64{10, 20, 30, 40, 50}, p: 0.5, want: 30},
		// ceil(0.0*3) = 0 -> idx -1 -> clamped to 0 -> the smallest.
		{name: "p0 lower clamp", samples: []int64{5, 6, 7}, p: 0.0, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &Counters{}
			for _, s := range tt.samples {
				c.RecordInvoke(s)
			}
			assert.Equal(t, tt.want, c.recentPercentileUs(tt.p))
		})
	}
}

// referencePercentile is an independent nearest-rank implementation (mirroring
// the ring's last-latencyWindowSize retention) used to cross-check
// recentPercentileUs in the fuzz test below.
func referencePercentile(durations []int64, p float64) int64 {
	samples := slices.Clone(durations)
	if len(samples) > latencyWindowSize {
		samples = samples[len(samples)-latencyWindowSize:]
	}
	if len(samples) == 0 {
		return 0
	}
	slices.Sort(samples)
	n := len(samples)
	idx := int(math.Ceil(p*float64(n))) - 1
	idx = max(idx, 0)
	idx = min(idx, n-1)
	return samples[idx]
}

func FuzzRecentPercentileUs(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1})
	f.Add([]byte{5, 1, 9, 3, 7})
	f.Fuzz(func(t *testing.T, data []byte) {
		c := &Counters{}
		durations := make([]int64, len(data))
		for i, b := range data {
			// Map bytes to durations 1..256 so every sample is positive and distinct
			// values are possible; the ring keeps only the most recent samples.
			durations[i] = int64(b) + 1
			c.RecordInvoke(durations[i])
		}
		got := c.recentPercentileUs(healthLatencyPercentile)
		want := referencePercentile(durations, healthLatencyPercentile)
		require.Equal(t, want, got, "recentPercentileUs must match the nearest-rank reference")
	})
}

func TestRTFMetricKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		// Spaces and dots replaced, normalized form.
		{input: "BirdNET V2.4", want: "inference.BirdNET_V2_4.rtf"},
		// Already-clean id: must round-trip without modification.
		{input: "Perch_V2", want: "inference.Perch_V2.rtf"},
		// Empty string: produces "inference..rtf" (degenerate but must not panic).
		{input: "", want: "inference..rtf"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RTFMetricKey(tt.input))
		})
	}
}

func TestCounterMap_RecordError(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	m.RecordInvoke("birdnet", 1000)
	m.RecordError("birdnet")

	snap := m.PeekAll()["birdnet"]
	require.EqualValues(t, 1, snap.InvokeCount)
	require.EqualValues(t, 1, snap.InvokeErrors)
}

func TestCounters_RecordError(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordError()
	c.RecordError()

	peek := c.InvokeErrors.Load()
	assert.Equal(t, int64(2), peek)
}

func TestCounters_RecordError_NotResetOnSnapshot(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordError()
	snap := c.Snapshot()
	assert.Equal(t, int64(1), snap.InvokeErrors)

	// Second snapshot should still show cumulative count (no reset-on-read).
	snap2 := c.Snapshot()
	assert.Equal(t, int64(1), snap2.InvokeErrors)
}

func TestCounterMap_RecordError_NewModel(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	// RecordError on a model that has never had RecordInvoke called yet.
	m.RecordError("unknown")

	peek := m.PeekAll()["unknown"]
	require.EqualValues(t, 0, peek.InvokeCount)
	require.EqualValues(t, 1, peek.InvokeErrors)
}

func TestCounterMap_RecordError_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	const goroutines = 10
	const iterations = 1000

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				m.RecordError("model_a")
			}
		})
	}
	wg.Wait()

	peek := m.PeekAll()["model_a"]
	assert.Equal(t, int64(goroutines*iterations), peek.InvokeErrors)
}

func TestThroughputMetricKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{input: "BirdNET v2.4", want: "inference.BirdNET_v2_4.throughput"},
		{input: "Perch_V2", want: "inference.Perch_V2.throughput"},
		{input: "", want: "inference..throughput"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ThroughputMetricKey(tt.input))
		})
	}
}

func TestErrorRateMetricKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{input: "BirdNET v2.4", want: "inference.BirdNET_v2_4.error_rate"},
		{input: "Perch_V2", want: "inference.Perch_V2.error_rate"},
		{input: "", want: "inference..error_rate"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ErrorRateMetricKey(tt.input))
		})
	}
}

func TestCounterMap_PeekAll_DoesNotInterfereWithSnapshot(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	m.RecordInvoke("model_a", 1000)

	// PeekAll never reads or resets the collector's windowed max, so SnapshotAll
	// still sees the full windowed value.
	peek := m.PeekAll()
	assert.Equal(t, int64(1000), peek["model_a"].InvokeMaxUsLifetime)

	snap := m.SnapshotAll()
	assert.Equal(t, int64(1000), snap["model_a"].InvokeMaxUs)

	// SnapshotAll reset the collector's windowed max, but PeekAll's lifetime max
	// is never reset.
	peek2 := m.PeekAll()
	assert.Equal(t, int64(1000), peek2["model_a"].InvokeMaxUsLifetime)
	snap2 := m.SnapshotAll()
	assert.Equal(t, int64(0), snap2["model_a"].InvokeMaxUs)
}

// TestCounterMap_PeekAll_LifetimeMaxSurvivesSnapshotReset reproduces the model
// card "avg latency > max latency" bug: the status endpoint reads the max via
// PeekAll while the metrics collector resets the windowed max on every tick
// through SnapshotAll. A slow warm-up invocation inflates the lifetime average,
// but its peak was wiped by the collector, so the reported max fell below the
// average. PeekAll must report the lifetime peak so max >= avg always holds.
func TestCounterMap_PeekAll_LifetimeMaxSurvivesSnapshotReset(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}

	// A slow warm-up invocation sets the lifetime peak (2s).
	m.RecordInvoke("model_a", 2_000_000)

	// The collector ticks: SnapshotAll resets the windowed max on read.
	_ = m.SnapshotAll()

	// Steady-state invocations afterwards are much faster than the warm-up.
	m.RecordInvoke("model_a", 250_000)
	m.RecordInvoke("model_a", 260_000)

	peek := m.PeekAll()["model_a"]
	require.Positive(t, peek.InvokeCount)
	avgUs := peek.InvokeTotalUs / peek.InvokeCount

	// The lifetime max (used by the model card) survives the collector reset, so
	// it still reflects the warm-up peak and stays >= the lifetime average.
	assert.Equal(t, int64(2_000_000), peek.InvokeMaxUsLifetime,
		"lifetime max must report the warm-up peak, not the since-last-tick peak")
	assert.GreaterOrEqual(t, peek.InvokeMaxUsLifetime, avgUs,
		"model-card max latency must never be below average latency")
}
