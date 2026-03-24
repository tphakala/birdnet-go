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
	// suppressionReminderInterval controls how often a reminder is logged/reported
	// while errors are still being suppressed. This provides periodic visibility
	// without flooding.
	suppressionReminderInterval = 5 * time.Minute
)

// providerErrorState tracks the suppression state for a single provider.
type providerErrorState struct {
	// consecutiveFailures counts failures since last success.
	consecutiveFailures int

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

// ErrorSuppressor tracks per-provider error state and suppresses repeated
// identical errors from flooding Sentry and logs. It allows the first error
// through, then suppresses until recovery or a periodic reminder interval.
//
// Thread-safe for concurrent use.
type ErrorSuppressor struct {
	mu       sync.Mutex
	states   map[string]*providerErrorState
	log      logger.Logger
	reporter TelemetryReporter
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
// The caller should only report to Sentry/log when this returns true.
func (es *ErrorSuppressor) ShouldReport(providerName string) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	state := es.getOrCreateState(providerName)
	state.consecutiveFailures++
	state.lastFailureTime = time.Now()

	if state.consecutiveFailures == 1 {
		// First failure - always report
		state.firstFailureTime = time.Now()
		state.reported = true
		state.lastReportTime = time.Now()
		return true
	}

	// Check if enough time has passed for a periodic reminder
	if time.Since(state.lastReportTime) >= suppressionReminderInterval {
		state.lastReportTime = time.Now()
		return true
	}

	return false
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
