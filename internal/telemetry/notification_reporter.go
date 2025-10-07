// Package telemetry provides the Sentry implementation of notification telemetry reporting
package telemetry

import (
	"sync/atomic"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Why Interface-Based Design for Notification Telemetry?
//
// Unlike other packages (e.g., datastore) that directly call telemetry functions,
// the notification package uses an interface-based approach for several reasons:
//
// 1. TESTABILITY: The notification package has extensive test coverage (15+ tests)
//    that need to verify telemetry is called correctly WITHOUT requiring a full
//    Sentry setup. The interface allows easy mocking with mockTelemetryReporter.
//
// 2. ISOLATION: Notification tests run frequently and should be fast. They shouldn't
//    depend on telemetry infrastructure, network, or external services. The interface
//    provides clean isolation.
//
// 3. FLEXIBILITY: While currently using Sentry, the interface allows swapping
//    implementations (e.g., local logging, different backends) without changing
//    notification package code.
//
// 4. DEPENDENCY INJECTION: The notification service is initialized early and may
//    need different telemetry behavior in different contexts (testing, development,
//    production). The interface makes this explicit and configurable.
//
// 5. NO CIRCULAR IMPORTS: While telemetry could import notification (one-way),
//    the interface approach makes the dependency direction explicit and prevents
//    future circular import issues as both packages evolve.
//
// Trade-off: Slightly more code (interface + implementation) vs. direct calls,
// but the benefits in testability and flexibility outweigh the cost.
//
// Note: If notification package tests become simpler or telemetry becomes
// optional, we could simplify to direct calls like datastore does.

// SentryNotificationReporter implements notification.TelemetryReporter for Sentry
type SentryNotificationReporter struct {
	enabled bool
}

// NewNotificationReporter creates a new Sentry-backed notification telemetry reporter
func NewNotificationReporter(enabled bool) notification.TelemetryReporter {
	return &SentryNotificationReporter{
		enabled: enabled,
	}
}

// CaptureError reports an error with component context to Sentry
func (r *SentryNotificationReporter) CaptureError(err error, component string) {
	if !r.enabled {
		return
	}

	// Use existing telemetry CaptureError function
	CaptureError(err, component)
}

// CaptureEvent reports a custom event with tags and contexts to Sentry
func (r *SentryNotificationReporter) CaptureEvent(message, level string, tags map[string]string, contexts map[string]interface{}) {
	if !r.enabled {
		return
	}

	// Skip settings check in test mode
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
		if settings == nil || !settings.Sentry.Enabled {
			return
		}
	}

	// Scrub message for privacy
	scrubbedMessage := privacy.ScrubMessage(message)

	// Convert string level to sentry.Level
	sentryLevel := convertToSentryLevel(level)

	// Log the event being sent (privacy-safe)
	logTelemetryDebug(nil, "sending notification event",
		"event_type", "notification",
		"level", level,
		"tags", tags,
	)

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentryLevel)

		// Set all provided tags
		for key, value := range tags {
			scope.SetTag(key, value)
		}

		// Set all provided contexts
		for key, value := range contexts {
			// Convert interface{} to map for Sentry context
			if contextMap, ok := value.(map[string]interface{}); ok {
				scope.SetContext(key, contextMap)
			}
		}

		// Capture as message event
		sentry.CaptureMessage(scrubbedMessage)
	})

	// Log successful submission
	logTelemetryDebug(nil, "notification event sent successfully",
		"level", level,
		"component", tags["component"],
	)
}

// IsEnabled returns whether telemetry reporting is enabled
func (r *SentryNotificationReporter) IsEnabled() bool {
	if !r.enabled {
		return false
	}

	// Skip settings check in test mode
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
		return settings != nil && settings.Sentry.Enabled
	}

	return true
}

// convertToSentryLevel converts string level to sentry.Level
func convertToSentryLevel(level string) sentry.Level {
	switch level {
	case "debug":
		return sentry.LevelDebug
	case "info":
		return sentry.LevelInfo
	case "warning":
		return sentry.LevelWarning
	case "error":
		return sentry.LevelError
	case "critical", "fatal":
		return sentry.LevelFatal
	default:
		return sentry.LevelInfo
	}
}
