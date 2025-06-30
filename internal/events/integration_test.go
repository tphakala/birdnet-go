package events_test

import (
	"sync"
	"testing"
	"time"
	
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
	if err != nil {
		t.Fatalf("failed to initialize event bus: %v", err)
	}
	
	if eb == nil {
		t.Fatal("expected non-nil event bus")
	}
	
	// The fact that this compiles proves no circular dependency
	t.Log("No circular dependency detected")
	
	// Cleanup
	t.Cleanup(func() {
		events.ResetForTesting()
	})
}

// TestErrorEventIntegration tests the integration between errors and events packages
func TestErrorEventIntegration(t *testing.T) {
	t.Parallel()
	
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
	if err != nil {
		t.Fatalf("failed to initialize event bus: %v", err)
	}
	
	// Create a test consumer
	consumer := &testConsumer{
		events: make([]events.ErrorEvent, 0),
	}
	
	err = eb.RegisterConsumer(consumer)
	if err != nil {
		t.Fatalf("failed to register consumer: %v", err)
	}
	
	// Set up the integration
	err = events.InitializeErrorsIntegration(func(publisher interface{}) {
		if p, ok := publisher.(errors.EventPublisher); ok {
			errors.SetEventPublisher(p)
		}
	})
	if err != nil {
		t.Fatalf("failed to initialize integration: %v", err)
	}
	
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
		deadline := time.Now().Add(100 * time.Millisecond)
		for time.Now().Before(deadline) {
			consumer.mu.Lock()
			count := len(consumer.events)
			consumer.mu.Unlock()
			if count >= expected {
				return true
			}
			time.Sleep(1 * time.Millisecond) // Small sleep to avoid busy loop
		}
		return false
	}
	
	if !waitForEvents(1) {
		consumer.mu.Lock()
		count := len(consumer.events)
		consumer.mu.Unlock()
		t.Fatalf("timeout waiting for event, got %d events", count)
	}
	
	// Check that the consumer received the event
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	
	if len(consumer.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(consumer.events))
	}
	
	event := consumer.events[0]
	if event.GetComponent() != "test-component" {
		t.Errorf("expected component 'test-component', got %s", event.GetComponent())
	}
	if event.GetCategory() != string(errors.CategoryNetwork) {
		t.Errorf("expected category 'network', got %s", event.GetCategory())
	}
	
	ctx := event.GetContext()
	if op, ok := ctx["operation"].(string); !ok || op != "test_operation" {
		t.Errorf("expected operation 'test_operation', got %v", ctx["operation"])
	}
	
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