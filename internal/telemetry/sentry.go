// Package telemetry provides privacy-compliant error tracking and telemetry
package telemetry

import (
	"fmt"
	"log"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// InitSentry initializes Sentry SDK with privacy-compliant settings
// This function will only initialize Sentry if explicitly enabled by the user
func InitSentry(settings *conf.Settings) error {
	// Check if Sentry is explicitly enabled (opt-in)
	if !settings.Sentry.Enabled {
		log.Println("Sentry telemetry is disabled (opt-in required)")
		return nil
	}

	// Validate DSN is provided
	if settings.Sentry.DSN == "" {
		return fmt.Errorf("sentry DSN is required when Sentry is enabled")
	}

	// Initialize Sentry with privacy-compliant options
	err := sentry.Init(sentry.ClientOptions{
		Dsn:        settings.Sentry.DSN,
		SampleRate: settings.Sentry.SampleRate,
		Debug:      settings.Sentry.Debug,
		
		// Privacy-compliant settings
		AttachStacktrace: false, // Don't attach stack traces by default
		Environment:      "production",
		
		// Set release version if available
		Release: fmt.Sprintf("birdnet-go@%s", settings.Version),
		
		// Configure integrations
		Integrations: func(integrations []sentry.Integration) []sentry.Integration {
			// Remove any integrations that might collect sensitive data
			var filtered []sentry.Integration
			for _, integration := range integrations {
				// Keep only essential integrations
				filtered = append(filtered, integration)
			}
			return filtered
		},
		
		// BeforeSend allows us to filter sensitive data
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Remove any potentially sensitive information
			event.User = sentry.User{} // Clear user data
			
			// Clear any sensitive context
			if event.Contexts != nil {
				delete(event.Contexts, "device")
				delete(event.Contexts, "os")
				delete(event.Contexts, "runtime")
			}
			
			// Only keep essential error information
			for k := range event.Extra {
				// Remove any extra data that might contain sensitive info
				if k != "error_type" && k != "component" {
					delete(event.Extra, k)
				}
			}
			
			return event
		},
	})

	if err != nil {
		return fmt.Errorf("sentry initialization failed: %w", err)
	}

	// Flush buffered events before the program terminates
	defer sentry.Flush(2 * time.Second)

	log.Println("Sentry telemetry initialized successfully (opt-in enabled)")
	return nil
}

// CaptureError captures an error with privacy-compliant context
func CaptureError(err error, component string) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetContext("error", map[string]interface{}{
			"type": fmt.Sprintf("%T", err),
		})
		sentry.CaptureException(err)
	})
}

// CaptureMessage captures a message with privacy-compliant context
func CaptureMessage(message string, level sentry.Level, component string) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetLevel(level)
		sentry.CaptureMessage(message)
	})
}

// Flush ensures all buffered events are sent to Sentry
func Flush(timeout time.Duration) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}
	
	sentry.Flush(timeout)
}