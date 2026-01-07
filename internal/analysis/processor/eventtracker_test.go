package processor

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// =============================================================================
// StandardEventBehavior Tests
// =============================================================================

func TestStandardEventBehavior(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		lastEventTime time.Time
		timeout       time.Duration
		wantAllow     bool
	}{
		{
			name:          "allows event when timeout exceeded",
			lastEventTime: time.Now().Add(-2 * time.Second),
			timeout:       1 * time.Second,
			wantAllow:     true,
		},
		{
			name:          "blocks event within timeout",
			lastEventTime: time.Now(),
			timeout:       1 * time.Second,
			wantAllow:     false,
		},
		{
			name:          "allows event at exactly timeout boundary",
			lastEventTime: time.Now().Add(-1 * time.Second),
			timeout:       1 * time.Second,
			wantAllow:     true, // >= means exactly at boundary is allowed
		},
		{
			name:          "zero timeout always allows",
			lastEventTime: time.Now(),
			timeout:       0,
			wantAllow:     true,
		},
		{
			name:          "negative timeout allows (time.Since always >= negative)",
			lastEventTime: time.Now(),
			timeout:       -1 * time.Second,
			wantAllow:     true,
		},
		{
			name:          "very old event allows",
			lastEventTime: time.Now().Add(-24 * time.Hour),
			timeout:       1 * time.Minute,
			wantAllow:     true,
		},
		{
			name:          "zero time (epoch) allows",
			lastEventTime: time.Time{},
			timeout:       1 * time.Second,
			wantAllow:     true, // time.Since(zero) is huge
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := StandardEventBehavior(tt.lastEventTime, tt.timeout)
			assert.Equal(t, tt.wantAllow, got)
		})
	}
}

// =============================================================================
// EventHandler Tests
// =============================================================================

func TestEventHandler_ShouldHandleEvent_FirstEventAlwaysAllowed(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// First event for any species should always be allowed
	assert.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
	assert.True(t, handler.ShouldHandleEvent("House Sparrow", 60*time.Second))
	assert.True(t, handler.ShouldHandleEvent("Blue Jay", 60*time.Second))
}

func TestEventHandler_ShouldHandleEvent_BlocksWithinTimeout(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// First event allowed
	require.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))

	// Immediate second event blocked
	assert.False(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
}

func TestEventHandler_ShouldHandleEvent_CaseInsensitive(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// First event with mixed case
	require.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))

	// Same species with different case should be blocked
	assert.False(t, handler.ShouldHandleEvent("american robin", 60*time.Second))
	assert.False(t, handler.ShouldHandleEvent("AMERICAN ROBIN", 60*time.Second))
	assert.False(t, handler.ShouldHandleEvent("AmErIcAn RoBiN", 60*time.Second))
}

func TestEventHandler_ShouldHandleEvent_SpeciesIndependence(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// Track event for species A
	require.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))

	// Species B should still be allowed (independent tracking)
	assert.True(t, handler.ShouldHandleEvent("House Sparrow", 60*time.Second))

	// Species A still blocked
	assert.False(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
}

func TestEventHandler_ResetEvent(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// Track event
	require.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
	require.False(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))

	// Reset the event
	handler.ResetEvent("American Robin")

	// Should be allowed again
	assert.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
}

func TestEventHandler_ResetEvent_CaseInsensitive(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// Track with one case
	require.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
	require.False(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))

	// Reset with different case
	handler.ResetEvent("AMERICAN ROBIN")

	// Should be allowed (reset worked despite case difference)
	assert.True(t, handler.ShouldHandleEvent("american robin", 60*time.Second))
}

func TestEventHandler_ResetEvent_NonexistentSpecies(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(60*time.Second, StandardEventBehavior)

	// Reset a species that was never tracked (should not panic)
	assert.NotPanics(t, func() {
		handler.ResetEvent("Unknown Species")
	})

	// Handler still works normally
	assert.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
}

func TestEventHandler_ZeroTimeout(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(0, StandardEventBehavior)

	// With zero timeout, events should always be allowed
	assert.True(t, handler.ShouldHandleEvent("American Robin", 0))
	assert.True(t, handler.ShouldHandleEvent("American Robin", 0))
	assert.True(t, handler.ShouldHandleEvent("American Robin", 0))
}

