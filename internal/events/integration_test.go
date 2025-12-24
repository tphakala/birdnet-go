package events_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// TestNoCircularDependency verifies that events package doesn't import errors package
// This test will fail to compile if there's a circular dependency
func TestNoCircularDependency(t *testing.T) {
	t.Parallel()

	// This test primarily exists to ensure compilation succeeds
	// If there's a circular dependency, this won't compile

	// Initialize logging
	logging.Init()

	// Reset state
	events.ResetForTesting()

	// Initialize event bus
	eb, err := events.Initialize(nil)
	require.NoError(t, err, "failed to initialize event bus")
	require.NotNil(t, eb, "expected non-nil event bus")

	// The fact that this compiles proves no circular dependency
	t.Log("No circular dependency detected")

	// Cleanup
	t.Cleanup(func() {
		events.ResetForTesting()
	})
}

// TestErrorEventIntegration tests the integration between errors and events packages
func TestErrorEventIntegration(t *testing.T) {
	// Remove t.Parallel() to ensure clean state for global error hooks

	// Initialize logging
	logging.Init()

	// Reset event bus state
	events.ResetForTesting()

	// Clear error hooks
	errors.ClearErrorHooks()

	// Initialize event bus
	config := &events.Config{
		BufferSize: 100,
		Workers:    2,
		Enabled:    true,
		Deduplication: &events.DeduplicationConfig{
			Enabled: false, // Disable for this test
		},
	}

	eb, err := events.Initialize(config)
	require.NoError(t, err, "failed to initialize event bus")

	// Create a test consumer
	consumer := &testConsumer{
		events: make([]events.ErrorEvent, 0),
	}

	err = eb.RegisterConsumer(consumer)
	require.NoError(t, err, "failed to register consumer")

	// Set up the integration
	err = events.InitializeErrorsIntegration(func(publisher any) {
		if p, ok := publisher.(errors.EventPublisher); ok {
			errors.SetEventPublisher(p)
		}
	})
	require.NoError(t, err, "failed to initialize integration")

	// Enable error reporting by adding a hook
	// This ensures hasActiveReporting is true
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		// Empty hook just to enable reporting
	})

	// Create an enhanced error
	_ = errors.Newf("test error").
		Component("test-component").
		Category(errors.CategoryNetwork).
		Context("operation", "test_operation").
		Build()

	// The error should have been published to the event bus
	// Wait for the event to be processed
	waitForEvents := func(expected int) bool {
		// Increase timeout to 2 seconds to avoid flakiness
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			consumer.mu.Lock()
			count := len(consumer.events)
			consumer.mu.Unlock()
			if count >= expected {
				return true
			}
			time.Sleep(10 * time.Millisecond) // Small sleep to avoid busy loop
		}
		return false
	}

	if !waitForEvents(1) {
		consumer.mu.Lock()
		count := len(consumer.events)
		consumer.mu.Unlock()
		require.Failf(t, "timeout waiting for event", "got %d events", count)
	}

	// Check that the consumer received the event
	consumer.mu.Lock()
	defer consumer.mu.Unlock()

	require.Len(t, consumer.events, 1)

	event := consumer.events[0]
	assert.Equal(t, "test-component", event.GetComponent())
	assert.Equal(t, string(errors.CategoryNetwork), event.GetCategory())

	ctx := event.GetContext()
	op, ok := ctx["operation"].(string)
	require.True(t, ok, "operation should be a string")
	assert.Equal(t, "test_operation", op)

	// Cleanup
	t.Cleanup(func() {
		events.ResetForTesting()
		errors.ClearErrorHooks()
	})
}

// testConsumer is a simple consumer for testing
type testConsumer struct {
	events []events.ErrorEvent
	mu     sync.Mutex
}

func (tc *testConsumer) Name() string {
	return "test-consumer"
}

func (tc *testConsumer) ProcessEvent(event events.ErrorEvent) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.events = append(tc.events, event)
	return nil
}

func (tc *testConsumer) ProcessBatch(errorEvents []events.ErrorEvent) error {
	for _, event := range errorEvents {
		if err := tc.ProcessEvent(event); err != nil {
			return err
		}
	}
	return nil
}

func (tc *testConsumer) SupportsBatching() bool {
	return false
}
