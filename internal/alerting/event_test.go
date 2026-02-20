package alerting

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertEventBus_SubscribeAndPublish(t *testing.T) {
	bus := NewAlertEventBus()
	defer bus.Stop()

	var received atomic.Pointer[AlertEvent]

	bus.Subscribe(func(event *AlertEvent) {
		received.Store(event)
	})

	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{"stream_name": "backyard-cam"},
		Timestamp:  time.Now(),
	}

	bus.Publish(event)

	require.Eventually(t, func() bool { return received.Load() != nil }, time.Second, 5*time.Millisecond)
	got := received.Load()
	assert.Equal(t, ObjectTypeStream, got.ObjectType)
	assert.Equal(t, EventStreamDisconnected, got.EventName)
	assert.Equal(t, "backyard-cam", got.Properties["stream_name"])
}

func TestAlertEventBus_MultipleHandlers(t *testing.T) {
	bus := NewAlertEventBus()
	defer bus.Stop()

	var count atomic.Int32

	for range 3 {
		bus.Subscribe(func(_ *AlertEvent) {
			count.Add(1)
		})
	}

	bus.Publish(&AlertEvent{ObjectType: ObjectTypeDetection, EventName: EventDetectionNewSpecies})

	assert.Eventually(t, func() bool { return count.Load() == 3 }, time.Second, 5*time.Millisecond)
}

func TestAlertEventBus_PublishWithNoHandlers(t *testing.T) {
	bus := NewAlertEventBus()
	defer bus.Stop()
	// Should not panic
	bus.Publish(&AlertEvent{ObjectType: ObjectTypeSystem, MetricName: MetricCPUUsage})
}

func TestAlertEventBus_PublishSetsTimestamp(t *testing.T) {
	bus := NewAlertEventBus()
	defer bus.Stop()

	var received atomic.Pointer[AlertEvent]

	bus.Subscribe(func(event *AlertEvent) {
		received.Store(event)
	})

	before := time.Now()
	bus.Publish(&AlertEvent{ObjectType: ObjectTypeDetection})

	require.Eventually(t, func() bool { return received.Load() != nil }, time.Second, 5*time.Millisecond)
	got := received.Load()
	assert.False(t, got.Timestamp.IsZero())
	assert.False(t, got.Timestamp.Before(before))
}

func TestAlertEventBus_ConcurrentPublish(t *testing.T) {
	bus := NewAlertEventBus()
	defer bus.Stop()

	var count atomic.Int32

	bus.Subscribe(func(_ *AlertEvent) {
		count.Add(1)
	})

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			bus.Publish(&AlertEvent{ObjectType: ObjectTypeDetection})
		})
	}
	wg.Wait()

	assert.Eventually(t, func() bool { return count.Load() == 100 }, time.Second, 5*time.Millisecond)
}
