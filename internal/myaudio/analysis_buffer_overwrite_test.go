package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTracker creates a fresh overwriteTracker for testing.
func newTestTracker(t *testing.T) *overwriteTracker {
	t.Helper()
	return &overwriteTracker{
		windowStart: time.Now(),
	}
}

// TestOverwriteTracker_WindowReset verifies that the sliding window resets
// after the configured duration expires, clearing all counters.
func TestOverwriteTracker_WindowReset(t *testing.T) {
	t.Parallel()

	tracker := newTestTracker(t)
	sourceID := "test-window-reset"

	// Register the tracker
	overwriteTrackersMu.Lock()
	overwriteTrackers[sourceID] = tracker
	overwriteTrackersMu.Unlock()
	t.Cleanup(func() {
		overwriteTrackersMu.Lock()
		delete(overwriteTrackers, sourceID)
		overwriteTrackersMu.Unlock()
	})

	// Record enough writes to exceed minimum threshold
	writeCount := int64(overwriteMinWrites + 10)
	for range writeCount {
		trackOverwrite(sourceID, true)
	}

	tracker.mu.Lock()
	assert.Equal(t, writeCount, tracker.totalWrites)
	assert.Equal(t, writeCount, tracker.overwriteCount)
	// Force window to expire
	tracker.windowStart = time.Now().Add(-(overwriteWindowDuration + time.Second))
	tracker.mu.Unlock()

	// Next write should reset the window
	trackOverwrite(sourceID, false)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Equal(t, int64(1), tracker.totalWrites, "window should have been reset")
	assert.Equal(t, int64(0), tracker.overwriteCount, "overwrite count should be 0 after reset")
}

// TestOverwriteTracker_BelowThreshold verifies that no notification is
// triggered when the overwrite rate is below the configured threshold.
func TestOverwriteTracker_BelowThreshold(t *testing.T) {
	t.Parallel()

	tracker := newTestTracker(t)
	sourceID := "test-below-threshold"

	overwriteTrackersMu.Lock()
	overwriteTrackers[sourceID] = tracker
	overwriteTrackersMu.Unlock()
	t.Cleanup(func() {
		overwriteTrackersMu.Lock()
		delete(overwriteTrackers, sourceID)
		overwriteTrackersMu.Unlock()
	})

	// Write 100 times with only 5 overwrites (5% < 10% threshold)
	for range 95 {
		trackOverwrite(sourceID, false)
	}
	for range 5 {
		trackOverwrite(sourceID, true)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Equal(t, int64(100), tracker.totalWrites)
	assert.Equal(t, int64(5), tracker.overwriteCount)
	// lastNotified should still be zero since threshold wasn't reached
	assert.True(t, tracker.lastNotified.IsZero(), "should not have notified when below threshold")
}

// TestOverwriteTracker_AboveThreshold verifies that the lastNotified timestamp
// is set when the overwrite rate exceeds the threshold.
func TestOverwriteTracker_AboveThreshold(t *testing.T) {
	t.Parallel()

	tracker := newTestTracker(t)
	sourceID := "test-above-threshold"

	overwriteTrackersMu.Lock()
	overwriteTrackers[sourceID] = tracker
	overwriteTrackersMu.Unlock()
	t.Cleanup(func() {
		overwriteTrackersMu.Lock()
		delete(overwriteTrackers, sourceID)
		overwriteTrackersMu.Unlock()
	})

	// Write 100 times with 50 overwrites (50% > 10% threshold)
	for range 50 {
		trackOverwrite(sourceID, false)
	}
	for range 50 {
		trackOverwrite(sourceID, true)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Equal(t, int64(100), tracker.totalWrites)
	assert.Equal(t, int64(50), tracker.overwriteCount)
	// lastNotified should be set since threshold was exceeded
	assert.False(t, tracker.lastNotified.IsZero(), "should have triggered notification when above threshold")
}

// TestOverwriteTracker_Cooldown verifies that duplicate notifications are
// suppressed within the cooldown period.
func TestOverwriteTracker_Cooldown(t *testing.T) {
	t.Parallel()

	tracker := newTestTracker(t)
	sourceID := "test-cooldown"

	overwriteTrackersMu.Lock()
	overwriteTrackers[sourceID] = tracker
	overwriteTrackersMu.Unlock()
	t.Cleanup(func() {
		overwriteTrackersMu.Lock()
		delete(overwriteTrackers, sourceID)
		overwriteTrackersMu.Unlock()
	})

	// Trigger first notification
	for range 100 {
		trackOverwrite(sourceID, true)
	}

	tracker.mu.Lock()
	firstNotify := tracker.lastNotified
	require.False(t, firstNotify.IsZero(), "first notification should have fired")
	tracker.mu.Unlock()

	// Reset window but keep lastNotified within cooldown
	tracker.mu.Lock()
	tracker.totalWrites = 0
	tracker.overwriteCount = 0
	tracker.windowStart = time.Now()
	tracker.mu.Unlock()

	// Generate more overwrites — should NOT update lastNotified
	for range 100 {
		trackOverwrite(sourceID, true)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Equal(t, firstNotify, tracker.lastNotified,
		"lastNotified should not change during cooldown period")
}

// TestOverwriteTracker_MinWrites verifies that no notification is sent when
// the total number of writes is below the minimum required sample size.
func TestOverwriteTracker_MinWrites(t *testing.T) {
	t.Parallel()

	tracker := newTestTracker(t)
	sourceID := "test-min-writes"

	overwriteTrackersMu.Lock()
	overwriteTrackers[sourceID] = tracker
	overwriteTrackersMu.Unlock()
	t.Cleanup(func() {
		overwriteTrackersMu.Lock()
		delete(overwriteTrackers, sourceID)
		overwriteTrackersMu.Unlock()
	})

	// Write fewer than overwriteMinWrites, all overwrites (100% rate)
	for range overwriteMinWrites - 1 {
		trackOverwrite(sourceID, true)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Equal(t, int64(overwriteMinWrites-1), tracker.totalWrites)
	assert.Equal(t, int64(overwriteMinWrites-1), tracker.overwriteCount)
	assert.True(t, tracker.lastNotified.IsZero(),
		"should not notify when write count is below minimum")
}

// TestOverwriteTracker_UnknownSource verifies that trackOverwrite returns
// silently when called with a source ID that has no registered tracker.
func TestOverwriteTracker_UnknownSource(t *testing.T) {
	t.Parallel()

	// Should not panic or error — just return
	trackOverwrite("nonexistent-source-id", true)
	trackOverwrite("nonexistent-source-id", false)
}
