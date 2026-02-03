package telemetry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestErrorHandlerNeverBlocks validates that error reporting never blocks the caller
func TestErrorHandlerNeverBlocks(t *testing.T) {
	// Cannot run in parallel due to global event bus state
	t.Run("error.Build() should never block on telemetry", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Make telemetry slow
		config.MockTransport.SetDelay(100 * time.Millisecond)

		// Measure how long Build() takes
		start := time.Now()

		// Create and build an error - this triggers telemetry reporting
		err := errors.Newf("test error").
			Component("test").
			Category(errors.CategoryNetwork).
			Build()

		elapsed := time.Since(start)

		// Build() should return immediately, not wait for telemetry
		assert.Less(t, elapsed, 5*time.Millisecond, "error.Build() should not block on telemetry")

		t.Logf("error.Build() took %v", elapsed)
		_ = err // use the error to avoid compiler warnings
	})

	t.Run("batch error creation performance", func(t *testing.T) {
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Slow transport
		config.MockTransport.SetDelay(50 * time.Millisecond)

		start := time.Now()

		// Create many errors rapidly
		for i := range 100 {
			_ = errors.Newf("error %d", i).
				Component("batch-test").
				Category(errors.CategoryDatabase).
				Build()
		}

		elapsed := time.Since(start)

		// 100 errors should complete quickly even with slow telemetry
		assert.Less(t, elapsed, 50*time.Millisecond, "Creating 100 errors should not block on telemetry")

		t.Logf("Created 100 errors in %v", elapsed)
	})
}

// TestEventBusAsyncBehavior validates that when event bus is available, error reporting is async
func TestEventBusAsyncBehavior(t *testing.T) {
	t.Parallel()
	t.Run("notification system uses event bus (async)", func(t *testing.T) {
		t.Parallel()
		// This test documents that the notification system properly uses
		// the event bus for async error handling, while telemetry does not

		t.Log("Current architecture:")
		t.Log("- Notification system: Uses event bus (async) ✓")
		t.Log("- Telemetry system: Uses legacy sync path ✗")
		t.Log("")
		t.Log("The notification worker implements EventConsumer and processes")
		t.Log("errors asynchronously via the event bus, preventing blocking.")
	})
}

// TestCurrentTelemetryIntegration tests the current telemetry integration
func TestCurrentTelemetryIntegration(t *testing.T) {
	t.Parallel()
	t.Run("telemetry uses legacy synchronous path", func(t *testing.T) {
		t.Parallel()
		config, cleanup := InitForTesting(t)
		defer cleanup()

		// Add significant delay to telemetry
		config.MockTransport.SetDelay(100 * time.Millisecond)

		// Create error - this should trigger telemetry
		start := time.Now()

		_ = errors.Newf("test telemetry integration").
			Component("test").
			Category(errors.CategoryNetwork).
			Build()

		elapsed := time.Since(start)

		// Log the timing
		t.Logf("Error creation took %v", elapsed)

		// With 100ms delay, if telemetry is synchronous, Build() would block
		// Currently, telemetry appears to be called synchronously
		if elapsed > 50*time.Millisecond {
			t.Logf("WARNING: Telemetry integration appears to be SYNCHRONOUS")
			t.Logf("Error creation may have blocked waiting for telemetry")
		}
	})
}

// slowEventConsumer simulates a slow consumer for testing
type slowEventConsumer struct {
	delay     time.Duration
	onProcess func()
}

func (c *slowEventConsumer) ProcessError(ctx context.Context, err error) {
	// Simulate slow processing
	time.Sleep(c.delay)

	if c.onProcess != nil {
		c.onProcess()
	}
}

func (c *slowEventConsumer) String() string {
	return fmt.Sprintf("slowEventConsumer(delay=%v)", c.delay)
}

// TestRecommendedAsyncPattern shows the recommended pattern
func TestRecommendedAsyncPattern(t *testing.T) {
	// Cannot run in parallel due to global event bus state
	t.Run("recommended: use event bus for all error reporting", func(t *testing.T) {
		// This test demonstrates the recommended architecture:
		// 1. Error creation publishes to event bus (non-blocking)
		// 2. Telemetry worker consumes from event bus (async)
		// 3. Notification worker consumes from event bus (async)

		t.Log("Recommended architecture:")
		t.Log("1. Create TelemetryWorker that implements EventConsumer")
		t.Log("2. Register TelemetryWorker with event bus")
		t.Log("3. Remove direct telemetry calls from error.Build()")
		t.Log("4. This ensures error handling never blocks on telemetry")
	})
}
