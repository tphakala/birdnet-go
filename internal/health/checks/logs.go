package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// recentErrorWindow is the lookback period used by RecentErrorsCheck.
const recentErrorWindow = time.Hour

// recentErrorWarnThreshold is the error count above which a warning is issued.
const recentErrorWarnThreshold = 10

// recentErrorCritThreshold is the error count above which the check is marked critical.
const recentErrorCritThreshold = 50

// RecentErrorsCheck counts errors recorded in the last hour and warns when the count is high.
type RecentErrorsCheck struct {
	errorBuffer *health.ErrorRingBuffer
}

// NewRecentErrorsCheck creates a RecentErrorsCheck backed by the given error ring buffer.
func NewRecentErrorsCheck(errorBuffer *health.ErrorRingBuffer) *RecentErrorsCheck {
	return &RecentErrorsCheck{errorBuffer: errorBuffer}
}

// Name returns the check identifier.
func (c *RecentErrorsCheck) Name() string { return "recent_errors" }

// Category returns the logs category.
func (c *RecentErrorsCheck) Category() health.Category { return health.CategoryLogs }

// Run counts errors in the last hour and reports degraded status when the count is elevated.
func (c *RecentErrorsCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.errorBuffer.Count() == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	since := start.Add(-recentErrorWindow)
	count := c.errorBuffer.CountSince(since)

	status := health.StatusHealthy
	msg := fmt.Sprintf("Error count OK (%d in last hour)", count)

	switch {
	case count > recentErrorCritThreshold:
		status = health.StatusCritical
		msg = fmt.Sprintf("High error rate (%d errors in last hour)", count)
	case count > recentErrorWarnThreshold:
		status = health.StatusWarning
		msg = fmt.Sprintf("Elevated error count (%d errors in last hour)", count)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"count_last_hour": count,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// trendHalfWindow is the half-period used for error trend comparison (30 minutes each half).
const trendHalfWindow = 30 * time.Minute

// trendRatioThreshold is the ratio of recent to previous errors above which a warning is issued.
const trendRatioThreshold = 1.5

// trendMinCount is the minimum recent error count required to trigger a trend warning.
const trendMinCount = 5

// ErrorTrendCheck compares error counts in the last 30 minutes against the preceding 30 minutes.
// A warning is issued when the recent count exceeds 1.5x the previous count and is above a minimum.
type ErrorTrendCheck struct {
	errorBuffer *health.ErrorRingBuffer
}

// NewErrorTrendCheck creates an ErrorTrendCheck backed by the given error ring buffer.
func NewErrorTrendCheck(errorBuffer *health.ErrorRingBuffer) *ErrorTrendCheck {
	return &ErrorTrendCheck{errorBuffer: errorBuffer}
}

// Name returns the check identifier.
func (c *ErrorTrendCheck) Name() string { return "error_trend" }

// Category returns the logs category.
func (c *ErrorTrendCheck) Category() health.Category { return health.CategoryLogs }

// Run compares recent versus previous error counts and warns on an increasing trend.
func (c *ErrorTrendCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.errorBuffer.Count() == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	midpoint := start.Add(-trendHalfWindow)
	periodStart := start.Add(-2 * trendHalfWindow)

	recent := c.errorBuffer.CountSince(midpoint)
	total := c.errorBuffer.CountSince(periodStart)
	previous := total - recent

	status := health.StatusHealthy
	msg := fmt.Sprintf("Error trend stable (recent=%d, previous=%d)", recent, previous)

	if previous > 0 {
		ratio := float64(recent) / float64(previous)
		if ratio > trendRatioThreshold && recent > trendMinCount {
			status = health.StatusWarning
			msg = fmt.Sprintf("Error rate increasing (recent=%d, previous=%d, ratio=%.1fx)", recent, previous, ratio)
		}
	} else if recent > trendMinCount {
		status = health.StatusWarning
		msg = fmt.Sprintf("Error rate increasing (recent=%d, previous=0)", recent)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"recent_30m":   recent,
			"previous_30m": previous,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// criticalEventLookback is how far back CriticalEventsCheck scans for fatal/panic entries.
const criticalEventLookback = 100

// CriticalEventsCheck scans recent log entries for fatal or panic-level events.
type CriticalEventsCheck struct {
	errorBuffer *health.ErrorRingBuffer
}

// NewCriticalEventsCheck creates a CriticalEventsCheck backed by the given error ring buffer.
func NewCriticalEventsCheck(errorBuffer *health.ErrorRingBuffer) *CriticalEventsCheck {
	return &CriticalEventsCheck{errorBuffer: errorBuffer}
}

// Name returns the check identifier.
func (c *CriticalEventsCheck) Name() string { return "critical_events" }

// Category returns the logs category.
func (c *CriticalEventsCheck) Category() health.Category { return health.CategoryLogs }

// Run scans recent log entries for fatal or panic-level events and fails if any are found.
func (c *CriticalEventsCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.errorBuffer.Count() == 0 {
		return skippedResult(c.Name(), c.Category(), start)
	}

	entries := c.errorBuffer.Recent(criticalEventLookback)

	type eventEntry struct {
		Level     string    `json:"level"`
		Message   string    `json:"message"`
		Component string    `json:"component,omitempty"`
		Timestamp time.Time `json:"timestamp"`
	}

	var criticalFound []eventEntry
	for _, e := range entries {
		if e.Level == "fatal" || e.Level == "panic" {
			criticalFound = append(criticalFound, eventEntry{
				Level:     e.Level,
				Message:   e.Message,
				Component: e.Component,
				Timestamp: e.Timestamp,
			})
		}
	}

	if len(criticalFound) > 0 {
		return health.Result{
			Name:     c.Name(),
			Category: c.Category(),
			Status:   health.StatusCritical,
			Message:  fmt.Sprintf("Found %d critical event(s) (fatal/panic) in recent log entries", len(criticalFound)),
			Details: map[string]any{
				"events": criticalFound,
			},
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     health.StatusHealthy,
		Message:    "No critical events in recent log entries",
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
