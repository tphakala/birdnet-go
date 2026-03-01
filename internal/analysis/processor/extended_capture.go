package processor

import "time"

// Extended capture timeout thresholds.
const (
	extendedCaptureMinInitialWait  = 15 * time.Second
	extendedCaptureMediumThreshold = 30 * time.Second
	extendedCaptureMediumWait      = 30 * time.Second
	extendedCaptureLongThreshold   = 2 * time.Minute
	extendedCaptureLongWait        = 60 * time.Second
)

// calculateExtendedFlushDeadline computes the next flush deadline for an extended capture
// detection using the scaled timeout algorithm. The deadline scales with session duration:
//   - Short (<30s): max(15s, normalDetectionWindow)
//   - Medium (30s-2m): 30s after now
//   - Long (>2m): 60s after now
//
// The result is always capped at maxDeadline to enforce the absolute maximum duration.
func calculateExtendedFlushDeadline(now, firstDetected, maxDeadline time.Time, normalDetectionWindow time.Duration) time.Time {
	sessionDuration := now.Sub(firstDetected)

	var deadline time.Time
	switch {
	case sessionDuration < extendedCaptureMediumThreshold:
		deadline = now.Add(max(normalDetectionWindow, extendedCaptureMinInitialWait))
	case sessionDuration < extendedCaptureLongThreshold:
		deadline = now.Add(extendedCaptureMediumWait)
	default:
		deadline = now.Add(extendedCaptureLongWait)
	}

	if deadline.After(maxDeadline) {
		deadline = maxDeadline
	}

	return deadline
}
