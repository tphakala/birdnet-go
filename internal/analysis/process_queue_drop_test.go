// process_queue_drop_test.go verifies that results-queue overflow drops are
// counted rather than silently discarded. Before this accounting existed, a full
// results queue dropped detections with no durable signal of how much data was
// lost.
package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestRecordResultsQueueDrop_CountsDrops confirms that each drop increments the
// cumulative counter and that the returned total reflects it. A nil metrics
// pointer is passed to exercise the safe-no-op metrics path.
func TestRecordResultsQueueDrop_CountsDrops(t *testing.T) {
	// Not parallel: mutates the package-global droppedDetectionsTotal counter.
	before := droppedDetectionsTotal.Load()
	t.Cleanup(func() { droppedDetectionsTotal.Store(before) })

	got := recordResultsQueueDrop("test-source", "test-model", nil)
	assert.Equal(t, before+1, got, "first drop should return previous total + 1")
	assert.Equal(t, before+1, droppedDetectionsTotal.Load(), "counter should reflect the increment")

	got2 := recordResultsQueueDrop("test-source", "test-model", nil)
	assert.Equal(t, before+2, got2, "second drop should return previous total + 2")
	assert.Equal(t, before+2, droppedDetectionsTotal.Load())
}

// TestDroppedDetectionsTotal_MonotonicUnderConcurrency verifies the counter is
// safe to increment concurrently (it is hit from per-source goroutines) and that
// every drop is accounted for with no lost updates.
func TestDroppedDetectionsTotal_MonotonicUnderConcurrency(t *testing.T) {
	// Not parallel: mutates the package-global counter.
	before := droppedDetectionsTotal.Load()
	t.Cleanup(func() { droppedDetectionsTotal.Store(before) })

	const goroutines = 16
	const dropsEach = 64

	done := make(chan struct{})
	for range goroutines {
		go func() {
			defer func() { done <- struct{}{} }()
			for range dropsEach {
				recordResultsQueueDrop("concurrent-source", "concurrent-model", nil)
			}
		}()
	}
	for range goroutines {
		<-done
	}

	assert.Equal(t, before+int64(goroutines*dropsEach), droppedDetectionsTotal.Load(),
		"every concurrent drop must be counted with no lost updates")
}

// TestRecordResultsQueueDrop_SurfacesOnHealthStore verifies that, once the
// diagnostics health sink is wired, each drop is recorded into the health
// metrics store and event buffer with the "queue_drops" metric label that
// ResultsQueueDropCheck filters on. Clearing the sink stops further recording.
func TestRecordResultsQueueDrop_SurfacesOnHealthStore(t *testing.T) {
	// Not parallel: mutates package-global drop counter and health sink.
	beforeCount := droppedDetectionsTotal.Load()
	prevSink := resultsQueueDropHealthSink.Load()
	t.Cleanup(func() {
		droppedDetectionsTotal.Store(beforeCount)
		resultsQueueDropHealthSink.Store(prevSink)
	})

	store := observability.NewHealthMetricsStore()
	buf := observability.NewHealthEventBuffer(observability.DefaultEventBufferCapacity)
	SetResultsQueueDropHealthSink(store, buf)

	const source = "sink-source"
	recordResultsQueueDrop(source, "sink-model", nil)

	key := observability.MetricPrefixResultsQueueDrops + source
	assert.Equal(t, int64(1), store.Sum(key, time.Hour), "drop must be recorded into the health store")

	events := buf.Recent(observability.MetricTypeResultsQueueDrops, 10)
	require.Len(t, events, 1, "drop must be recorded as a single health event")
	assert.Equal(t, source, events[0].Source)
	assert.Equal(t, int64(1), events[0].Delta)
	assert.Equal(t, observability.MetricTypeResultsQueueDrops, events[0].Metric)

	// Clearing the sink must stop further recording without panicking.
	SetResultsQueueDropHealthSink(nil, nil)
	recordResultsQueueDrop(source, "sink-model", nil)
	assert.Equal(t, int64(1), store.Sum(key, time.Hour), "no further drops should be recorded after the sink is cleared")
}

// TestRecordResultsQueueDrop_NilEventsBufferTolerated verifies that a sink wired
// with a store but no event buffer still records windowed counts and does not
// panic on the drop path.
func TestRecordResultsQueueDrop_NilEventsBufferTolerated(t *testing.T) {
	// Not parallel: mutates package-global drop counter and health sink.
	beforeCount := droppedDetectionsTotal.Load()
	prevSink := resultsQueueDropHealthSink.Load()
	t.Cleanup(func() {
		droppedDetectionsTotal.Store(beforeCount)
		resultsQueueDropHealthSink.Store(prevSink)
	})

	store := observability.NewHealthMetricsStore()
	SetResultsQueueDropHealthSink(store, nil)

	const source = "nil-buf-source"
	require.NotPanics(t, func() {
		recordResultsQueueDrop(source, "sink-model", nil)
	})
	assert.Equal(t, int64(1), store.Sum(observability.MetricPrefixResultsQueueDrops+source, time.Hour))
}
