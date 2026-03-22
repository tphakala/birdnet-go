package buffer

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// OverwriteTrackerOpts holds configuration for NewOverwriteTracker.
type OverwriteTrackerOpts struct {
	// WindowDuration is the sliding window over which the overwrite rate is
	// calculated. After this duration the counters are reset.
	WindowDuration time.Duration

	// RateThreshold is the overwrite percentage (0–100) that triggers a
	// warning notification.
	RateThreshold int

	// MinWrites is the minimum number of writes that must be recorded in the
	// current window before a threshold check is performed.
	MinWrites int64

	// NotifyCooldown is the minimum duration between two consecutive warning
	// notifications for the same source.
	NotifyCooldown time.Duration

	// Logger is used to emit the overload warning. If nil, warnings are
	// silently dropped.
	Logger logger.Logger
}

// OverwriteTracker tracks buffer overwrite events over a sliding time window
// and logs a warning when the overwrite rate exceeds a configured threshold.
// It is safe for concurrent use.
type OverwriteTracker struct {
	mu             sync.Mutex
	totalWrites    int64
	overwriteCount int64
	windowStart    time.Time
	lastNotified   time.Time

	windowDuration time.Duration
	rateThreshold  int
	minWrites      int64
	notifyCooldown time.Duration
	log            logger.Logger
}

// NewOverwriteTracker creates an OverwriteTracker with the given options.
func NewOverwriteTracker(opts OverwriteTrackerOpts) *OverwriteTracker {
	return &OverwriteTracker{
		windowStart:    time.Now(),
		windowDuration: opts.WindowDuration,
		rateThreshold:  opts.RateThreshold,
		minWrites:      opts.MinWrites,
		notifyCooldown: opts.NotifyCooldown,
		log:            opts.Logger,
	}
}

// RecordWrite increments the total write counter for the current window.
func (t *OverwriteTracker) RecordWrite() {
	t.mu.Lock()
	t.maybeResetWindowLocked()
	t.totalWrites++
	t.mu.Unlock()
}

// RecordOverwrite increments the overwrite counter for the current window.
// Callers must also call RecordWrite for each overwrite event.
func (t *OverwriteTracker) RecordOverwrite() {
	t.mu.Lock()
	t.overwriteCount++
	t.mu.Unlock()
}

// CheckAndNotify logs a warning if the overwrite rate exceeds the configured
// threshold after the minimum number of writes, respecting the notification
// cooldown. sourceID is included in the log message for identification.
func (t *OverwriteTracker) CheckAndNotify(sourceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.totalWrites == 0 || t.totalWrites < t.minWrites {
		return
	}

	rate := float64(t.overwriteCount) / float64(t.totalWrites) * 100
	if int(rate) < t.rateThreshold {
		return
	}

	now := time.Now()
	if t.notifyCooldown > 0 && now.Sub(t.lastNotified) < t.notifyCooldown {
		return
	}

	t.lastNotified = now

	if t.log != nil {
		t.log.Warn("audio analysis buffer overload detected",
			logger.String("source_id", sourceID),
			logger.Float64("overwrite_rate_pct", rate),
			logger.Int64("total_writes", t.totalWrites),
			logger.Int64("overwrite_count", t.overwriteCount),
		)
	}
}

// OverwriteRate returns the current overwrite rate as a percentage (0–100).
// Returns 0 if no writes have been recorded.
func (t *OverwriteTracker) OverwriteRate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.totalWrites == 0 {
		return 0
	}
	return float64(t.overwriteCount) / float64(t.totalWrites) * 100
}

// Reset clears all counters and restarts the sliding window.
func (t *OverwriteTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totalWrites = 0
	t.overwriteCount = 0
	t.windowStart = time.Now()
}

// maybeResetWindowLocked resets counters when the current window has expired.
// Must be called with t.mu held.
func (t *OverwriteTracker) maybeResetWindowLocked() {
	if t.windowDuration > 0 && time.Since(t.windowStart) > t.windowDuration {
		t.totalWrites = 0
		t.overwriteCount = 0
		t.windowStart = time.Now()
	}
}
