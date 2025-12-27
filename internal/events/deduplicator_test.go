package events

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestDeduplicatorBasic tests basic deduplication functionality
func TestDeduplicatorBasic(t *testing.T) {
	t.Parallel()

	log := logger.NewConsoleLogger("test", logger.LogLevelDebug)

	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             1 * time.Second,
		MaxEntries:      100,
		CleanupInterval: 0, // Disable automatic cleanup for test
	}

	dedup := NewErrorDeduplicator(config, log)
	t.Cleanup(dedup.Shutdown)

	// Create test event
	event := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "test error message",
		timestamp: time.Now(),
		context: map[string]any{
			"operation": "test_op",
		},
	}

	// First occurrence should be processed
	assert.True(t, dedup.ShouldProcess(event), "first occurrence should be processed")

	// Immediate duplicate should be suppressed
	assert.False(t, dedup.ShouldProcess(event), "immediate duplicate should be suppressed")

	// Check stats
	stats := dedup.GetStats()
	assert.Equal(t, uint64(2), stats.TotalSeen)
	assert.Equal(t, uint64(1), stats.TotalSuppressed)
	assert.Equal(t, 1, stats.CacheSize)

	// Wait for TTL to expire
	time.Sleep(1100 * time.Millisecond)

	// Same error after TTL should be processed
	assert.True(t, dedup.ShouldProcess(event), "error after TTL expiration should be processed")
}

// TestDeduplicatorDifferentErrors tests that different errors are not deduplicated
func TestDeduplicatorDifferentErrors(t *testing.T) {
	t.Parallel()

	log := logger.NewConsoleLogger("test", logger.LogLevelDebug)

	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      100,
		CleanupInterval: 0,
	}

	dedup := NewErrorDeduplicator(config, log)
	t.Cleanup(dedup.Shutdown)

	// Different components
	event1 := &mockErrorEvent{
		component: "component1",
		category:  "test-error",
		message:   "same message",
		timestamp: time.Now(),
	}

	event2 := &mockErrorEvent{
		component: "component2",
		category:  "test-error",
		message:   "same message",
		timestamp: time.Now(),
	}

	// Both should be processed
	assert.True(t, dedup.ShouldProcess(event1), "event1 should be processed")
	assert.True(t, dedup.ShouldProcess(event2), "event2 should be processed")

	// Different categories
	event3 := &mockErrorEvent{
		component: "component1",
		category:  "category1",
		message:   "same message",
		timestamp: time.Now(),
	}

	event4 := &mockErrorEvent{
		component: "component1",
		category:  "category2",
		message:   "same message",
		timestamp: time.Now(),
	}

	assert.True(t, dedup.ShouldProcess(event3), "event3 should be processed")
	assert.True(t, dedup.ShouldProcess(event4), "event4 should be processed")

	// Different messages
	event5 := &mockErrorEvent{
		component: "component1",
		category:  "category1",
		message:   "message1",
		timestamp: time.Now(),
	}

	event6 := &mockErrorEvent{
		component: "component1",
		category:  "category1",
		message:   "message2",
		timestamp: time.Now(),
	}

	assert.True(t, dedup.ShouldProcess(event5), "event5 should be processed")
	assert.True(t, dedup.ShouldProcess(event6), "event6 should be processed")
}

