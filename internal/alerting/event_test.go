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
	var received *AlertEvent

	bus.Subscribe(func(event *AlertEvent) {
		received = event
	})

	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{"stream_name": "backyard-cam"},
		Timestamp:  time.Now(),
	}

	bus.Publish(event)

	require.NotNil(t, received)
	assert.Equal(t, ObjectTypeStream, received.ObjectType)
	assert.Equal(t, EventStreamDisconnected, received.EventName)
	assert.Equal(t, "backyard-cam", received.Properties["stream_name"])
}

func TestAlertEventBus_MultipleHandlers(t *testing.T) {
	bus := NewAlertEventBus()
	var count atomic.Int32

	for range 3 {
		bus.Subscribe(func(_ *AlertEvent) {
			count.Add(1)
		})
	}

	bus.Publish(&AlertEvent{ObjectType: ObjectTypeDetection, EventName: EventDetectionNewSpecies})

	assert.Equal(t, int32(3), count.Load())
}

func TestAlertEventBus_PublishWithNoHandlers(t *testing.T) {
	bus := NewAlertEventBus()
	// Should not panic
	bus.Publish(&AlertEvent{ObjectType: ObjectTypeSystem, MetricName: MetricCPUUsage})
}

func TestAlertEventBus_PublishSetsTimestamp(t *testing.T) {
	bus := NewAlertEventBus()
	var received *AlertEvent

	bus.Subscribe(func(event *AlertEvent) {
		received = event
	})

	before := time.Now()
	bus.Publish(&AlertEvent{ObjectType: ObjectTypeDetection})
	after := time.Now()

	require.NotNil(t, received)
	assert.False(t, received.Timestamp.IsZero())
	assert.False(t, received.Timestamp.Before(before))
	assert.False(t, received.Timestamp.After(after))
}

func TestAlertEventBus_ConcurrentPublish(t *testing.T) {
	bus := NewAlertEventBus()
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

	assert.Equal(t, int32(100), count.Load())
}
