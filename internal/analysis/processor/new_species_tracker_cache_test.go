package processor

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestCleanupExpiredCacheComprehensive tests the cleanupExpiredCache method thoroughly
func TestCleanupExpiredCacheComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("cleanup with no cache", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		
		// Call cleanup with no cache entries
		now := time.Now()
		tracker.cleanupExpiredCache(now)
		
		// Should not panic and cache should remain empty
		assert.NotNil(t, tracker.statusCache)
		assert.Empty(t, tracker.statusCache)
	})

	t.Run("cleanup with expired entries", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.cacheTTL = 1 * time.Second // Short TTL for testing
		
		// Add some cache entries
		now := time.Now()
		tracker.statusCache["species1"] = cachedSpeciesStatus{
			status:    SpeciesStatus{IsNew: true},
			timestamp: now.Add(-2 * time.Second), // Expired
		}
		tracker.statusCache["species2"] = cachedSpeciesStatus{
			status:    SpeciesStatus{IsNew: false},
			timestamp: now, // Current
		}
		tracker.statusCache["species3"] = cachedSpeciesStatus{
			status:    SpeciesStatus{IsNew: true},
			timestamp: now.Add(-1 * time.Hour), // Very expired
		}
		
		// Run cleanup with force=true to bypass recent check
		tracker.cleanupExpiredCacheWithForce(now, true)
		
		// Only species2 should remain
		assert.Len(t, tracker.statusCache, 1)
		assert.Contains(t, tracker.statusCache, "species2")
		assert.NotContains(t, tracker.statusCache, "species1")
		assert.NotContains(t, tracker.statusCache, "species3")
	})

	t.Run("cleanup with LRU eviction", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.cacheTTL = 1 * time.Hour // Long TTL
		
		now := time.Now()
		// Add 1500 cache entries (exceeds maxCacheSize of 1000)
		for i := 0; i < 1500; i++ {
			species := fmt.Sprintf("species_%d", i)
			tracker.statusCache[species] = cachedSpeciesStatus{
				status:    SpeciesStatus{IsNew: i%2 == 0},
				timestamp: now.Add(time.Duration(-i) * time.Second), // Older entries have older timestamps
			}
		}
		
		// Run cleanup with force=true to bypass recent check
		tracker.cleanupExpiredCacheWithForce(now, true)
		
		// Should keep only 800 entries (80% of maxCacheSize)
		assert.LessOrEqual(t, len(tracker.statusCache), 800)
		
		// Verify that newer entries are kept
		// Species_0 to species_799 should be kept (newest)
		for i := 0; i < 800; i++ {
			species := fmt.Sprintf("species_%d", i)
			assert.Contains(t, tracker.statusCache, species, "Newer entry should be kept")
		}
		
		// Species_1000+ should be removed (oldest)
		for i := 1000; i < 1500; i++ {
			species := fmt.Sprintf("species_%d", i)
			assert.NotContains(t, tracker.statusCache, species, "Older entry should be removed")
		}
	})

	t.Run("cleanup updates lastCacheCleanup", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		
		// Set lastCacheCleanup to past
		oldCleanupTime := time.Now().Add(-1 * time.Hour)
		tracker.lastCacheCleanup = oldCleanupTime
		
		// Run cleanup with force=true to update lastCacheCleanup
		now := time.Now()
		tracker.cleanupExpiredCacheWithForce(now, true)
		
		// lastCacheCleanup should be updated
		assert.True(t, tracker.lastCacheCleanup.After(oldCleanupTime))
	})

	t.Run("cleanup skips if recently cleaned", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.cacheTTL = 1 * time.Second
		
		// Set lastCacheCleanup to very recent
		tracker.lastCacheCleanup = time.Now().Add(-1 * time.Second)
		
		// Add expired entry
		tracker.statusCache["expired"] = cachedSpeciesStatus{
			status:    SpeciesStatus{IsNew: true},
			timestamp: time.Now().Add(-2 * time.Hour),
		}
		
		// Run cleanup - should skip due to recent cleanup
		now := time.Now()
		tracker.cleanupExpiredCache(now)
		
		// Expired entry should still be there (cleanup was skipped)
		assert.Contains(t, tracker.statusCache, "expired")
	})
}

