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

// Record records an error occurrence and returns the action the caller should take.
func (t *ErrorBurstTracker) Record(component, category, errMsg string) BurstAction {
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
		return BurstActionAllow
	}

	bucket.count++
	bucket.lastSeen = now

	if bucket.count <= t.threshold {
		return BurstActionAllow
	}

	if !bucket.notified {
		bucket.notified = true
		return BurstActionSummary
	}

	return BurstActionSuppress
}

// GetSummary returns burst information for rendering a summary notification.
func (t *ErrorBurstTracker) GetSummary(component, category string) *BurstSummary {
	key := component + ":" + category

	t.mu.Lock()
	defer t.mu.Unlock()

	bucket, exists := t.buckets[key]
	if !exists {
		return nil
	}

	return &BurstSummary{
		Component:   component,
		Category:    category,
		Count:       bucket.count,
		SampleError: bucket.sample,
		WindowMin:   int(t.window.Minutes()),
	}
}
