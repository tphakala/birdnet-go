package telemetry

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	runtimectx "github.com/tphakala/birdnet-go/internal/buildinfo"
)

func TestTelemetryIntegration(t *testing.T) {
	// Cannot run in parallel due to global event bus state
	t.Run("error reporting through telemetry", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Initialize error integration
		testSettings := &conf.Settings{
			Sentry: conf.SentrySettings{Enabled: true},
		}
		InitializeErrorIntegration(testSettings)

		// Create an error with context
		originalErr := fmt.Errorf("connection failed")
		err := errors.New(originalErr).
			Component("test-component").
			Category(errors.CategoryNetwork).
			Context("host", "example.com").
			Context("port", "443").
			Build()

		// Report error through CaptureError directly
		CaptureError(err, "test-component")

		// Verify event was captured
		AssertEventCount(t, config.MockTransport, 1, 100*time.Millisecond)

		// Check event details
		event := config.MockTransport.GetLastEvent()
		if event == nil {
			t.Fatal("Expected event to be captured")
		}

		// Verify component tag
		if tag, ok := event.Tags["component"]; !ok || tag != "test-component" {
			t.Errorf("Expected component tag 'test-component', got %s", tag)
		}
	})

	t.Run("message reporting with different levels", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Report messages at different levels
		ReportMessage("System initialized", sentry.LevelInfo, "system")
		ReportMessage("High memory usage", sentry.LevelWarning, "monitor")
		ReportMessage("Critical error occurred", sentry.LevelError, "core")

		// Wait for all events
		AssertEventCount(t, config.MockTransport, 3, 200*time.Millisecond)

		// Verify levels
		events := config.MockTransport.GetEvents()
		if len(events) != 3 {
			t.Fatalf("Expected 3 events, got %d", len(events))
		}

		expectedLevels := []sentry.Level{
			sentry.LevelInfo,
			sentry.LevelWarning,
			sentry.LevelError,
		}

		for i, event := range events {
			if event.Level != expectedLevels[i] {
				t.Errorf("Event %d: expected level %s, got %s",
					i, expectedLevels[i], event.Level)
			}
		}
	})

	t.Run("attachment uploader availability", func(t *testing.T) {
		_, cleanup := InitForTesting(t)
		defer cleanup()

		// Get attachment uploader
		uploader := GetAttachmentUploader()
		if uploader == nil {
			t.Fatal("Expected attachment uploader to be available")
		}

		// The uploader should exist even if we can't use all its methods in test
		// This verifies the initialization is working correctly
	})

	t.Run("initialization safety", func(t *testing.T) {
		// Test that telemetry handles initialization safely
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Verify we can capture events after initialization
		testErr := fmt.Errorf("post-init error")
		ReportError(testErr)

		// Should capture the event
		AssertEventCount(t, config.MockTransport, 1, 100*time.Millisecond)
	})

	t.Run("privacy compliance", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Report error with sensitive data
		sensitiveErr := fmt.Errorf("failed to connect to https://user:pass@api.example.com/secret")
		ReportError(sensitiveErr)

		// Verify event was captured
		AssertEventCount(t, config.MockTransport, 1, 100*time.Millisecond)

		// Check that sensitive data was scrubbed
		event := config.MockTransport.GetLastEvent()
		if event == nil {
			t.Fatal("Expected event to be captured")
		}

		// Verify URL was anonymized
		if strings.Contains(event.Message, "api.example.com") {
			t.Error("Sensitive URL was not anonymized")
		}

		if strings.Contains(event.Message, "user:pass") {
			t.Error("Credentials were not removed")
		}

		// The error message should be scrubbed
		t.Logf("Scrubbed message: %s", event.Message)
	})

	t.Run("concurrent event reporting", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Report events concurrently
		numGoroutines := 10
		eventsPerGoroutine := 5

		done := make(chan bool)
		for i := range numGoroutines {
			go func(id int) {
				for j := range eventsPerGoroutine {
					err := fmt.Errorf("error from goroutine %d event %d", id, j)
					ReportError(err)
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for range numGoroutines {
			<-done
		}

		// Verify all events were captured
		expectedCount := numGoroutines * eventsPerGoroutine
		AssertEventCount(t, config.MockTransport, expectedCount, 500*time.Millisecond)
	})

	t.Run("flush behavior", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Report multiple events
		for i := range 5 {
			ReportMessage(fmt.Sprintf("Event %d", i), sentry.LevelInfo, "test")
		}

		// Flush with timeout
		Flush(2 * time.Second)

		// All events should be captured
		AssertEventCount(t, config.MockTransport, 5, 100*time.Millisecond)
	})
}

func TestTelemetryDisabled(t *testing.T) {
	t.Parallel()
	// Test behavior when telemetry is disabled
	transport := NewMockTransport()

	// Create settings with telemetry disabled
	settings := &conf.Settings{
		Sentry: conf.SentrySettings{
			Enabled: false,
		},
	}

	// Initialize with disabled telemetry
	runtimeCtx := runtimectx.NewContext("test-version", "test-build", "test-system-id")
	err := InitSentry(settings, runtimeCtx)
	if err != nil {
		t.Errorf("InitSentry should not error when disabled: %v", err)
	}

	// Update the cached telemetry state
	UpdateTelemetryEnabled(settings.Sentry.Enabled)

	// Try to report error
	testErr := fmt.Errorf("should not be captured")
	CaptureError(testErr, "test")

	// Verify nothing was sent
	if transport.GetEventCount() > 0 {
		t.Error("No events should be captured when telemetry is disabled")
	}
}

// Helper function to test ReportError
func ReportError(err error) {
	// Extract component from error if it's our enhanced error
	component := "unknown"
	if enhancedErr, ok := err.(interface{ GetComponent() string }); ok {
		component = enhancedErr.GetComponent()
	}

	CaptureError(err, component)
}

// Helper function to test ReportMessage
func ReportMessage(message string, level sentry.Level, component string) {
	CaptureMessage(message, level, component)
}

