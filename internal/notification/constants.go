// Package notification provides shared constants for the notification system.
package notification

import "time"

// Circuit breaker state string representations.
// Used by both PushCircuitBreaker (circuit_breaker.go) and simpleCircuitBreaker (worker.go).
const (
	circuitStateClosed   = "closed"
	circuitStateHalfOpen = "half-open"
	circuitStateOpen     = "open"
	circuitStateUnknown  = "unknown"
)

// Time duration constants for notifications.
const (
	// DefaultDetectionExpiry is the default expiry time for detection notifications.
	DefaultDetectionExpiry = 24 * time.Hour

	// DefaultAlertExpiry is the default expiry time for alert notifications.
	DefaultAlertExpiry = 30 * time.Minute

	// DefaultResourceAlertExpiry is the expiry time for resource alert notifications.
	DefaultResourceAlertExpiry = 30 * time.Minute

	// DefaultQuickExpiry is the default short expiry for temporary notifications.
	DefaultQuickExpiry = 5 * time.Minute

	// DefaultHealthCheckInterval is the default interval between health checks.
	DefaultHealthCheckInterval = 60 * time.Second

	// DefaultHealthCheckTimeout is the default timeout for health checks.
	DefaultHealthCheckTimeout = 10 * time.Second

	// DefaultAlertThrottle is the default throttle period between repeated alerts.
	DefaultAlertThrottle = 5 * time.Minute

	// DefaultCleanupInterval is the default interval for cleanup tasks.
	DefaultCleanupInterval = 5 * time.Minute
)

// Numeric constants for notifications.
const (
	// PercentMultiplier is used to convert decimal confidence to percentage.
	PercentMultiplier = 100

	// DefaultMaxNotifications is the default maximum number of stored notifications.
	DefaultMaxNotifications = 1000

	// DefaultRateLimitMaxEvents is the default maximum rate-limited events.
	DefaultRateLimitMaxEvents = 100

	// DefaultChannelBufferSize is the default buffer size for notification channels.
	DefaultChannelBufferSize = 10

	// DefaultRequestsPerMinute is the default rate limit for requests per minute.
	DefaultRequestsPerMinute = 60

	// DefaultBurstSize is the default burst size for rate limiting.
	DefaultBurstSize = 10

	// DefaultBatchSize is the default batch size for worker processing.
	DefaultBatchSize = 10

	// DefaultBatchTimeoutMs is the default batch timeout in milliseconds.
	DefaultBatchTimeoutMs = 100

	// DefaultFailureThreshold is the default failure threshold for circuit breakers.
	DefaultFailureThreshold = 5

	// DefaultRecoveryTimeoutSeconds is the default recovery timeout in seconds.
	DefaultRecoveryTimeoutSeconds = 30

	// DefaultHalfOpenMaxEvents is the default max events in half-open state.
	DefaultHalfOpenMaxEvents = 3

	// DefaultMaxSummaryMessages is the maximum number of messages in summary.
	DefaultMaxSummaryMessages = 5

	// DefaultTruncateLength is the default truncation length for messages.
	DefaultTruncateLength = 100

	// DefaultMessageTruncateLength is the default truncation length for event messages.
	DefaultMessageTruncateLength = 500

	// DefaultScriptOutputTruncateLength is the truncation length for script output.
	DefaultScriptOutputTruncateLength = 512

	// DefaultRateLimitReportThreshold is the threshold percentage for rate limit reports.
	DefaultRateLimitReportThreshold = 50.0

	// JitterMultiplier is used in exponential backoff jitter calculations.
	JitterMultiplier = 2

	// JitterDivisor is used in exponential backoff jitter calculations.
	JitterDivisor = 100

	// SharedAddressMask is the mask byte for shared address space detection (100.64.0.0/10).
	SharedAddressMask = 0xC0

	// TestConfidenceValue is a sample confidence value for webhook testing.
	TestConfidenceValue = 0.95
)
