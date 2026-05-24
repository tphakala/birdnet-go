package observability

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCollectorWithHealth(t *testing.T) (*Collector, *HealthMetricsStore, *HealthEventBuffer) {
	t.Helper()
	store := NewMemoryStore(100)
	healthStore := NewHealthMetricsStore()
	healthEvents := NewHealthEventBuffer(100)

	c := NewCollector(store, 15*time.Second, func() float64 { return 0 })
	c.SetHealthStore(healthStore)
	c.SetHealthEvents(healthEvents)
	return c, healthStore, healthEvents
}

func TestCollector_AudioHealthDelta(t *testing.T) {
	t.Parallel()
	c, healthStore, healthEvents := newTestCollectorWithHealth(t)

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 10, Errors: 2},
		}
	})

	c.collectHealthCounters()
	assert.Equal(t, int64(0), healthStore.LifetimeTotal("audio.drops.src1"))

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 15, Errors: 5},
		}
	})
	c.collectHealthCounters()
	assert.Equal(t, int64(5), healthStore.LifetimeTotal("audio.drops.src1"))
	assert.Equal(t, int64(3), healthStore.LifetimeTotal("audio.overruns.src1"))

	events := healthEvents.Recent("drops", 10)
	require.Len(t, events, 1)
	assert.Equal(t, int64(5), events[0].Delta)
}

func TestCollector_AudioHealthZeroDeltaSkip(t *testing.T) {
	t.Parallel()
	c, healthStore, healthEvents := newTestCollectorWithHealth(t)

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 10, Errors: 0},
		}
	})

	c.collectHealthCounters()
	c.collectHealthCounters()

	assert.Equal(t, int64(0), healthStore.LifetimeTotal("audio.drops.src1"))
	assert.Empty(t, healthEvents.RecentAll(10))
}

func TestCollector_AudioHealthCounterReset(t *testing.T) {
	t.Parallel()
	c, healthStore, _ := newTestCollectorWithHealth(t)

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 100, Errors: 0},
		}
	})
	c.collectHealthCounters()

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 5, Errors: 0},
		}
	})
	c.collectHealthCounters()

	assert.Equal(t, int64(5), healthStore.LifetimeTotal("audio.drops.src1"))
}

func TestCollector_StreamHealthDelta(t *testing.T) {
	t.Parallel()
	c, healthStore, healthEvents := newTestCollectorWithHealth(t)

	c.SetStreamHealth(func() []StreamHealthSnapshot {
		return []StreamHealthSnapshot{
			{URL: "rtsp://cam1", RestartCount: 2},
		}
	})
	c.collectHealthCounters()

	c.SetStreamHealth(func() []StreamHealthSnapshot {
		return []StreamHealthSnapshot{
			{URL: "rtsp://cam1", RestartCount: 5},
		}
	})
	c.collectHealthCounters()

	assert.Equal(t, int64(3), healthStore.LifetimeTotal("stream.restarts.rtsp://cam1"))

	events := healthEvents.Recent("restarts", 10)
	require.Len(t, events, 1)
	assert.Equal(t, int64(3), events[0].Delta)
}

func TestCollector_MultiSourceAudio(t *testing.T) {
	t.Parallel()
	c, healthStore, _ := newTestCollectorWithHealth(t)

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 0},
			{SourceID: "src2", Drops: 0},
		}
	})
	c.collectHealthCounters()

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 10},
			{SourceID: "src2", Drops: 20},
		}
	})
	c.collectHealthCounters()

	assert.Equal(t, int64(10), healthStore.LifetimeTotal("audio.drops.src1"))
	assert.Equal(t, int64(20), healthStore.LifetimeTotal("audio.drops.src2"))
}

func TestCollector_SourceRemoval(t *testing.T) {
	t.Parallel()
	c, healthStore, _ := newTestCollectorWithHealth(t)

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 10},
			{SourceID: "src2", Drops: 5},
		}
	})
	c.collectHealthCounters()

	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{
			{SourceID: "src1", Drops: 15},
		}
	})
	c.collectHealthCounters()

	assert.Equal(t, int64(5), healthStore.LifetimeTotal("audio.drops.src1"))
	assert.Equal(t, int64(0), healthStore.LifetimeTotal("audio.drops.src2"))
}

func TestCollector_NoHealthStoreSkips(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore(100)
	c := NewCollector(store, 15*time.Second, func() float64 { return 0 })
	c.SetAudioRouter(func() []AudioRouterSnapshot {
		return []AudioRouterSnapshot{{SourceID: "src1", Drops: 10}}
	})

	c.collectHealthCounters()
}
