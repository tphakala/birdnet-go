package notification

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBurstTracker_FirstErrorAllowed(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	action, summary := bt.Record("securefs", "file-io", "file not found")
	assert.Equal(t, BurstActionAllow, action)
	assert.Nil(t, summary)
}

func TestBurstTracker_BelowThresholdAllowed(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "error 1")
	bt.Record("securefs", "file-io", "error 2")
	action, _ := bt.Record("securefs", "file-io", "error 3")
	assert.Equal(t, BurstActionAllow, action, "at threshold count should still allow")
}

func TestBurstTracker_SummaryAtThresholdPlusOne(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "error 1")
	bt.Record("securefs", "file-io", "error 2")
	bt.Record("securefs", "file-io", "error 3")
	action, summary := bt.Record("securefs", "file-io", "error 4")
	assert.Equal(t, BurstActionSummary, action)
	require.NotNil(t, summary, "summary should be returned with BurstActionSummary")
	assert.Equal(t, 4, summary.Count)
	assert.Equal(t, "error 1", summary.SampleError)
	assert.Equal(t, "securefs", summary.Component)
	assert.Equal(t, "file-io", summary.Category)
}

func TestBurstTracker_SuppressAfterSummary(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	for range 4 {
		bt.Record("securefs", "file-io", "error")
	}
	action, summary := bt.Record("securefs", "file-io", "error 5")
	assert.Equal(t, BurstActionSuppress, action)
	assert.Nil(t, summary)
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
		action, _ := bt.Record("securefs", "file-io", "new error")
		assert.Equal(t, BurstActionAllow, action)
	})
}

func TestBurstTracker_DifferentKeysIndependent(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	for range 4 {
		bt.Record("securefs", "file-io", "error")
	}

	action, _ := bt.Record("mqtt", "connection", "broker unreachable")
	assert.Equal(t, BurstActionAllow, action)
}
