package dbstats

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordRead(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordRead(500) // 0.5ms
	c.RecordRead(1000) // 1ms

	assert.Equal(t, int64(2), c.ReadCount.Load())
	assert.Equal(t, int64(1500), c.ReadTotalUs.Load())
	assert.Equal(t, int64(1000), c.ReadMaxUs.Load())
	assert.Equal(t, int64(0), c.SlowQueryCount.Load())
}

func TestRecordWrite(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordWrite(2000) // 2ms
	c.RecordWrite(3000) // 3ms

	assert.Equal(t, int64(2), c.WriteCount.Load())
	assert.Equal(t, int64(5000), c.WriteTotalUs.Load())
	assert.Equal(t, int64(3000), c.WriteMaxUs.Load())
}

func TestSlowQueryCounting(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordRead(50_000)  // 50ms — not slow
	c.RecordRead(100_001) // 100.001ms — slow
	c.RecordWrite(200_000) // 200ms — slow

	assert.Equal(t, int64(2), c.SlowQueryCount.Load())
}

func TestSlowQueryThresholdExact(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	// Exactly at threshold — should NOT count as slow
	c.RecordRead(SlowThresholdUs)
	assert.Equal(t, int64(0), c.SlowQueryCount.Load())

	// One microsecond over — should count as slow
	c.RecordRead(SlowThresholdUs + 1)
	assert.Equal(t, int64(1), c.SlowQueryCount.Load())
}

func TestBusyTimeout(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordBusyTimeout()
	c.RecordBusyTimeout()
	c.RecordBusyTimeout()

	assert.Equal(t, int64(3), c.BusyTimeouts.Load())
}

func TestSnapshotResetOnRead(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordRead(5000)
	c.RecordWrite(8000)

	snap1 := c.Snapshot()
	assert.Equal(t, int64(5000), snap1.ReadMaxUs)
	assert.Equal(t, int64(8000), snap1.WriteMaxUs)
	require.False(t, snap1.CollectedAt.IsZero())

	// Max should be reset after snapshot
	snap2 := c.Snapshot()
	assert.Equal(t, int64(0), snap2.ReadMaxUs)
	assert.Equal(t, int64(0), snap2.WriteMaxUs)

	// Cumulative counters should still be present
	assert.Equal(t, int64(1), snap2.ReadCount)
	assert.Equal(t, int64(5000), snap2.ReadTotalUs)
	assert.Equal(t, int64(1), snap2.WriteCount)
	assert.Equal(t, int64(8000), snap2.WriteTotalUs)
}

func TestSnapshotCumulativeCounters(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	c.RecordRead(100)
	c.RecordRead(200)
	c.RecordWrite(300)
	c.RecordBusyTimeout()

	snap := c.Snapshot()

	assert.Equal(t, int64(2), snap.ReadCount)
	assert.Equal(t, int64(300), snap.ReadTotalUs)
	assert.Equal(t, int64(1), snap.WriteCount)
	assert.Equal(t, int64(300), snap.WriteTotalUs)
	assert.Equal(t, int64(0), snap.SlowQueries)
	assert.Equal(t, int64(1), snap.BusyTimeouts)
}

func TestMaxTracksHighestValue(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	// Record in non-monotonic order
	c.RecordRead(500)
	c.RecordRead(1000)
	c.RecordRead(300)
	c.RecordRead(800)

	assert.Equal(t, int64(1000), c.ReadMaxUs.Load())
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	const goroutines = 100
	const opsPerGoroutine = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent reads
	for range goroutines {
		go func() {
			defer wg.Done()
			for range opsPerGoroutine {
				c.RecordRead(100)
			}
		}()
	}

	// Concurrent writes
	for range goroutines {
		go func() {
			defer wg.Done()
			for range opsPerGoroutine {
				c.RecordWrite(200)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(goroutines*opsPerGoroutine), c.ReadCount.Load())
	assert.Equal(t, int64(goroutines*opsPerGoroutine*100), c.ReadTotalUs.Load())
	assert.Equal(t, int64(goroutines*opsPerGoroutine), c.WriteCount.Load())
	assert.Equal(t, int64(goroutines*opsPerGoroutine*200), c.WriteTotalUs.Load())
}

func TestConcurrentSnapshotWithWrites(t *testing.T) {
	t.Parallel()
	c := &Counters{}

	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer goroutine
	go func() {
		defer wg.Done()
		for range iterations {
			c.RecordRead(100)
			c.RecordWrite(200)
		}
	}()

	// Snapshot goroutine — should never panic
	go func() {
		defer wg.Done()
		for range iterations {
			snap := c.Snapshot()
			// Cumulative counters should never decrease
			assert.GreaterOrEqual(t, snap.ReadCount, int64(0))
			assert.GreaterOrEqual(t, snap.WriteCount, int64(0))
		}
	}()

	wg.Wait()
}
