package events

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// BenchmarkFastPathNoConsumers tests the performance when no consumers are registered
func BenchmarkFastPathNoConsumers(b *testing.B) {
	// No logging initialization needed for benchmarks

	// Reset global state
	ResetForTesting()
	errors.ClearErrorHooks()

	// Initialize event bus but don't register any consumers
	_, err := Initialize(nil)
	if err != nil {
		b.Fatalf("failed to initialize event bus: %v", err)
	}

	// Set up integration with errors package
	err = InitializeErrorsIntegration(func(publisher any) {
		if p, ok := publisher.(errors.EventPublisher); ok {
			errors.SetEventPublisher(p)
		}
	})
	if err != nil {
		b.Fatalf("failed to initialize integration: %v", err)
	}

	// Create a test event
	testEvent := &mockErrorEvent{
		component: "benchmark",
		category:  "test",
		message:   "benchmark test message",
		timestamp: time.Now(),
	}

	// Get event bus for direct testing
	eb := GetEventBus()
	if eb == nil {
		b.Fatal("event bus should not be nil")
	}

	b.ReportAllocs()

	// Benchmark the fast path
	for b.Loop() {
		_ = eb.TryPublish(testEvent)
	}
}

// BenchmarkWithConsumer tests the performance with a consumer registered
func BenchmarkWithConsumer(b *testing.B) {
	// No logging initialization needed for benchmarks

	// Reset global state
	ResetForTesting()
	errors.ClearErrorHooks()

	// Initialize event bus
	config := &Config{
		BufferSize: 10000,
		Workers:    4,
		Enabled:    true,
		Deduplication: &DeduplicationConfig{
			Enabled: false, // Disable deduplication for cleaner benchmark
		},
	}

	eb, err := Initialize(config)
	if err != nil {
		b.Fatalf("failed to initialize event bus: %v", err)
	}

	// Register a simple consumer
	consumer := &mockConsumer{
		name: "benchmark-consumer",
	}
	err = eb.RegisterConsumer(consumer)
	if err != nil {
		b.Fatalf("failed to register consumer: %v", err)
	}

	// Create a test event
	testEvent := &mockErrorEvent{
		component: "benchmark",
		category:  "test",
		message:   "benchmark test message",
		timestamp: time.Now(),
	}

	b.ReportAllocs()

	// Benchmark with consumer
	for b.Loop() {
		_ = eb.TryPublish(testEvent)
	}
}

// BenchmarkErrorCreationNoReporting tests error creation when reporting is disabled
func BenchmarkErrorCreationNoReporting(b *testing.B) {
	// Ensure no reporting is active
	errors.SetTelemetryReporter(nil)
	errors.ClearErrorHooks()

	b.ReportAllocs()

	for b.Loop() {
		_ = errors.Newf("test error %d", 42).
			Component("benchmark").
			Category(errors.CategoryGeneric).
			Build()
	}
}

// BenchmarkErrorCreationWithReporting tests error creation when reporting is enabled
func BenchmarkErrorCreationWithReporting(b *testing.B) {
	// Enable reporting by adding a hook
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		// Empty hook just to enable reporting
	})

	b.ReportAllocs()

	for b.Loop() {
		_ = errors.Newf("test error %d", 42).
			Component("benchmark").
			Category(errors.CategoryGeneric).
			Build()
	}

	// Cleanup
	b.Cleanup(func() {
		errors.ClearErrorHooks()
	})
}

// BenchmarkHasActiveConsumers tests the performance of the flag check
func BenchmarkHasActiveConsumers(b *testing.B) {
	// Reset state
	ResetForTesting()

	b.ReportAllocs()

	for b.Loop() {
		_ = HasActiveConsumers()
	}
}
