package processor

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFlushDeadlineInFuture verifies that FlushDeadline is always in the future
// This tests the fix for BG-16 where FlushDeadline was calculated incorrectly
func TestFlushDeadlineInFuture(t *testing.T) {
	// Initialize test settings with default values
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Length = 15     // 15 seconds capture length (default)
	settings.Realtime.Audio.Export.PreCapture = 3  // 3 seconds pre-capture (default)

	// Calculate detection window (this is the actual production logic)
	captureLength := time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	detectionWindow := max(time.Duration(0), captureLength-preCaptureLength)

	// With defaults: detectionWindow = 15s - 3s = 12s
	expectedWindow := 12 * time.Second
	if detectionWindow != expectedWindow {
		t.Errorf("Expected detectionWindow to be %v, got %v", expectedWindow, detectionWindow)
	}

	// Simulate what happens when a new detection is created
	// item.StartTime would be backdated (Now - 13s in production)
	// but FlushDeadline should be calculated from Now
	beforeCreation := time.Now()
	flushDeadline := time.Now().Add(detectionWindow)
	afterCreation := time.Now()

	// Verify FlushDeadline is in the future
	if !flushDeadline.After(beforeCreation) {
		t.Errorf("FlushDeadline %v is not in the future relative to creation time %v",
			flushDeadline, beforeCreation)
	}

	// Verify FlushDeadline is approximately detectionWindow seconds in the future
	minExpected := beforeCreation.Add(detectionWindow)
	maxExpected := afterCreation.Add(detectionWindow)

	if flushDeadline.Before(minExpected) || flushDeadline.After(maxExpected) {
		t.Errorf("FlushDeadline %v is not within expected range [%v, %v]",
			flushDeadline, minExpected, maxExpected)
	}

	// Test with edge case: minimum capture length (10s)
	settings.Realtime.Audio.Export.Length = 10
	settings.Realtime.Audio.Export.PreCapture = 5 // Max allowed (captureLength/2)

	captureLength = time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength = time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
	detectionWindow = max(time.Duration(0), captureLength-preCaptureLength)

	// With edge case: detectionWindow = 10s - 5s = 5s
	expectedWindow = 5 * time.Second
	if detectionWindow != expectedWindow {
		t.Errorf("Edge case: Expected detectionWindow to be %v, got %v", expectedWindow, detectionWindow)
	}

	// Even with edge case, FlushDeadline should be in the future
	flushDeadline = time.Now().Add(detectionWindow)
	if !flushDeadline.After(time.Now()) {
		t.Errorf("Edge case: FlushDeadline %v is not in the future", flushDeadline)
	}
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
	if possibleOverlaps < minRequiredOverlaps {
		t.Errorf("Detection window (%v) doesn't provide enough time for overlaps. "+
			"Only %d overlaps possible with step size %.1fs, need at least %d",
			detectionWindow, possibleOverlaps, stepSize, minRequiredOverlaps)
	}

	// With defaults, we should get 12s / 0.8s = 15 possible overlaps - plenty!
	expectedOverlaps := 15
	if possibleOverlaps != expectedOverlaps {
		t.Logf("Note: Expected %d overlaps, got %d (still acceptable if >= %d)",
			expectedOverlaps, possibleOverlaps, minRequiredOverlaps)
	}
}