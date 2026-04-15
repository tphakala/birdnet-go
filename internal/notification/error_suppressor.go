// Package notification error suppression for repeated provider failures.
//
// ErrorSuppressor prevents Sentry/log flooding when a provider is persistently
// failing (e.g., unreachable webhook endpoint, expired API token). It reports
// the first error, suppresses subsequent identical errors, and reports a
// summary when the provider recovers or a periodic reminder interval elapses.
//
// Thread-safe for concurrent use across dispatch goroutines.
package notification

import (
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// suppressionReminderInterval is the INITIAL interval between repeated
	// reminders while a provider is still failing. The effective interval
	// doubles on each reminder (see nextReminderInterval and
	// maxSuppressionReminderInterval) so a long-lived outage does not keep
	// generating one Sentry event every five minutes. Starting at five minutes
	// keeps the first reminder actionable for an operator.
	suppressionReminderInterval = 5 * time.Minute

	// maxSuppressionReminderInterval caps the exponential growth. Twenty-four
	// hours keeps the rate of Sentry events at roughly once per day for long
	// outages while still giving an operator periodic confirmation that the
	// failure is ongoing.
	maxSuppressionReminderInterval = 24 * time.Hour

	// defaultStaleEntryMaxAge is the default duration after which an error state
	// entry with no recent activity is considered stale and eligible for eviction.
	// This prevents the states map from growing unbounded when providers are
	// abandoned (removed from config, renamed, etc.) without a RecordSuccess call.
	defaultStaleEntryMaxAge = 24 * time.Hour
)

// providerErrorState tracks the suppression state for a single provider.
type providerErrorState struct {
	// consecutiveFailures counts failures since last success.
	consecutiveFailures int

	// reportCount tracks how many times we have already reported for this
	// failure streak. Drives the exponential reminder backoff — see
	// nextReminderInterval in error_suppressor.go.
	reportCount int

	// firstFailureTime is when the current failure streak started.
	firstFailureTime time.Time

	// lastFailureTime is when the most recent failure occurred.
	lastFailureTime time.Time

	// lastReportTime is when we last reported (logged/sent telemetry) for this provider.
	lastReportTime time.Time

	// reported indicates the initial error has been reported.
	reported bool

	// sampleError is the first error message in the current failure streak.
	sampleError string
}

// cleanupInterval controls how often the stale-entry cleanup runs
// within ShouldReport. This avoids scanning on every single call.
const cleanupInterval = 1 * time.Hour

// ErrorSuppressor tracks per-provider error state and suppresses repeated
// identical errors from flooding Sentry and logs. It allows the first error
// through, then suppresses until recovery or a periodic reminder interval.
//
// Stale entries (from providers that were abandoned without recovery) are
// periodically evicted to prevent unbounded map growth.
//
// Thread-safe for concurrent use.
type ErrorSuppressor struct {
	mu          sync.Mutex
	states      map[string]*providerErrorState
	lastCleanup time.Time // last time stale entries were evicted
	log         logger.Logger
	reporter    TelemetryReporter
}

// NewErrorSuppressor creates a new ErrorSuppressor instance.
func NewErrorSuppressor(log logger.Logger, reporter TelemetryReporter) *ErrorSuppressor {
	return &ErrorSuppressor{
		states:   make(map[string]*providerErrorState),
		log:      log,
		reporter: reporter,
	}
}

// ShouldReport determines whether an error for the given provider should be
// reported to telemetry/logs. Returns true for the first failure and for
// periodic reminders. Returns false when the error should be suppressed.
//
// Also periodically evicts stale entries to bound map growth.
//
// The caller should only report to Sentry/log when this returns true.
func (es *ErrorSuppressor) ShouldReport(providerName string) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	// Periodically evict stale entries to prevent unbounded map growth
	es.cleanupStaleEntriesLocked(defaultStaleEntryMaxAge)

	state := es.getOrCreateState(providerName)
	state.consecutiveFailures++
	state.lastFailureTime = time.Now()

	if state.consecutiveFailures == 1 {
		// First failure - always report
		state.firstFailureTime = time.Now()
		state.reported = true
		state.lastReportTime = time.Now()
		state.reportCount = 1
		return true
	}

	// Check if enough time has passed for the next reminder. The interval
	// grows exponentially with each reported reminder so a multi-hour outage
	// does not keep emitting one Sentry event every suppressionReminderInterval.
	if time.Since(state.lastReportTime) >= nextReminderInterval(state.reportCount) {
		state.lastReportTime = time.Now()
		state.reportCount++
		return true
	}

	return false
}

// nextReminderInterval returns the wait time before the next reminder, given
// how many reminders have already been emitted for the current failure streak.
//
// reportCount is 1 after the initial failure, which means the first *reminder*
// (report 2) waits one base interval, the second reminder waits two base
// intervals, and so on, capped at maxSuppressionReminderInterval.
//
// Schedule with defaults (5m base, 2x multiplier, 24h cap):
//
//	report 2: 5m  (5 * 2^0)
//	report 3: 10m (5 * 2^1)
//	report 4: 20m (5 * 2^2)
//	report 5: 40m
//	report 6: 80m
//	...       capped at 24h
func nextReminderInterval(reportCount int) time.Duration {
	if reportCount < 1 {
		return suppressionReminderInterval
	}

	// Exponential growth via bit-shift — multiplier is hard-coded to 2 for a
	// 5m → 10m → 20m → 40m → ... schedule. Cap the shift at a bound that
	// comfortably exceeds
	// maxSuppressionReminderInterval to avoid any chance of signed-int
	// overflow if reportCount grows unbounded during a very long outage.
	const maxShift = 20
	shift := reportCount - 1
	if shift > maxShift {
		shift = maxShift
	}

	multiplier := int64(1) << shift
	interval := time.Duration(multiplier) * suppressionReminderInterval
	if interval <= 0 || interval > maxSuppressionReminderInterval {
		return maxSuppressionReminderInterval
	}
	return interval
}

