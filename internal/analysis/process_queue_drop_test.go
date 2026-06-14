// process_queue_drop_test.go verifies that results-queue overflow drops are
// counted rather than silently discarded. Before this accounting existed, a full
// results queue dropped detections with no durable signal of how much data was
// lost.
package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRecordResultsQueueDrop_CountsDrops confirms that each drop increments the
// cumulative counter and that the returned total reflects it. A nil metrics
// pointer is passed to exercise the safe-no-op metrics path.
func TestRecordResultsQueueDrop_CountsDrops(t *testing.T) {
	// Not parallel: mutates the package-global droppedDetectionsTotal counter.
	before := droppedDetectionsTotal.Load()

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
