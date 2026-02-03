package telemetry

import (
	"fmt"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestE2ECompleteFlow tests the complete telemetry flow
func TestE2ECompleteFlow(t *testing.T) {
	// Cannot run in parallel due to global event bus state
	config, cleanup := InitForTesting(t)
	defer cleanup()

	t.Run("error to telemetry flow", func(t *testing.T) {
		// Create enhanced error
		enhancedErr := errors.Newf("test error").
			Component("test-component").
			Category(errors.CategoryNetwork).
			Build()

		// Capture through telemetry
		CaptureError(enhancedErr, enhancedErr.GetComponent())

		// Verify capture
		AssertEventCount(t, config.MockTransport, 1, 100*time.Millisecond)

		// Check actual event
		event := config.MockTransport.GetLastEvent()
		require.NotNil(t, event, "Expected event to be captured")
		t.Logf("Captured event message: %s", event.Message)
		AssertEventTag(t, config.MockTransport, event.Message, "component", "test-component")
	})

	t.Run("privacy scrubbing", func(t *testing.T) {
		config.MockTransport.Clear()

		// Error with sensitive URL
		err := fmt.Errorf("failed to connect to https://user:pass@api.example.com/secret")
		CaptureError(err, "api-client")

		AssertEventCount(t, config.MockTransport, 1, 100*time.Millisecond)

		event := config.MockTransport.GetLastEvent()
		require.NotNil(t, event, "Expected event to be captured")
		// Verify URL was scrubbed
		assert.NotContains(t, event.Message, "api.example.com", "URL domain should be anonymized")
		assert.NotContains(t, event.Message, "user:pass", "Credentials should be removed")
	})

	t.Run("concurrent reporting", func(t *testing.T) {
		config.MockTransport.Clear()

		// Report errors concurrently
		done := make(chan bool, 10)
		for i := range 10 {
			go func(id int) {
				err := fmt.Errorf("concurrent error %d", id)
				CaptureError(err, fmt.Sprintf("component-%d", id))
				done <- true
			}(i)
		}

		// Wait for all
		for range 10 {
			<-done
		}

		// Should capture all
		AssertEventCount(t, config.MockTransport, 10, 500*time.Millisecond)
	})
}

// TestE2EMessageFlow tests message reporting flow
func TestE2EMessageFlow(t *testing.T) {
	// Cannot run in parallel due to global event bus state
	config, cleanup := InitForTesting(t)
	defer cleanup()

	tests := []struct {
		message   string
		level     sentry.Level
		component string
	}{
		{"System started", sentry.LevelInfo, "system"},
		{"High memory usage", sentry.LevelWarning, "monitor"},
		{"Critical failure", sentry.LevelError, "core"},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			config.MockTransport.Clear()

			CaptureMessage(tt.message, tt.level, tt.component)

			AssertEventCount(t, config.MockTransport, 1, 100*time.Millisecond)
			AssertEventLevel(t, config.MockTransport, tt.message, tt.level)
			AssertEventTag(t, config.MockTransport, tt.message, "component", tt.component)
		})
	}
}