// TestDeduplicatorLRUEviction tests LRU eviction when cache is full
func TestDeduplicatorLRUEviction(t *testing.T) {
	t.Parallel()

	log := logger.NewConsoleLogger("test", logger.LogLevelDebug)

	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      3, // Small cache for testing
		CleanupInterval: 0,
	}

	dedup := NewErrorDeduplicator(config, log)
	t.Cleanup(dedup.Shutdown)

	// Add 3 different errors
	for i := range 3 {
		event := &mockErrorEvent{
			component: "test",
			category:  "test-error",
			message:   fmt.Sprintf("error %d", i),
			timestamp: time.Now(),
		}
		assert.True(t, dedup.ShouldProcess(event), "error %d should be processed", i)
	}

	// Cache should be full
	stats := dedup.GetStats()
	assert.Equal(t, 3, stats.CacheSize)

	// Access the first error again to make it most recently used
	event0 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "error 0",
		timestamp: time.Now(),
	}
	assert.False(t, dedup.ShouldProcess(event0), "duplicate of error 0 should be suppressed")

	// Add a new error, should evict error 1 (least recently used)
	event3 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "error 3",
		timestamp: time.Now(),
	}
	assert.True(t, dedup.ShouldProcess(event3), "error 3 should be processed")

	// Error 1 should have been evicted and now be processed again
	event1 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "error 1",
		timestamp: time.Now(),
	}
	assert.True(t, dedup.ShouldProcess(event1), "error 1 should be processed after eviction")
}

// TestDeduplicatorCleanup tests automatic cleanup of expired entries
func TestDeduplicatorCleanup(t *testing.T) {
	t.Parallel()

	log := logger.NewConsoleLogger("test", logger.LogLevelDebug)

	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             500 * time.Millisecond,
		MaxEntries:      100,
		CleanupInterval: 200 * time.Millisecond,
	}

	dedup := NewErrorDeduplicator(config, log)
	t.Cleanup(dedup.Shutdown)

	// Add some errors
	for i := range 5 {
		event := &mockErrorEvent{
			component: "test",
			category:  "test-error",
			message:   fmt.Sprintf("error %d", i),
			timestamp: time.Now(),
		}
		dedup.ShouldProcess(event)
	}

	// Check initial cache size
	stats := dedup.GetStats()
	assert.Equal(t, 5, stats.CacheSize)

	// Wait for cleanup to run after TTL expires
	time.Sleep(800 * time.Millisecond)

	// Cache should be empty after cleanup
	stats = dedup.GetStats()
	assert.Equal(t, 0, stats.CacheSize)
}

// TestDeduplicatorDisabled tests behavior when deduplication is disabled
func TestDeduplicatorDisabled(t *testing.T) {
	t.Parallel()

	log := logger.NewConsoleLogger("test", logger.LogLevelDebug)

	config := &DeduplicationConfig{
		Enabled: false,
	}

	dedup := NewErrorDeduplicator(config, log)
	t.Cleanup(dedup.Shutdown)

	event := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "test error",
		timestamp: time.Now(),
	}

	// All events should be processed when disabled
	for range 10 {
		assert.True(t, dedup.ShouldProcess(event), "all events should be processed when deduplication is disabled")
	}

	// Stats should show no suppression
	stats := dedup.GetStats()
	assert.Equal(t, uint64(0), stats.TotalSuppressed)
}

// TestDeduplicatorContext tests that context fields affect deduplication
func TestDeduplicatorContext(t *testing.T) {
	t.Parallel()

	log := logger.NewConsoleLogger("test", logger.LogLevelDebug)

	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      100,
		CleanupInterval: 0,
	}

	dedup := NewErrorDeduplicator(config, log)
	t.Cleanup(dedup.Shutdown)

	// Same error with different operation context
	event1 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "same error",
		timestamp: time.Now(),
		context: map[string]any{
			"operation": "operation1",
		},
	}

	event2 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "same error",
		timestamp: time.Now(),
		context: map[string]any{
			"operation": "operation2",
		},
	}

	// Different operations should not be deduplicated
	assert.True(t, dedup.ShouldProcess(event1), "event1 should be processed")
	assert.True(t, dedup.ShouldProcess(event2), "event2 with different operation should be processed")

	// Same operation should be deduplicated
	event3 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "same error",
		timestamp: time.Now(),
		context: map[string]any{
			"operation": "operation1",
		},
	}

	assert.False(t, dedup.ShouldProcess(event3), "event3 with same operation should be suppressed")
}