// RecordFailure records a failure for a provider without checking whether to report.
// Use this to update the sample error message on the first failure.
func (es *ErrorSuppressor) RecordFailure(providerName, errorMessage string) {
	es.mu.Lock()
	defer es.mu.Unlock()

	state := es.getOrCreateState(providerName)
	if state.sampleError == "" {
		state.sampleError = errorMessage
	}
}

// RecordSuccess records a successful delivery for a provider and resets its
// failure state. If the provider was in a suppressed failure state, logs a
// recovery message with the count of suppressed errors.
func (es *ErrorSuppressor) RecordSuccess(providerName string) {
	es.mu.Lock()

	state, exists := es.states[providerName]
	if !exists || state.consecutiveFailures == 0 {
		es.mu.Unlock()
		return
	}

	// Copy state for reporting outside the lock — reportRecovery may do
	// network I/O (Sentry CaptureEvent) which must not hold the mutex.
	stateCopy := *state
	shouldLog := state.consecutiveFailures > 1 && es.log != nil
	shouldReport := state.consecutiveFailures > 1 && es.reporter != nil && es.reporter.IsEnabled()

	// Reset state while holding lock
	delete(es.states, providerName)
	es.mu.Unlock()

	// Log recovery if there were suppressed failures
	if shouldLog {
		es.log.Info("provider recovered after consecutive failures",
			logger.String("provider", providerName),
			logger.Int("suppressed_errors", stateCopy.consecutiveFailures-1),
			logger.Duration("failure_duration", time.Since(stateCopy.firstFailureTime)))
	}

	// Report recovery telemetry event (outside lock — may involve network I/O)
	if shouldReport {
		es.reportRecovery(providerName, &stateCopy)
	}
}

// GetSuppressedCount returns the number of consecutive failures for a provider.
// Returns 0 if the provider has no recorded failures.
func (es *ErrorSuppressor) GetSuppressedCount(providerName string) int {
	es.mu.Lock()
	defer es.mu.Unlock()

	state, exists := es.states[providerName]
	if !exists {
		return 0
	}
	return state.consecutiveFailures
}

// CleanupStaleEntries removes provider error states that have had no activity
// (no failures recorded) for longer than maxAge. This prevents the states map
// from growing unbounded when providers are abandoned without a RecordSuccess call.
//
// Can be called explicitly for testing or external coordination. For normal
// operation, stale entries are also evicted automatically during ShouldReport.
func (es *ErrorSuppressor) CleanupStaleEntries(maxAge time.Duration) int {
	es.mu.Lock()
	defer es.mu.Unlock()

	return es.doCleanupStaleEntries(maxAge)
}

// cleanupStaleEntriesLocked runs the stale-entry eviction at most once per
// cleanupInterval. Must be called with es.mu held.
func (es *ErrorSuppressor) cleanupStaleEntriesLocked(maxAge time.Duration) {
	now := time.Now()
	if now.Sub(es.lastCleanup) < cleanupInterval {
		return
	}
	es.lastCleanup = now
	removed := es.doCleanupStaleEntries(maxAge)
	if removed > 0 && es.log != nil {
		es.log.Debug("evicted stale error suppressor entries",
			logger.Int("removed", removed),
			logger.Int("remaining", len(es.states)))
	}
}

// doCleanupStaleEntries removes entries older than maxAge. Must be called with es.mu held.
func (es *ErrorSuppressor) doCleanupStaleEntries(maxAge time.Duration) int {
	now := time.Now()
	removed := 0
	for name, state := range es.states {
		if now.Sub(state.lastFailureTime) > maxAge {
			delete(es.states, name)
			removed++
		}
	}
	return removed
}

// getOrCreateState returns the error state for a provider, creating one if needed.
// Must be called with es.mu held.
func (es *ErrorSuppressor) getOrCreateState(providerName string) *providerErrorState {
	state, exists := es.states[providerName]
	if !exists {
		state = &providerErrorState{}
		es.states[providerName] = state
	}
	return state
}

// reportRecovery sends a telemetry event when a provider recovers from a failure streak.
// Must be called with es.mu held.
func (es *ErrorSuppressor) reportRecovery(providerName string, state *providerErrorState) {
	failureDuration := time.Since(state.firstFailureTime)

	message := fmt.Sprintf("Provider %s recovered after %d consecutive failures (%.0fs)",
		providerName, state.consecutiveFailures, failureDuration.Seconds())

	tags := map[string]string{
		"component":            "notification",
		"provider":             providerName,
		"event_type":           "provider_recovery",
		"consecutive_failures": fmt.Sprintf("%d", state.consecutiveFailures),
	}

	contexts := map[string]any{
		"recovery": map[string]any{
			"consecutive_failures":  state.consecutiveFailures,
			"failure_duration_secs": failureDuration.Seconds(),
			"sample_error":          state.sampleError,
			"first_failure_time":    state.firstFailureTime.Format(time.RFC3339),
			"last_failure_time":     state.lastFailureTime.Format(time.RFC3339),
		},
	}

	es.reporter.CaptureEvent(message, SeverityInfo, tags, contexts)
}
