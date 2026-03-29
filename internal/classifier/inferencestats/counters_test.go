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
