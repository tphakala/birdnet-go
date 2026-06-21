package observability

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestNewMemoryStore(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(100)

	require.NotNil(t, store)
	assert.Equal(t, 100, store.maxPoints)
	assert.Empty(t, store.Names())
	assert.Empty(t, store.GetAll(100))
	assert.Empty(t, store.GetLatest())
}

func TestMemoryStore_RecordBatch_And_Get(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)

	store.RecordBatch(map[string]float64{
		"cpu.total":           45.2,
		"memory.used_percent": 62.1,
	})

	// Get single metric
	points := store.Get("cpu.total", 10)
	require.Len(t, points, 1)
	assert.InDelta(t, 45.2, points[0].Value, 0.01)
	assert.False(t, points[0].Timestamp.IsZero())

	// Non-existent metric
	assert.Nil(t, store.Get("nonexistent", 10))
}

func TestMemoryStore_CircularOverwrite(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(3)

	// Write 5 values; buffer capacity is 3, so first 2 should be evicted
	for i := range 5 {
		store.RecordBatch(map[string]float64{"m": float64(i)})
	}

	points := store.Get("m", 10)
	require.Len(t, points, 3)
	// Should contain values 2, 3, 4 in chronological order
	assert.InDelta(t, 2.0, points[0].Value, 0.01)
	assert.InDelta(t, 3.0, points[1].Value, 0.01)
	assert.InDelta(t, 4.0, points[2].Value, 0.01)
}

func TestMemoryStore_Get_LimitedPoints(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)

	for i := range 5 {
		store.RecordBatch(map[string]float64{"m": float64(i)})
	}

	// Request only 2 most recent points
	points := store.Get("m", 2)
	require.Len(t, points, 2)
	assert.InDelta(t, 3.0, points[0].Value, 0.01)
	assert.InDelta(t, 4.0, points[1].Value, 0.01)
}

func TestMemoryStore_GetAll(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)

	store.RecordBatch(map[string]float64{
		"a": 1.0,
		"b": 2.0,
	})
	store.RecordBatch(map[string]float64{
		"a": 3.0,
		"b": 4.0,
	})

	all := store.GetAll(10)
	require.Len(t, all, 2)
	assert.Len(t, all["a"], 2)
	assert.Len(t, all["b"], 2)
	assert.InDelta(t, 3.0, all["a"][1].Value, 0.01)
}

func TestMemoryStore_GetLatest(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)

	store.RecordBatch(map[string]float64{"cpu": 10.0, "mem": 50.0})
	store.RecordBatch(map[string]float64{"cpu": 20.0, "mem": 60.0})

	latest := store.GetLatest()
	require.Len(t, latest, 2)
	assert.InDelta(t, 20.0, latest["cpu"].Value, 0.01)
	assert.InDelta(t, 60.0, latest["mem"].Value, 0.01)
}

func TestMemoryStore_Names(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)

	store.RecordBatch(map[string]float64{
		"cpu.total":           1.0,
		"memory.used_percent": 2.0,
		"cpu.temperature":     3.0,
	})

	names := store.Names()
	// Names should be sorted
	require.Len(t, names, 3)
	assert.Equal(t, "cpu.temperature", names[0])
	assert.Equal(t, "cpu.total", names[1])
	assert.Equal(t, "memory.used_percent", names[2])
}

func TestMemoryStore_Subscribe(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)
		ch, cancel := store.Subscribe()
		t.Cleanup(cancel)

		// Record a batch; subscriber should receive it
		store.RecordBatch(map[string]float64{"cpu": 42.0})

		select {
		case snapshot := <-ch:
			require.Contains(t, snapshot, "cpu")
			assert.InDelta(t, 42.0, snapshot["cpu"].Value, 0.01)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for subscriber notification")
		}
	})
}

func TestMemoryStore_Subscribe_Cancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)
		ch, cancel := store.Subscribe()

		// Cancel the subscription
		cancel()

		// Record a batch; cancelled subscriber should NOT receive it
		store.RecordBatch(map[string]float64{"cpu": 99.0})

		select {
		case <-ch:
			t.Fatal("cancelled subscriber should not receive data")
		case <-time.After(100 * time.Millisecond):
			// Expected: no data received
		}
	})
}

func TestMemoryStore_Subscribe_MultipleSubscribers(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)

		ch1, cancel1 := store.Subscribe()
		t.Cleanup(cancel1)
		ch2, cancel2 := store.Subscribe()
		t.Cleanup(cancel2)

		store.RecordBatch(map[string]float64{"cpu": 55.0})

		// Both subscribers should receive the data
		for _, ch := range []<-chan map[string]MetricPoint{ch1, ch2} {
			select {
			case snapshot := <-ch:
				require.Contains(t, snapshot, "cpu")
				assert.InDelta(t, 55.0, snapshot["cpu"].Value, 0.01)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for subscriber notification")
			}
		}
	})
}