// TestCleanupOldNotificationRecordsLockedComprehensive tests the cleanupOldNotificationRecordsLocked method
func TestCleanupOldNotificationRecordsLockedComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("cleanup with no records", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		
		// Lock and cleanup
		now := time.Now()
		tracker.mu.Lock()
		tracker.cleanupOldNotificationRecordsLocked(now)
		tracker.mu.Unlock()
		
		// Should not panic and map should be empty
		assert.NotNil(t, tracker.notificationLastSent)
		assert.Empty(t, tracker.notificationLastSent)
	})

	t.Run("cleanup removes old records", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.notificationSuppressionWindow = 7 * 24 * time.Hour // 7 days
		
		now := time.Now()
		
		// Add notification records
		tracker.notificationLastSent = map[string]time.Time{
			"species1": now.Add(-8 * 24 * time.Hour),  // 8 days ago - should be removed
			"species2": now.Add(-6 * 24 * time.Hour),  // 6 days ago - should be kept
			"species3": now.Add(-30 * 24 * time.Hour), // 30 days ago - should be removed
			"species4": now.Add(-1 * time.Hour),       // 1 hour ago - should be kept
		}
		
		// Lock and cleanup
		tracker.mu.Lock()
		tracker.cleanupOldNotificationRecordsLocked(now)
		tracker.mu.Unlock()
		
		// Check results
		assert.Len(t, tracker.notificationLastSent, 2)
		assert.Contains(t, tracker.notificationLastSent, "species2")
		assert.Contains(t, tracker.notificationLastSent, "species4")
		assert.NotContains(t, tracker.notificationLastSent, "species1")
		assert.NotContains(t, tracker.notificationLastSent, "species3")
	})

	t.Run("cleanup with custom suppression window", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.notificationSuppressionWindow = 1 * time.Hour // 1 hour window
		
		now := time.Now()
		
		// Add notification records
		tracker.notificationLastSent = map[string]time.Time{
			"species1": now.Add(-2 * time.Hour),   // Should be removed
			"species2": now.Add(-30 * time.Minute), // Should be kept
			"species3": now.Add(-90 * time.Minute), // Should be removed
		}
		
		// Lock and cleanup
		tracker.mu.Lock()
		tracker.cleanupOldNotificationRecordsLocked(now)
		tracker.mu.Unlock()
		
		// Check results
		assert.Len(t, tracker.notificationLastSent, 1)
		assert.Contains(t, tracker.notificationLastSent, "species2")
		assert.NotContains(t, tracker.notificationLastSent, "species1")
		assert.NotContains(t, tracker.notificationLastSent, "species3")
	})

	t.Run("cleanup preserves all recent records", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.notificationSuppressionWindow = 24 * time.Hour
		
		now := time.Now()
		
		// Add all recent notification records
		tracker.notificationLastSent = map[string]time.Time{
			"species1": now.Add(-1 * time.Hour),
			"species2": now.Add(-2 * time.Hour),
			"species3": now.Add(-3 * time.Hour),
			"species4": now.Add(-4 * time.Hour),
		}
		
		originalCount := len(tracker.notificationLastSent)
		
		// Lock and cleanup
		tracker.mu.Lock()
		tracker.cleanupOldNotificationRecordsLocked(now)
		tracker.mu.Unlock()
		
		// All should be preserved
		assert.Len(t, tracker.notificationLastSent, originalCount)
	})
}

// TestCheckAndUpdateSpeciesComprehensive tests the CheckAndUpdateSpecies method comprehensively
func TestCheckAndUpdateSpeciesComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("concurrent updates", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		
		// Run concurrent updates
		done := make(chan bool, 10)
		now := time.Now()
		
		for i := 0; i < 10; i++ {
			go func(id int) {
				species := fmt.Sprintf("species_%d", id)
				isNew, days := tracker.CheckAndUpdateSpecies(species, now)
				assert.True(t, isNew)
				assert.Equal(t, 0, days)
				done <- true
			}(i)
		}
		
		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}
		
		// Verify all species were added
		tracker.mu.RLock()
		assert.Len(t, tracker.speciesFirstSeen, 10)
		tracker.mu.RUnlock()
	})

	t.Run("updates earlier detection", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		
		now := time.Now()
		
		// First detection
		isNew, days := tracker.CheckAndUpdateSpecies("Robin", now)
		assert.True(t, isNew)
		assert.Equal(t, 0, days)
		
		// Earlier detection should update
		earlier := now.Add(-5 * 24 * time.Hour)
		isNew, days = tracker.CheckAndUpdateSpecies("Robin", earlier)
		assert.True(t, isNew) // Still within window
		assert.Equal(t, 0, days) // Days from the earlier time
		
		// Verify the earlier time was stored
		tracker.mu.RLock()
		storedTime := tracker.speciesFirstSeen["Robin"]
		tracker.mu.RUnlock()
		assert.Equal(t, earlier.Unix(), storedTime.Unix())
	})
}

// TestCacheManagement tests various cache management scenarios
func TestCacheManagement(t *testing.T) {
	t.Parallel()

	t.Run("cache hit improves performance", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.cacheTTL = 5 * time.Minute
		
		// First call - cache miss
		now := time.Now()
		status1 := tracker.GetSpeciesStatus("Robin", now)
		
		// Second call - should hit cache
		status2 := tracker.GetSpeciesStatus("Robin", now)
		
		// Results should be identical
		assert.Equal(t, status1.IsNew, status2.IsNew)
		assert.Equal(t, status1.DaysSinceFirst, status2.DaysSinceFirst)
		
		// Cache should contain the entry
		tracker.mu.RLock()
		cached, exists := tracker.statusCache["Robin"]
		tracker.mu.RUnlock()
		assert.True(t, exists)
		assert.Equal(t, status1.IsNew, cached.status.IsNew)
	})

	t.Run("cache invalidation on update", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
		}
		
		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		tracker.cacheTTL = 5 * time.Minute
		
		now := time.Now()
		
		// Get status - creates cache entry
		_ = tracker.GetSpeciesStatus("Robin", now)
		
		// Update species - should invalidate cache
		tracker.UpdateSpecies("Robin", now.Add(1*time.Hour))
		
		// Cache should be cleared for this species
		tracker.mu.RLock()
		_, exists := tracker.statusCache["Robin"]
		tracker.mu.RUnlock()
		assert.False(t, exists, "Cache should be invalidated after update")
	})
}

