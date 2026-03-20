package notification

import (
	"sync"
	"time"
)

// BurstAction indicates what the caller should do with the current error.
type BurstAction int

const (
	// BurstActionAllow means create a normal individual notification.
	BurstActionAllow BurstAction = iota
	// BurstActionSummary means create a summary notification (threshold+1 reached).
	BurstActionSummary
	// BurstActionSuppress means don't create a notification (summary already sent).
	BurstActionSuppress
)

// BurstSummary holds information about a burst for summary notification rendering.
type BurstSummary struct {
	Component   string
	Category    string
	Count       int
	SampleError string
	WindowMin   int
}

// ErrorBurstTracker groups repeated errors from the same component+category
// within a sliding window. It prevents notification spam by collapsing bursts
// into a single summary notification.
type ErrorBurstTracker struct {
	mu        sync.Mutex
	buckets   map[string]*burstBucket
	threshold int
	window    time.Duration
}

type burstBucket struct {
	count     int
	firstSeen time.Time
	lastSeen  time.Time
	sample    string // first error message in the window
	notified  bool   // summary notification was sent
}

// NewErrorBurstTracker creates a tracker with the given burst threshold and
// window duration. Errors from the same component+category within the window
// are grouped. The first `threshold` errors pass through individually. At
// threshold+1, a summary notification is created. Subsequent errors in the
// window are suppressed.
func NewErrorBurstTracker(threshold int, window time.Duration) *ErrorBurstTracker {
	return &ErrorBurstTracker{
		buckets:   make(map[string]*burstBucket),
		threshold: threshold,
		window:    window,
	}
}

// Record records an error occurrence and returns the action the caller should
// take. When the action is BurstActionSummary, the returned *BurstSummary is
// a snapshot captured atomically under the same lock, avoiding a TOCTOU race
// where the window could expire between Record and a separate GetSummary call.
func (t *ErrorBurstTracker) Record(component, category, errMsg string) (BurstAction, *BurstSummary) {
	key := component + ":" + category

	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	bucket, exists := t.buckets[key]

	// Expired or new bucket — reset.
	if !exists || now.Sub(bucket.firstSeen) > t.window {
		t.buckets[key] = &burstBucket{
			count:     1,
			firstSeen: now,
			lastSeen:  now,
			sample:    errMsg,
		}
		return BurstActionAllow, nil
	}

	bucket.count++
	bucket.lastSeen = now

	if bucket.count <= t.threshold {
		return BurstActionAllow, nil
	}

	if !bucket.notified {
		bucket.notified = true
		summary := &BurstSummary{
			Component:   component,
			Category:    category,
			Count:       bucket.count,
			SampleError: bucket.sample,
			WindowMin:   int(t.window.Minutes()),
		}
		return BurstActionSummary, summary
	}

	return BurstActionSuppress, nil
}
