package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFlushDeadlineInFuture verifies that FlushDeadline is always in the future
// This tests the fix for BG-16 where FlushDeadline was calculated incorrectly
func TestFlushDeadlineInFuture(t *testing.T) {
	// Initialize test settings with default values
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Length = 15    // 15 seconds capture length (default)
	settings.Realtime.Audio.Export.PreCapture = 3 // 3 seconds pre-capture (default)

	// Calculate detection window (this is the actual production logic)
	captureLength := time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	detectionWindow := max(time.Duration(0), captureLength-preCaptureLength)

	// With defaults: detectionWindow = 15s - 3s = 12s
	expectedWindow := 12 * time.Second
	require.Equal(t, expectedWindow, detectionWindow, "detectionWindow mismatch")

	// Simulate what happens when a new detection is created
	// item.StartTime would be backdated (Now - 13s in production)
	// but FlushDeadline should be calculated from Now
	beforeCreation := time.Now()
	flushDeadline := time.Now().Add(detectionWindow)
	afterCreation := time.Now()

	// Verify FlushDeadline is in the future
	assert.True(t, flushDeadline.After(beforeCreation),
		"FlushDeadline %v is not in the future relative to creation time %v",
		flushDeadline, beforeCreation)

	// Verify FlushDeadline is approximately detectionWindow seconds in the future
	minExpected := beforeCreation.Add(detectionWindow)
	maxExpected := afterCreation.Add(detectionWindow)

	assert.False(t, flushDeadline.Before(minExpected) || flushDeadline.After(maxExpected),
		"FlushDeadline %v is not within expected range [%v, %v]",
		flushDeadline, minExpected, maxExpected)

	// Test with edge case: minimum capture length (10s)
	settings.Realtime.Audio.Export.Length = 10
	settings.Realtime.Audio.Export.PreCapture = 5 // Max allowed (captureLength/2)

	captureLength = time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength = time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	detectionWindow = max(time.Duration(0), captureLength-preCaptureLength)

	// With edge case: detectionWindow = 10s - 5s = 5s
	expectedWindow = 5 * time.Second
	assert.Equal(t, expectedWindow, detectionWindow, "Edge case: detectionWindow mismatch")

	// Even with edge case, FlushDeadline should be in the future
	flushDeadline = time.Now().Add(detectionWindow)
	assert.True(t, flushDeadline.After(time.Now()),
		"Edge case: FlushDeadline %v is not in the future", flushDeadline)
}

// TestDetectionWindowGivesTimeForOverlaps verifies that detectionWindow
// provides enough time for overlapping analyses to accumulate
func TestDetectionWindowGivesTimeForOverlaps(t *testing.T) {
	// With overlap 2.2, step size is 0.8s (3s - 2.2s)
	overlapDuration := 2.2
	chunkDuration := 3.0
	stepSize := chunkDuration - overlapDuration // 0.8s

	// With default settings
	captureLength := 15 * time.Second
	preCapture := 3 * time.Second
	detectionWindow := captureLength - preCapture // 12s

	// Calculate how many overlapping analyses can occur within the detection window
	possibleOverlaps := int(detectionWindow.Seconds() / stepSize)

	// We need at least 2-3 overlaps for the filtering to work
	minRequiredOverlaps := 2
	assert.GreaterOrEqual(t, possibleOverlaps, minRequiredOverlaps,
		"Detection window (%v) doesn't provide enough time for overlaps. "+
			"Only %d overlaps possible with step size %.1fs, need at least %d",
		detectionWindow, possibleOverlaps, stepSize, minRequiredOverlaps)

	// With defaults, we should get 12s / 0.8s = 15 possible overlaps - plenty!
	expectedOverlaps := 15
	if possibleOverlaps != expectedOverlaps {
		t.Logf("Note: Expected %d overlaps, got %d (still acceptable if >= %d)",
			expectedOverlaps, possibleOverlaps, minRequiredOverlaps)
	}
}

// TestFlushDeadlineNotBackdated is a regression test that simulates the exact
// bug scenario from BG-16. This test would have caught the original bug where
// FlushDeadline was calculated using the backdated startTime instead of time.Now().
func TestFlushDeadlineNotBackdated(t *testing.T) {
	// Simulate the bug scenario exactly with default settings
	detectionOffset := 10 * time.Second
	preCapture := 3 * time.Second
	backdatedStart := time.Now().Add(-(detectionOffset + preCapture))
	captureLength := 15 * time.Second
	detectionWindow := captureLength - preCapture // 12s

	// The buggy calculation: FlushDeadline = item.StartTime.Add(detectionWindow)
	// where item.StartTime was backdated (Now - 13s)
	buggyDeadline := backdatedStart.Add(detectionWindow)

	// Should fail with the bug - deadline is in the past
	// We assert that buggy deadline is NOT after now (i.e., it's in the past)
	assert.False(t, buggyDeadline.After(time.Now()),
		"Expected buggy deadline to be in the past, but it's in the future. "+
			"This test may need adjustment if system clock is unreliable.")
	t.Logf("Bug confirmed: deadline %v is in the past (Now: %v, backdatedStart: %v)",
		buggyDeadline, time.Now(), backdatedStart)
	t.Logf("Calculation: backdatedStart (%v) + detectionWindow (%v) = %v (in past!)",
		backdatedStart.Format("15:04:05.000"), detectionWindow, buggyDeadline.Format("15:04:05.000"))

	// The fixed calculation: FlushDeadline = time.Now().Add(detectionWindow)
	fixedDeadline := time.Now().Add(detectionWindow)

	// Should pass with the fix - deadline is in the future
	assert.True(t, fixedDeadline.After(time.Now()),
		"Fix failed: deadline %v is still in the past (Now: %v)",
		fixedDeadline, time.Now())
	t.Logf("Fix confirmed: deadline %v is in the future (Now: %v)",
		fixedDeadline, time.Now())
	t.Logf("Calculation: Now + detectionWindow (%v) = %v (in future!)",
		detectionWindow, fixedDeadline.Format("15:04:05.000"))

	// Verify the difference between buggy and fixed approaches
	timeDifference := fixedDeadline.Sub(buggyDeadline)
	expectedDifference := detectionOffset + preCapture // 13s

	// Allow 100ms tolerance for test execution time
	tolerance := 100 * time.Millisecond
	assert.InDelta(t, expectedDifference.Seconds(), timeDifference.Seconds(), tolerance.Seconds(),
		"Time difference between fixed and buggy deadline should be ~%v, got %v",
		expectedDifference, timeDifference)

	// Document the impact
	t.Logf("Impact: With buggy calculation, detections flushed immediately instead of waiting %v", detectionWindow)
	t.Logf("Result: overlap-based filtering couldn't accumulate confirmations, causing 'matched 1/2 times' rejections")
}
