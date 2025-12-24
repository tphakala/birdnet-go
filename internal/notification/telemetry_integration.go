// Package notification provides telemetry integration for notification operations
package notification

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Telemetry severity levels
const (
	SeverityDebug    = "debug"
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityError    = "error"
	SeverityCritical = "critical"
)

// TelemetryReporter defines the interface for reporting telemetry events.
// This interface is implemented by the telemetry package to avoid circular imports.
// The notification package defines the interface, and the telemetry package provides
// the concrete implementation via dependency injection.
type TelemetryReporter interface {
	// CaptureError reports an error with component context
	CaptureError(err error, component string)

	// CaptureEvent reports a custom event with tags and contexts
	CaptureEvent(message, level string, tags map[string]string, contexts map[string]any)

	// IsEnabled returns whether telemetry reporting is enabled
	IsEnabled() bool
}

// TelemetryConfig holds configuration for notification telemetry
type TelemetryConfig struct {
	// Enabled controls all telemetry reporting
	// Controlled by global Sentry setting
	Enabled bool

	// RateLimitReportThreshold sets the minimum drop rate percentage to trigger telemetry.
	// Only sustained high drop rates above this threshold are reported.
	// Default: 50.0 (50%)
	RateLimitReportThreshold float64
}

// DefaultTelemetryConfig returns default telemetry configuration
func DefaultTelemetryConfig() TelemetryConfig {
	return TelemetryConfig{
		Enabled:                  true, // Controlled by global Sentry setting
		RateLimitReportThreshold: 50.0, // Report when drop rate > 50%
	}
}

// NotificationTelemetry handles telemetry reporting for notification operations
type NotificationTelemetry struct {
	config   *TelemetryConfig
	reporter TelemetryReporter
}

// NewNotificationTelemetry creates a new notification telemetry instance.
// The reporter is injected to avoid circular imports with the telemetry package.
func NewNotificationTelemetry(config *TelemetryConfig, reporter TelemetryReporter) *NotificationTelemetry {
	if config == nil {
		defaultCfg := DefaultTelemetryConfig()
		config = &defaultCfg
	}

	return &NotificationTelemetry{
		config:   config,
		reporter: reporter,
	}
}

// IsEnabled returns whether telemetry is enabled
func (nt *NotificationTelemetry) IsEnabled() bool {
	if nt == nil || nt.config == nil || nt.reporter == nil {
		return false
	}
	return nt.config.Enabled && nt.reporter.IsEnabled()
}

// CircuitBreakerStateTransition reports a circuit breaker state change event
func (nt *NotificationTelemetry) CircuitBreakerStateTransition(
	providerName string,
	oldState CircuitState,
	newState CircuitState,
	consecutiveFailures int,
	timeInPreviousState time.Duration,
	config CircuitBreakerConfig,
) {
	// Check if enabled
	if !nt.IsEnabled() {
		return
	}

	// Determine severity level based on new state
	level := SeverityInfo
	if newState == StateOpen {
		level = SeverityWarning
	} else if newState == StateClosed && oldState == StateOpen {
		level = SeverityInfo // Recovery event
	}

	// Build message
	message := fmt.Sprintf("Circuit breaker state transition: %s → %s", oldState.String(), newState.String())

	// Build tags
	tags := map[string]string{
		"component":            "notification",
		"provider":             providerName,
		"old_state":            oldState.String(),
		"new_state":            newState.String(),
		"consecutive_failures": fmt.Sprintf("%d", consecutiveFailures),
	}

	// Build contexts
	contexts := map[string]any{
		"circuit_breaker": map[string]any{
			"failure_threshold":              config.MaxFailures,
			"timeout_seconds":                config.Timeout.Seconds(),
			"half_open_max_requests":         config.HalfOpenMaxRequests,
			"time_in_previous_state_seconds": timeInPreviousState.Seconds(),
		},
	}

	// Report event
	nt.reporter.CaptureEvent(message, level, tags, contexts)
}

// NoopTelemetryReporter is a no-op implementation of TelemetryReporter for testing
// or when telemetry is disabled
type NoopTelemetryReporter struct{}

// CaptureError is a no-op
func (n *NoopTelemetryReporter) CaptureError(err error, component string) {}

// CaptureEvent is a no-op
func (n *NoopTelemetryReporter) CaptureEvent(message, level string, tags map[string]string, contexts map[string]any) {
}

// IsEnabled always returns false
func (n *NoopTelemetryReporter) IsEnabled() bool {
	return false
}

// NewNoopTelemetryReporter creates a no-op telemetry reporter
func NewNoopTelemetryReporter() TelemetryReporter {
	return &NoopTelemetryReporter{}
}

// isConnectionError checks if the error is a network/connection-level error
// that indicates user configuration issues rather than code quality problems.
// These include: connection refused, DNS failures, network unreachable, etc.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Connection-level errors (service not running, wrong port, firewall)
	connectionPatterns := []string{
		"connection refused",
		"connection reset",
		"connection closed",
		"no route to host",
		"network is unreachable",
		"network unreachable",
		"host is down",
		"no such host",        // DNS resolution failure
		"lookup ",             // DNS lookup failure pattern
		"dial tcp",            // General TCP dial failures
		"dial udp",            // UDP dial failures
		"i/o timeout",         // Network timeout at socket level
		"broken pipe",         // Connection broken
		"connection timed out", // TCP connection timeout
	}

	for _, pattern := range connectionPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// WebhookRequestError reports a webhook HTTP request failure with privacy-safe context
