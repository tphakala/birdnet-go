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
