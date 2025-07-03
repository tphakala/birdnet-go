package events_test

import (
	"fmt"
	"time"
	
	"github.com/tphakala/birdnet-go/internal/events"
)

// ExampleConfig_debug demonstrates how to enable debug logging for the event bus
func ExampleConfig_debug() {
	// Create configuration with debug logging enabled
	config := &events.Config{
		BufferSize: 1000,
		Workers:    4,
		Enabled:    true,
		Debug:      true, // Enable debug logging
		Deduplication: &events.DeduplicationConfig{
			Enabled:         true,
			Debug:           true, // Enable deduplication debug logging
			TTL:             5 * time.Minute,
			MaxEntries:      10000,
			CleanupInterval: 1 * time.Minute,
		},
	}
	
	// Initialize event bus
	eb, err := events.Initialize(config)
	if err != nil {
		fmt.Printf("Failed to initialize: %v\n", err)
		return
	}
	
	// Event bus will now log:
	// - Event publishing details with buffer metrics
	// - Worker processing times
	// - Consumer registration durations
	// - Slow consumer warnings (>100ms)
	// - Deduplication checks
	// - Enhanced performance metrics with rates
	
	// Shutdown when done
	_ = eb.Shutdown(5 * time.Second)
	
	// Output:
}