func (nt *NotificationTelemetry) WebhookRequestError(
	providerName string,
	err error,
	statusCode int,
	endpoint string,
	method string,
	authType string,
	isTimeout bool,
	isCancelled bool,
) {
	// Check if enabled
	if !nt.IsEnabled() {
		return
	}

	// Don't report cancellations as errors
	if isCancelled {
		return
	}

	// Don't report connection-level errors - these are user configuration issues
	// (service not running, wrong URL, DNS misconfiguration, firewall, etc.)
	// not code quality problems that need telemetry alerts
	if isConnectionError(err) {
		return
	}

	// Determine severity based on error type
	var level string
	switch {
	case isTimeout:
		level = SeverityWarning // Timeouts often indicate user's network/service issues
	case statusCode >= 400 && statusCode < 500:
		level = SeverityWarning // Client errors likely config issues
	default:
		level = SeverityError
	}

	// Anonymize URL for privacy
	anonymizedURL := privacy.AnonymizeURL(endpoint)

	// Build message - check timeout first, then err != nil to avoid panic
	var message string
	switch {
	case isTimeout:
		message = "Webhook request timed out"
	case err != nil:
		message = fmt.Sprintf("Webhook request failed: %s", err.Error())
	default:
		message = "Webhook request failed"
	}

	// Scrub error message for privacy
	scrubbedMessage := privacy.ScrubMessage(message)

	// Build tags
	tags := map[string]string{
		"component":     "notification",
		"provider":      providerName,
		"provider_type": "webhook",
		"status_code":   fmt.Sprintf("%d", statusCode),
		"method":        method,
		"auth_type":     authType,
		"endpoint_hash": anonymizedURL,
		"is_timeout":    fmt.Sprintf("%t", isTimeout),
	}

	// Build contexts
	contexts := map[string]any{
		"request": map[string]any{
			"method":        method,
			"endpoint_hash": anonymizedURL,
			"auth_type":     authType, // Type only, never token/credentials
			"is_timeout":    isTimeout,
			"is_cancelled":  isCancelled,
		},
	}

	// Report error through interface
	nt.reporter.CaptureEvent(scrubbedMessage, level, tags, contexts)
}

// ProviderInitializationError reports provider creation/validation failures
func (nt *NotificationTelemetry) ProviderInitializationError(
	providerName string,
	providerType string,
	errorType string,
	err error,
) {
	// Check if enabled
	if !nt.IsEnabled() {
		return
	}

	// Scrub error message for privacy (remove paths, secrets)
	var scrubbedMessage string
	if err == nil {
		scrubbedMessage = "unknown error"
	} else {
		scrubbedMessage = privacy.ScrubMessage(err.Error())
	}

	// Build message
	message := fmt.Sprintf("Provider initialization failed: %s", scrubbedMessage)

	// Build tags
	tags := map[string]string{
		"component":     "notification",
		"provider":      providerName,
		"provider_type": providerType,
		"error_type":    errorType, // template_parse, validation, secret_resolution
	}

	// Build contexts
	contexts := map[string]any{
		"initialization": map[string]any{
			"provider_type": providerType,
			"error_type":    errorType,
		},
	}

	// Report as error level (prevents provider from working)
	nt.reporter.CaptureEvent(message, SeverityError, tags, contexts)
}

// WorkerPanicRecovered reports a panic that was caught and recovered in a worker
func (nt *NotificationTelemetry) WorkerPanicRecovered(
	workerType string,
	panicValue any,
	stackTrace string,
	eventsProcessed uint64,
	eventsDropped uint64,
) {
	// Check if enabled
	if !nt.IsEnabled() {
		return
	}

	// Scrub stack trace for privacy (remove detection metadata)
	scrubbedStack := privacy.ScrubMessage(stackTrace)

	// Build message
	message := fmt.Sprintf("Worker panic recovered: %v", panicValue)
	scrubbedMessage := privacy.ScrubMessage(message)

	// Build tags
	tags := map[string]string{
		"component":   "notification",
		"worker_type": workerType,
		"panic_type":  fmt.Sprintf("%T", panicValue),
	}

	// Build contexts
	contexts := map[string]any{
		"worker_state": map[string]any{
			"events_processed": eventsProcessed,
			"events_dropped":   eventsDropped,
		},
		"panic": map[string]any{
			"value":       scrubbedMessage,
			"stack_trace": scrubbedStack,
		},
	}

	// Report as critical level (worker crashed but recovered)
	nt.reporter.CaptureEvent(scrubbedMessage, SeverityCritical, tags, contexts)
}

// RateLimitExceeded reports sustained high rate limiting (indicating config issues or spam)
func (nt *NotificationTelemetry) RateLimitExceeded(
	droppedCount int,
	windowSeconds int,
	maxEvents int,
	dropRatePercent float64,
) {
	// Check if enabled
	if !nt.IsEnabled() {
		return
	}

	// Only report if drop rate exceeds configured threshold
	threshold := nt.config.RateLimitReportThreshold
	if threshold <= 0 {
		threshold = 50.0 // Fallback to default if misconfigured
	}
	if dropRatePercent < threshold {
		return
	}

	// Build message
	message := fmt.Sprintf("Notification rate limit exceeded: %d events dropped (%.1f%% drop rate)", droppedCount, dropRatePercent)

	// Build tags
	tags := map[string]string{
		"component": "notification",
		"subsystem": "rate_limiter",
		"drop_rate": fmt.Sprintf("%.1f", dropRatePercent),
		"severity":  "high_drop_rate",
	}

	// Build contexts
	contexts := map[string]any{
		"rate_limiter": map[string]any{
			"window_seconds":    windowSeconds,
			"max_events":        maxEvents,
			"dropped_count":     droppedCount,
			"drop_rate_percent": dropRatePercent,
		},
	}

	// Report as warning level (indicates configuration or usage issue)
	nt.reporter.CaptureEvent(message, SeverityWarning, tags, contexts)
}
