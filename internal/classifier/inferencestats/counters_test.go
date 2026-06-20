package inferencestats

import (
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
	assert.Equal(t, int64(1200), a.InvokeMaxUs)

	b := peek1["model_b"]
	assert.Equal(t, int64(1), b.InvokeCount)
	assert.Equal(t, int64(300), b.InvokeTotalUs)
	assert.Equal(t, int64(300), b.InvokeMaxUs)

	// PeekAll must NOT reset InvokeMaxUs
	peek2 := m.PeekAll()
	assert.Equal(t, int64(1200), peek2["model_a"].InvokeMaxUs)
	assert.Equal(t, int64(300), peek2["model_b"].InvokeMaxUs)
}

func TestCounterMap_PeekAll_Empty(t *testing.T) {
	t.Parallel()
	m := &CounterMap{}
	peek := m.PeekAll()
	assert.Empty(t, peek)
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

	// PeekAll is non-destructive: it does not reset the windowed max that the
	// collector consumes through SnapshotAll.
	peek := m.PeekAll()
	assert.Equal(t, int64(1000), peek["model_a"].InvokeMaxUs)

	snap := m.SnapshotAll()
	assert.Equal(t, int64(1000), snap["model_a"].InvokeMaxUs)

	// SnapshotAll resets the windowed max, so PeekAll's windowed InvokeMaxUs is
	// back to zero, but the lifetime max survives the reset.
	peek2 := m.PeekAll()
	assert.Equal(t, int64(0), peek2["model_a"].InvokeMaxUs)
	assert.Equal(t, int64(1000), peek2["model_a"].InvokeMaxUsLifetime)
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

	// The windowed max (used by the latency health check) was reset by the tick,
	// so it reflects only the recent steady-state peak, not the warm-up spike.
	// This is what keeps a one-time warm-up from latching the health check.
	assert.Equal(t, int64(260_000), peek.InvokeMaxUs,
		"windowed max must reflect only invocations since the last collector tick")
}
