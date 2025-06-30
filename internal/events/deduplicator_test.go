package events

import (
	"fmt"
	"testing"
	"time"
	
	"github.com/tphakala/birdnet-go/internal/logging"
)

// TestDeduplicatorBasic tests basic deduplication functionality
func TestDeduplicatorBasic(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	logger := logging.ForService("test")
	
	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             1 * time.Second,
		MaxEntries:      100,
		CleanupInterval: 0, // Disable automatic cleanup for test
	}
	
	dedup := NewErrorDeduplicator(config, logger)
	defer dedup.Shutdown()
	
	// Create test event
	event := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "test error message",
		timestamp: time.Now(),
		context: map[string]interface{}{
			"operation": "test_op",
		},
	}
	
	// First occurrence should be processed
	if !dedup.ShouldProcess(event) {
		t.Error("first occurrence should be processed")
	}
	
	// Immediate duplicate should be suppressed
	if dedup.ShouldProcess(event) {
		t.Error("immediate duplicate should be suppressed")
	}
	
	// Check stats
	stats := dedup.GetStats()
	if stats.TotalSeen != 2 {
		t.Errorf("expected 2 total seen, got %d", stats.TotalSeen)
	}
	if stats.TotalSuppressed != 1 {
		t.Errorf("expected 1 suppressed, got %d", stats.TotalSuppressed)
	}
	if stats.CacheSize != 1 {
		t.Errorf("expected cache size 1, got %d", stats.CacheSize)
	}
	
	// Wait for TTL to expire
	time.Sleep(1100 * time.Millisecond)
	
	// Same error after TTL should be processed
	if !dedup.ShouldProcess(event) {
		t.Error("error after TTL expiration should be processed")
	}
}

// TestDeduplicatorDifferentErrors tests that different errors are not deduplicated
func TestDeduplicatorDifferentErrors(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	logger := logging.ForService("test")
	
	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      100,
		CleanupInterval: 0,
	}
	
	dedup := NewErrorDeduplicator(config, logger)
	defer dedup.Shutdown()
	
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
	if !dedup.ShouldProcess(event1) {
		t.Error("event1 should be processed")
	}
	if !dedup.ShouldProcess(event2) {
		t.Error("event2 should be processed")
	}
	
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
	
	if !dedup.ShouldProcess(event3) {
		t.Error("event3 should be processed")
	}
	if !dedup.ShouldProcess(event4) {
		t.Error("event4 should be processed")
	}
	
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
	
	if !dedup.ShouldProcess(event5) {
		t.Error("event5 should be processed")
	}
	if !dedup.ShouldProcess(event6) {
		t.Error("event6 should be processed")
	}
}

// TestDeduplicatorLRUEviction tests LRU eviction when cache is full
func TestDeduplicatorLRUEviction(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	logger := logging.ForService("test")
	
	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      3, // Small cache for testing
		CleanupInterval: 0,
	}
	
	dedup := NewErrorDeduplicator(config, logger)
	defer dedup.Shutdown()
	
	// Add 3 different errors
	for i := 0; i < 3; i++ {
		event := &mockErrorEvent{
			component: "test",
			category:  "test-error",
			message:   fmt.Sprintf("error %d", i),
			timestamp: time.Now(),
		}
		if !dedup.ShouldProcess(event) {
			t.Errorf("error %d should be processed", i)
		}
	}
	
	// Cache should be full
	stats := dedup.GetStats()
	if stats.CacheSize != 3 {
		t.Errorf("expected cache size 3, got %d", stats.CacheSize)
	}
	
	// Access the first error again to make it most recently used
	event0 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "error 0",
		timestamp: time.Now(),
	}
	if dedup.ShouldProcess(event0) {
		t.Error("duplicate of error 0 should be suppressed")
	}
	
	// Add a new error, should evict error 1 (least recently used)
	event3 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "error 3",
		timestamp: time.Now(),
	}
	if !dedup.ShouldProcess(event3) {
		t.Error("error 3 should be processed")
	}
	
	// Error 1 should have been evicted and now be processed again
	event1 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "error 1",
		timestamp: time.Now(),
	}
	if !dedup.ShouldProcess(event1) {
		t.Error("error 1 should be processed after eviction")
	}
}

// TestDeduplicatorCleanup tests automatic cleanup of expired entries
func TestDeduplicatorCleanup(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	logger := logging.ForService("test")
	
	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             500 * time.Millisecond,
		MaxEntries:      100,
		CleanupInterval: 200 * time.Millisecond,
	}
	
	dedup := NewErrorDeduplicator(config, logger)
	defer dedup.Shutdown()
	
	// Add some errors
	for i := 0; i < 5; i++ {
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
	if stats.CacheSize != 5 {
		t.Errorf("expected initial cache size 5, got %d", stats.CacheSize)
	}
	
	// Wait for cleanup to run after TTL expires
	time.Sleep(800 * time.Millisecond)
	
	// Cache should be empty after cleanup
	stats = dedup.GetStats()
	if stats.CacheSize != 0 {
		t.Errorf("expected cache size 0 after cleanup, got %d", stats.CacheSize)
	}
}

// TestDeduplicatorDisabled tests behavior when deduplication is disabled
func TestDeduplicatorDisabled(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	logger := logging.ForService("test")
	
	config := &DeduplicationConfig{
		Enabled: false,
	}
	
	dedup := NewErrorDeduplicator(config, logger)
	defer dedup.Shutdown()
	
	event := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "test error",
		timestamp: time.Now(),
	}
	
	// All events should be processed when disabled
	for i := 0; i < 10; i++ {
		if !dedup.ShouldProcess(event) {
			t.Error("all events should be processed when deduplication is disabled")
		}
	}
	
	// Stats should show no suppression
	stats := dedup.GetStats()
	if stats.TotalSuppressed != 0 {
		t.Errorf("expected 0 suppressed when disabled, got %d", stats.TotalSuppressed)
	}
}

// TestDeduplicatorContext tests that context fields affect deduplication
func TestDeduplicatorContext(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	logger := logging.ForService("test")
	
	config := &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      100,
		CleanupInterval: 0,
	}
	
	dedup := NewErrorDeduplicator(config, logger)
	defer dedup.Shutdown()
	
	// Same error with different operation context
	event1 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "same error",
		timestamp: time.Now(),
		context: map[string]interface{}{
			"operation": "operation1",
		},
	}
	
	event2 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "same error",
		timestamp: time.Now(),
		context: map[string]interface{}{
			"operation": "operation2",
		},
	}
	
	// Different operations should not be deduplicated
	if !dedup.ShouldProcess(event1) {
		t.Error("event1 should be processed")
	}
	if !dedup.ShouldProcess(event2) {
		t.Error("event2 with different operation should be processed")
	}
	
	// Same operation should be deduplicated
	event3 := &mockErrorEvent{
		component: "test",
		category:  "test-error",
		message:   "same error",
		timestamp: time.Now(),
		context: map[string]interface{}{
			"operation": "operation1",
		},
	}
	
	if dedup.ShouldProcess(event3) {
		t.Error("event3 with same operation should be suppressed")
	}
}