func TestEventHandler_CustomBehaviorFunc(t *testing.T) {
	t.Parallel()

	// Custom behavior that always blocks (after first event)
	alwaysBlock := func(lastEventTime time.Time, timeout time.Duration) bool {
		return false
	}

	handler := NewEventHandler(60*time.Second, alwaysBlock)

	// First event for each species allowed (no previous event, so !exists branch)
	assert.True(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
	assert.True(t, handler.ShouldHandleEvent("House Sparrow", 60*time.Second))

	// Subsequent events for same species blocked by custom behavior
	assert.False(t, handler.ShouldHandleEvent("American Robin", 60*time.Second))
	assert.False(t, handler.ShouldHandleEvent("House Sparrow", 60*time.Second))
}

// =============================================================================
// EventTracker Tests - Basic Functionality
// =============================================================================

func TestNewEventTracker(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	require.NotNil(t, tracker)
	assert.Equal(t, 60*time.Second, tracker.DefaultInterval)
	assert.NotNil(t, tracker.Handlers)
	assert.NotNil(t, tracker.SpeciesConfigs)

	// All event types should have handlers
	eventTypes := []EventType{
		DatabaseSave, LogToFile, SendNotification,
		BirdWeatherSubmit, MQTTPublish, SSEBroadcast,
	}
	for _, et := range eventTypes {
		assert.Contains(t, tracker.Handlers, et, "missing handler for event type %d", et)
	}
}

func TestEventTracker_TrackEvent_BasicRateLimiting(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// First event allowed
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Immediate second event blocked
	assert.False(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTracker_TrackEvent_EventTypeIsolation(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Track for DatabaseSave
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Same species, different event type should be allowed
	assert.True(t, tracker.TrackEvent("American Robin", LogToFile))
	assert.True(t, tracker.TrackEvent("American Robin", SendNotification))
	assert.True(t, tracker.TrackEvent("American Robin", MQTTPublish))
	assert.True(t, tracker.TrackEvent("American Robin", SSEBroadcast))
	assert.True(t, tracker.TrackEvent("American Robin", BirdWeatherSubmit))

	// Original event type still blocked
	assert.False(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTracker_TrackEvent_UnknownEventType(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Unknown event type (value 999) should return false
	unknownType := EventType(999)
	assert.False(t, tracker.TrackEvent("American Robin", unknownType))
}

func TestEventTracker_ResetEvent(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Track and verify blocked
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
	require.False(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Reset
	tracker.ResetEvent("American Robin", DatabaseSave)

	// Should be allowed again
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTracker_ResetEvent_OnlyAffectsSpecificEventType(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Track for multiple event types
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
	require.True(t, tracker.TrackEvent("American Robin", LogToFile))

	// Reset only DatabaseSave
	tracker.ResetEvent("American Robin", DatabaseSave)

	// DatabaseSave should be allowed
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// LogToFile should still be blocked
	assert.False(t, tracker.TrackEvent("American Robin", LogToFile))
}

func TestEventTracker_ResetEvent_UnknownEventType(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Reset for unknown event type should not panic
	assert.NotPanics(t, func() {
		tracker.ResetEvent("American Robin", EventType(999))
	})
}

// =============================================================================
// EventTracker Tests - Species Config
// =============================================================================

func TestEventTrackerWithConfig_SpeciesSpecificInterval(t *testing.T) {
	t.Parallel()

	// Configure specific interval for one species
	speciesConfigs := map[string]conf.SpeciesConfig{
		"american robin": {Interval: 1}, // 1 second
	}
	tracker := NewEventTrackerWithConfig(60*time.Second, speciesConfigs)

	// American Robin uses 1 second interval
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
	require.False(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Wait for species-specific interval
	time.Sleep(1100 * time.Millisecond)
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTrackerWithConfig_ConfigNormalization(t *testing.T) {
	t.Parallel()

	// Config with mixed case key
	speciesConfigs := map[string]conf.SpeciesConfig{
		"American Robin": {Interval: 1}, // Mixed case in config
	}
	tracker := NewEventTrackerWithConfig(60*time.Second, speciesConfigs)

	// Should match regardless of case
	require.True(t, tracker.TrackEvent("american robin", DatabaseSave))
	require.False(t, tracker.TrackEvent("american robin", DatabaseSave))

	time.Sleep(1100 * time.Millisecond)
	assert.True(t, tracker.TrackEvent("AMERICAN ROBIN", DatabaseSave))
}

func TestEventTrackerWithConfig_IntervalZeroUsesDefault(t *testing.T) {
	t.Parallel()

	speciesConfigs := map[string]conf.SpeciesConfig{
		"american robin": {Interval: 0}, // Zero interval
	}
	// Use short default so test runs quickly
	tracker := NewEventTrackerWithConfig(1*time.Second, speciesConfigs)

	// Should use default interval (1 second), not zero
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
	require.False(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Should still be blocked after 500ms
	time.Sleep(500 * time.Millisecond)
	assert.False(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Should be allowed after full default interval
	time.Sleep(600 * time.Millisecond)
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTrackerWithConfig_NegativeIntervalUsesDefault(t *testing.T) {
	t.Parallel()

	speciesConfigs := map[string]conf.SpeciesConfig{
		"american robin": {Interval: -5}, // Negative interval
	}
	// Use short default so test runs quickly
	tracker := NewEventTrackerWithConfig(1*time.Second, speciesConfigs)

	// Should use default interval despite negative config
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
	require.False(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Should be allowed after default interval
	time.Sleep(1100 * time.Millisecond)
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTrackerWithConfig_NilSpeciesConfigs(t *testing.T) {
	t.Parallel()

	// nil configs should work (uses default interval for all)
	tracker := NewEventTrackerWithConfig(1*time.Second, nil)

	require.NotNil(t, tracker)
	require.NotNil(t, tracker.SpeciesConfigs) // Should be initialized to empty map

	// Should work normally with default interval
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
	assert.False(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

// =============================================================================
// EventTracker Tests - TrackEventWithNames
// =============================================================================

func TestTrackEventWithNames_CommonNamePriority(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Track with common name
	require.True(t, tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave))

	// Same common name, different scientific name should be blocked (common name is tracking key)
	assert.False(t, tracker.TrackEventWithNames("American Robin", "Different Scientific", DatabaseSave))
}

func TestTrackEventWithNames_ScientificNameFallback(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Track with empty common name, uses scientific name as key
	require.True(t, tracker.TrackEventWithNames("", "Turdus migratorius", DatabaseSave))

	// Same scientific name should be blocked
	assert.False(t, tracker.TrackEventWithNames("", "Turdus migratorius", DatabaseSave))

	// Different scientific name should be allowed
	assert.True(t, tracker.TrackEventWithNames("", "Passer domesticus", DatabaseSave))
}

func TestTrackEventWithNames_BothEmptyAlwaysAllows(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Both names empty should always allow (can't rate-limit without key)
	assert.True(t, tracker.TrackEventWithNames("", "", DatabaseSave))
	assert.True(t, tracker.TrackEventWithNames("", "", DatabaseSave))
	assert.True(t, tracker.TrackEventWithNames("", "", DatabaseSave))
}

func TestTrackEventWithNames_ConfigLookupByScientificName(t *testing.T) {
	t.Parallel()

	// Config keyed by scientific name
	speciesConfigs := map[string]conf.SpeciesConfig{
		"turdus migratorius": {Interval: 1}, // 1 second
	}
	tracker := NewEventTrackerWithConfig(60*time.Second, speciesConfigs)

	// Common name not in config, but scientific name is
	require.True(t, tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave))
	require.False(t, tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave))

	// Should use 1s interval from scientific name config
	time.Sleep(1100 * time.Millisecond)
	assert.True(t, tracker.TrackEventWithNames("American Robin", "Turdus migratorius", DatabaseSave))
}

// =============================================================================
// Concurrency Tests - Race Condition Detection
// =============================================================================

func TestEventHandler_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	handler := NewEventHandler(1*time.Millisecond, StandardEventBehavior)
	species := "American Robin"

	var wg sync.WaitGroup
	const goroutines = 100
	const iterations = 100

	// Track how many events were allowed
	var allowedCount atomic.Int64

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				if handler.ShouldHandleEvent(species, 1*time.Millisecond) {
					allowedCount.Add(1)
				}
			}
		})
	}

	wg.Wait()

	// At least some events should have been allowed
	assert.Positive(t, allowedCount.Load())
	// Not all events should be allowed (rate limiting working)
	assert.Less(t, allowedCount.Load(), int64(goroutines*iterations))
}

func TestEventTracker_ConcurrentTrackEvent(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(1 * time.Millisecond)

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 50

	eventTypes := []EventType{DatabaseSave, LogToFile, SendNotification, MQTTPublish}
	species := []string{"American Robin", "House Sparrow", "Blue Jay", "Cardinal"}

	var allowedCount atomic.Int64

	for range goroutines {
		wg.Go(func() {
			for j := range iterations {
				sp := species[j%len(species)]
				et := eventTypes[j%len(eventTypes)]
				if tracker.TrackEvent(sp, et) {
					allowedCount.Add(1)
				}
			}
		})
	}

	wg.Wait()

	// Should have processed without panic/deadlock
	assert.Positive(t, allowedCount.Load())
}

func TestEventTracker_ConcurrentTrackAndReset(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(1 * time.Millisecond)
	species := "American Robin"

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 100

	// Half goroutines track events, half reset events
	for i := range goroutines {
		if i%2 == 0 {
			wg.Go(func() {
				for range iterations {
					tracker.TrackEvent(species, DatabaseSave)
				}
			})
		} else {
			wg.Go(func() {
				for range iterations {
					tracker.ResetEvent(species, DatabaseSave)
				}
			})
		}
	}

	wg.Wait()
	// If we get here without deadlock or panic, test passes
}

func TestEventTracker_ConcurrentDifferentSpecies(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	var wg sync.WaitGroup
	const goroutines = 100

	// Each goroutine tracks a unique species
	results := make([]bool, goroutines)

	for i := range goroutines {
		wg.Go(func() {
			species := fmt.Sprintf("Species_%d", i) // Unique species per goroutine
			results[i] = tracker.TrackEvent(species, DatabaseSave)
		})
	}

	wg.Wait()

	// All first events for unique species should be allowed
	for i, result := range results {
		assert.True(t, result, "goroutine %d should have been allowed", i)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestEventTracker_EmptySpeciesName(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Empty species name with TrackEvent delegates to TrackEventWithNames
	// with empty scientific name, which always allows
	assert.True(t, tracker.TrackEvent("", DatabaseSave))
	assert.True(t, tracker.TrackEvent("", DatabaseSave))
}

func TestEventTracker_WhitespaceSpeciesName(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Whitespace-only names should be tracked (they're not empty after normalization)
	require.True(t, tracker.TrackEvent("   ", DatabaseSave))
	assert.False(t, tracker.TrackEvent("   ", DatabaseSave))
}

func TestEventTracker_UnicodeSpeciesNames(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Unicode species names should work
	require.True(t, tracker.TrackEvent("Pájaro Azul", DatabaseSave))
	assert.False(t, tracker.TrackEvent("Pájaro Azul", DatabaseSave))

	// Different unicode species should be independent
	assert.True(t, tracker.TrackEvent("北京雨燕", DatabaseSave))
}

func TestEventTracker_VeryLongSpeciesName(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	// Very long species name
	longName := strings.Repeat("A", 1000)

	require.True(t, tracker.TrackEvent(longName, DatabaseSave))
	assert.False(t, tracker.TrackEvent(longName, DatabaseSave))
}

func TestEventTracker_SpecialCharactersInName(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(60 * time.Second)

	specialNames := []string{
		"Species (Subspecies)",
		"Species/Variant",
		"Species-Type",
		"Species_underscore",
		"Species\twith\ttabs",
		"Species\nwith\nnewlines",
	}

	for _, name := range specialNames {
		require.True(t, tracker.TrackEvent(name, DatabaseSave), "first event for %q should be allowed", name)
		assert.False(t, tracker.TrackEvent(name, DatabaseSave), "second event for %q should be blocked", name)
	}
}

// =============================================================================
// Timeout Expiry Tests
// =============================================================================

func TestEventTracker_TimeoutExpiry(t *testing.T) {
	t.Parallel()

	// Use very short timeout for test speed
	tracker := NewEventTracker(100 * time.Millisecond)

	// First event allowed
	require.True(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Immediate second blocked
	require.False(t, tracker.TrackEvent("American Robin", DatabaseSave))

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	assert.True(t, tracker.TrackEvent("American Robin", DatabaseSave))
}

func TestEventTracker_MultipleTimeoutCycles(t *testing.T) {
	t.Parallel()

	tracker := NewEventTracker(50 * time.Millisecond)

	// Multiple cycles of allow -> block -> timeout -> allow
	for cycle := range 3 {
		require.True(t, tracker.TrackEvent("American Robin", DatabaseSave), "cycle %d: first event should be allowed", cycle)
		require.False(t, tracker.TrackEvent("American Robin", DatabaseSave), "cycle %d: second event should be blocked", cycle)
		time.Sleep(60 * time.Millisecond)
	}
}