func TestMemoryStore_Subscribe_SlowConsumerDrops(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)
		ch, cancel := store.Subscribe()
		t.Cleanup(cancel)

		// Don't read from channel — simulate slow consumer
		// Record 3 batches; channel cap is 1, so at most 1 is buffered
		store.RecordBatch(map[string]float64{"cpu": 1.0})
		store.RecordBatch(map[string]float64{"cpu": 2.0})
		store.RecordBatch(map[string]float64{"cpu": 3.0})

		// Should get only the first buffered one
		select {
		case snapshot := <-ch:
			require.Contains(t, snapshot, "cpu")
			// We should get the first value that was buffered (1.0)
			assert.InDelta(t, 1.0, snapshot["cpu"].Value, 0.01)
		case <-time.After(time.Second):
			t.Fatal("should have received at least one value")
		}
	})
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store := NewMemoryStore(100)

	var wg sync.WaitGroup
	// Concurrent writers
	for range 10 {
		wg.Go(func() {
			for range 100 {
				store.RecordBatch(map[string]float64{
					"cpu": 50.0,
					"mem": 70.0,
				})
			}
		})
	}

	// Concurrent readers
	for range 5 {
		wg.Go(func() {
			for range 100 {
				_ = store.Get("cpu", 10)
				_ = store.GetAll(10)
				_ = store.GetLatest()
				_ = store.Names()
			}
		})
	}

	// Concurrent subscribe/cancel
	for range 5 {
		wg.Go(func() {
			for range 20 {
				_, cancel := store.Subscribe()
				cancel()
			}
		})
	}

	wg.Wait()

	// Should have data after all goroutines complete
	assert.NotEmpty(t, store.Names())
}

func TestMemoryStore_TopologyBroadcast(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)
		ch, cancel := store.SubscribeTopology()
		t.Cleanup(cancel)

		// Broadcast; the subscriber should receive within a short deadline.
		store.BroadcastTopologyChanged()

		select {
		case <-ch:
			// Expected: signal received.
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for topology signal")
		}
	})
}

func TestMemoryStore_TopologyBroadcast_Cancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)
		ch, cancel := store.SubscribeTopology()

		// Cancel the subscription, then broadcast: no panic, no receive.
		cancel()
		store.BroadcastTopologyChanged()

		select {
		case <-ch:
			t.Fatal("cancelled subscriber should not receive a topology signal")
		case <-time.After(100 * time.Millisecond):
			// Expected: no signal received.
		}
	})
}

func TestMemoryStore_TopologyBroadcast_Coalesces(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(10)
		ch, cancel := store.SubscribeTopology()
		t.Cleanup(cancel)

		// Three broadcasts without a consumer; cap is 1, so they coalesce.
		store.BroadcastTopologyChanged()
		store.BroadcastTopologyChanged()
		store.BroadcastTopologyChanged()

		// First receive succeeds.
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("expected at least one coalesced topology signal")
		}

		// No second buffered signal remains.
		select {
		case <-ch:
			t.Fatal("topology signals should coalesce to a single pending value")
		case <-time.After(100 * time.Millisecond):
			// Expected: nothing more buffered.
		}
	})
}

func TestMemoryStore_TopologyBroadcast_NoSubscribers(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(10)

	// Broadcasting with no subscribers must not panic.
	assert.NotPanics(t, store.BroadcastTopologyChanged)
}

func TestRingBuffer_EmptyRead(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(5)

	assert.Nil(t, rb.read(10))

	_, ok := rb.latest()
	assert.False(t, ok)
}

func TestRingBuffer_ExactCapacity(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(3)

	now := time.Now()
	for i := range 3 {
		rb.write(MetricPoint{Timestamp: now.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}

	points := rb.read(3)
	require.Len(t, points, 3)
	assert.InDelta(t, 0.0, points[0].Value, 0.01)
	assert.InDelta(t, 1.0, points[1].Value, 0.01)
	assert.InDelta(t, 2.0, points[2].Value, 0.01)
}

func TestRingBuffer_WrapAround(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(3)

	now := time.Now()
	// Write 7 values into a cap-3 buffer
	for i := range 7 {
		rb.write(MetricPoint{Timestamp: now.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}

	points := rb.read(3)
	require.Len(t, points, 3)
	// Should contain 4, 5, 6
	assert.InDelta(t, 4.0, points[0].Value, 0.01)
	assert.InDelta(t, 5.0, points[1].Value, 0.01)
	assert.InDelta(t, 6.0, points[2].Value, 0.01)
}

// Compile-time check that MemoryStore implements MetricsStore.
var _ MetricsStore = (*MemoryStore)(nil)
