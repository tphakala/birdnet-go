package notification

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBurstTracker_FirstErrorAllowed(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	action := bt.Record("securefs", "file-io", "file not found")
	assert.Equal(t, BurstActionAllow, action)
}

func TestBurstTracker_BelowThresholdAllowed(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "error 1")
	bt.Record("securefs", "file-io", "error 2")
	action := bt.Record("securefs", "file-io", "error 3")
	assert.Equal(t, BurstActionAllow, action, "at threshold count should still allow")
}

func TestBurstTracker_SummaryAtThresholdPlusOne(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "error 1")
	bt.Record("securefs", "file-io", "error 2")
	bt.Record("securefs", "file-io", "error 3")
	action := bt.Record("securefs", "file-io", "error 4")
	assert.Equal(t, BurstActionSummary, action)
}

func TestBurstTracker_SuppressAfterSummary(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	for range 4 {
		bt.Record("securefs", "file-io", "error")
	}
	action := bt.Record("securefs", "file-io", "error 5")
	assert.Equal(t, BurstActionSuppress, action)
}

func TestBurstTracker_WindowReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { //nolint:thelper // synctest callback, not a test helper
		bt := NewErrorBurstTracker(3, 5*time.Minute)
		for range 4 {
			bt.Record("securefs", "file-io", "error")
		}
		// Advance past the 5-minute window using synctest's fake clock.
		time.Sleep(6 * time.Minute)

		// After window expires, next error should be allowed again.
		action := bt.Record("securefs", "file-io", "new error")
		assert.Equal(t, BurstActionAllow, action)
	})
}

func TestBurstTracker_DifferentKeysIndependent(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	for range 4 {
		bt.Record("securefs", "file-io", "error")
	}

	action := bt.Record("mqtt", "connection", "broker unreachable")
	assert.Equal(t, BurstActionAllow, action)
}

func TestBurstTracker_GetSummary(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "first error msg")
	bt.Record("securefs", "file-io", "second error")
	bt.Record("securefs", "file-io", "third error")
	bt.Record("securefs", "file-io", "fourth error")

	summary := bt.GetSummary("securefs", "file-io")
	assert.Equal(t, 4, summary.Count)
	assert.Equal(t, "first error msg", summary.SampleError)
	assert.Equal(t, "securefs", summary.Component)
	assert.Equal(t, "file-io", summary.Category)
}
