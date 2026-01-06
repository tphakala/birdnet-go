// new_species_tracker_cleanup_test.go
// Critical tests for data cleanup and notification suppression functions
package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestPruneOldEntries_CriticalReliability tests old entry pruning to prevent memory leaks
// CRITICAL: Without proper pruning, tracker will consume unbounded memory over time
//
//nolint:gocognit // Table-driven test with comprehensive scenario coverage requires complex verification
func TestPruneOldEntries_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		setupData      func(*SpeciesTracker, time.Time)
		currentTime    time.Time
		expectedPruned int
		description    string
	}{
		{
			"no_old_entries",
			func(tracker *SpeciesTracker, now time.Time) {
				// Add recent entries only
				tracker.speciesFirstSeen["Recent_Species_1"] = now.AddDate(0, 0, -5)
				tracker.speciesFirstSeen["Recent_Species_2"] = now.AddDate(0, 0, -10)
			},
			time.Now(),
			0,
			"Recent entries should not be pruned",
		},
		{
			"prune_lifetime_old_entries",
			func(tracker *SpeciesTracker, now time.Time) {
				// Lifetime entries are pruned after 10 years, NOT 2x window
				tracker.speciesFirstSeen["Very_Old_Species"] = now.AddDate(-11, 0, 0) // 11 years ago - should be pruned
				tracker.speciesFirstSeen["Old_Species_1"] = now.AddDate(0, 0, -30)    // 30 days ago - should NOT be pruned
				tracker.speciesFirstSeen["Old_Species_2"] = now.AddDate(0, 0, -40)    // 40 days ago - should NOT be pruned
				tracker.speciesFirstSeen["Recent_Species"] = now.AddDate(0, 0, -20)   // 20 days ago - should NOT be pruned
			},
			time.Now(),
			1,
			"Only entries older than 10 years should be pruned from lifetime tracking",
		},
		{
			"prune_yearly_tracking",
			func(tracker *SpeciesTracker, now time.Time) {
				// Yearly tracking prunes entries from before the current tracking year
				tracker.yearlyEnabled = true
				tracker.yearlyWindowDays = 7
				tracker.resetMonth = 1 // January reset
				tracker.resetDay = 1
				// Calculate year start (Jan 1 of current year) for proper entry placement
				yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
				// Add entry from before year start (should be pruned) and after year start (should NOT be pruned)
				tracker.speciesThisYear["Last_Year_Species"] = yearStart.AddDate(0, 0, -1) // Day before year start - should be pruned
				tracker.speciesThisYear["This_Year_Species"] = yearStart.AddDate(0, 0, 1)  // Day after year start - should NOT be pruned
			},
			time.Now(),
			1,
			"Only entries from before the current tracking year should be pruned",
		},
		{
			"prune_seasonal_tracking",
			func(tracker *SpeciesTracker, now time.Time) {
				// Seasonal tracking prunes entire seasons older than 1 year
				tracker.seasonalEnabled = true
				tracker.seasonalWindowDays = 7
				// Old season - all entries older than 1 year
				tracker.speciesBySeason["old_spring"] = map[string]time.Time{
					"Very_Old_Spring_1": now.AddDate(-2, 0, 0), // 2 years ago
					"Very_Old_Spring_2": now.AddDate(-2, 0, 0), // 2 years ago
				}
				// Recent season - has recent entries
				tracker.speciesBySeason["current_spring"] = map[string]time.Time{
					"Recent_Spring_Species": now.AddDate(0, 0, -5), // 5 days ago
				}
			},
			time.Now(),
			2,
			"Entire seasons older than 1 year should be pruned",
		},
		{
			"prune_notification_records",
			func(tracker *SpeciesTracker, now time.Time) {
				// Notification window is 168 hours (7 days)
				tracker.notificationSuppressionWindow = 168 * time.Hour
				tracker.notificationLastSent["Old_Notif_Species"] = now.Add(-170 * time.Hour)
				tracker.notificationLastSent["Recent_Notif_Species"] = now.Add(-100 * time.Hour)
			},
			time.Now(),
			1,
			"Old notification records should be pruned",
		},
		{
			"empty_season_map_removal",
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.seasonalEnabled = true
				tracker.seasonalWindowDays = 7
				// Create season with all old entries (older than 1 year) that will be pruned
				tracker.speciesBySeason["old_winter"] = map[string]time.Time{
					"Very_Old_Winter_1": now.AddDate(-2, 0, 0), // 2 years ago
					"Very_Old_Winter_2": now.AddDate(-2, 0, 0), // 2 years ago
				}
				tracker.speciesBySeason["recent_spring"] = map[string]time.Time{
					"Recent_Spring": now.AddDate(0, 0, -5), // 5 days ago
				}
			},
			time.Now(),
			2,
			"Old season maps should be removed entirely after pruning",
		},
		{
			"boundary_conditions",
			func(tracker *SpeciesTracker, now time.Time) {
				// Test exact boundary (10 years for lifetime tracking)
				// PruneOldEntries uses Before() which means entries at exactly 10 years ARE pruned
				tracker.speciesFirstSeen["Exactly_At_Boundary"] = now.AddDate(-10, 0, 0)  // Exactly 10 years - will be pruned
				tracker.speciesFirstSeen["Just_Before_Boundary"] = now.AddDate(-10, 0, 1) // Just under 10 years - not pruned
				tracker.speciesFirstSeen["Just_After_Boundary"] = now.AddDate(-10, 0, -1) // Just over 10 years - will be pruned
			},
			time.Now(),
			2, // Both exactly 10 years and over should be pruned (Before() includes the boundary)
			"Boundary conditions should be handled correctly (10 year cutoff)",
		},
		{
			"massive_dataset_pruning",
			func(tracker *SpeciesTracker, now time.Time) {
				// Add 1000 very old entries (> 10 years) and 100 recent ones
				for i := range 1000 {
					speciesName := "Very_Old_Species_" + string(rune(i))
					tracker.speciesFirstSeen[speciesName] = now.AddDate(-11, 0, -i) // 11+ years ago
				}
				for i := range 100 {
					speciesName := "Recent_Species_" + string(rune(i))
					tracker.speciesFirstSeen[speciesName] = now.AddDate(0, 0, -10) // 10 days ago
				}
			},
			time.Now(),
			1000,
			"Large datasets should be pruned efficiently (10+ year old entries)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker with standard settings
			settings := &conf.SpeciesTrackingSettings{
				Enabled:                      true,
				NewSpeciesWindowDays:         14,
				NotificationSuppressionHours: 168, // 7 days
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup test data
			tt.setupData(tracker, tt.currentTime)

			// Count entries before pruning
			beforeCount := len(tracker.speciesFirstSeen)
			if tracker.yearlyEnabled {
				beforeCount += len(tracker.speciesThisYear)
			}
			if tracker.seasonalEnabled {
				for _, seasonMap := range tracker.speciesBySeason {
					beforeCount += len(seasonMap)
				}
			}
			beforeCount += len(tracker.notificationLastSent)

			// Perform pruning
			pruned := tracker.PruneOldEntries()

			// Verify pruning count
			assert.Equal(t, tt.expectedPruned, pruned,
				"Incorrect number of entries pruned")

			// Count entries after pruning
			afterCount := len(tracker.speciesFirstSeen)
			if tracker.yearlyEnabled {
				afterCount += len(tracker.speciesThisYear)
			}
			if tracker.seasonalEnabled {
				for _, seasonMap := range tracker.speciesBySeason {
					afterCount += len(seasonMap)
				}
			}
			afterCount += len(tracker.notificationLastSent)

			// Verify count reduction
			assert.Equal(t, beforeCount-tt.expectedPruned, afterCount,
				"Entry count mismatch after pruning")

			// Verify empty season maps are removed
			if tt.name == "empty_season_map_removal" {
				_, exists := tracker.speciesBySeason["old_winter"]
				assert.False(t, exists, "Old winter season map should be removed")
				_, exists = tracker.speciesBySeason["recent_spring"]
				assert.True(t, exists, "Recent spring season map with entries should remain")
			}

			t.Logf("✓ Successfully pruned %d entries", pruned)
		})
	}
}

// TestNotificationSuppression_CriticalReliability tests notification suppression system
// CRITICAL: Prevents notification spam which can overwhelm users and systems
func TestNotificationSuppression_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		species          string
		lastSentTime     *time.Time
		currentTime      time.Time
		suppressionHours int
		expectSuppress   bool
		description      string
	}{
		{
			"never_sent_before",
			"New_Species",
			nil,
			time.Now(),
			168, // 7 days
			false,
			"Never sent notifications should not be suppressed",
		},
		{
			"within_suppression_window",
			"Recent_Species",
			func() *time.Time { t := time.Now().Add(-3 * 24 * time.Hour); return &t }(), // 3 days ago
			time.Now(),
			168, // 7 days
			true,
			"Notifications within suppression window should be suppressed",
		},
		{
			"outside_suppression_window",
			"Old_Species",
			func() *time.Time { t := time.Now().Add(-8 * 24 * time.Hour); return &t }(), // 8 days ago
			time.Now(),
			168, // 7 days
			false,
			"Notifications outside suppression window should not be suppressed",
		},
		{
			"exactly_at_window_boundary",
			"Boundary_Species",
			func() *time.Time { t := time.Now().Add(-168 * time.Hour); return &t }(), // Exactly 7 days
			time.Now(),
			168,
			false,
			"Notifications exactly at window boundary should not be suppressed",
		},
		{
			"suppression_disabled",
			"Any_Species",
			func() *time.Time { t := time.Now().Add(-1 * time.Hour); return &t }(), // 1 hour ago
			time.Now(),
			0, // Disabled
			false,
			"Suppression disabled should never suppress",
		},
		{
			"very_short_window",
			"Quick_Species",
			func() *time.Time { t := time.Now().Add(-30 * time.Minute); return &t }(), // 30 minutes ago
			time.Now(),
			1, // 1 hour window
			true,
			"Short suppression window should work correctly",
		},
		{
			"future_time_handling",
			"Future_Species",
			func() *time.Time { t := time.Now().Add(1 * time.Hour); return &t }(), // 1 hour in future
			time.Now(),
			168,
			true,
			"Future timestamps should be handled gracefully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker with specific suppression settings
			settings := &conf.SpeciesTrackingSettings{
				Enabled:                      true,
				NotificationSuppressionHours: tt.suppressionHours,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup last sent time if provided
			if tt.lastSentTime != nil {
				tracker.notificationLastSent[tt.species] = *tt.lastSentTime
			}

			// Test suppression
			shouldSuppress := tracker.ShouldSuppressNotification(tt.species, tt.currentTime)

			assert.Equal(t, tt.expectSuppress, shouldSuppress,
				"Suppression mismatch for %s", tt.species)

			t.Logf("✓ Correctly determined suppression: %v", shouldSuppress)
		})
	}
}

// TestRecordNotificationSent_CriticalReliability tests notification recording
// CRITICAL: Recording must be thread-safe and accurate for suppression to work
func TestRecordNotificationSent_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker
	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NotificationSuppressionHours: 168,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Test 1: Record single notification
	species1 := "Test_Species_1"
	sentTime1 := time.Now()
	tracker.RecordNotificationSent(species1, sentTime1)

	recorded, exists := tracker.notificationLastSent[species1]
	assert.True(t, exists, "Notification should be recorded")
	assert.Equal(t, sentTime1, recorded, "Recorded time should match")

	// Test 2: Update existing notification
	sentTime2 := sentTime1.Add(1 * time.Hour)
	tracker.RecordNotificationSent(species1, sentTime2)

	recorded, exists = tracker.notificationLastSent[species1]
	assert.True(t, exists, "Updated notification should exist")
	assert.Equal(t, sentTime2, recorded, "Should update to newer time")

	// Test 3: Multiple species
	species2 := "Test_Species_2"
	species3 := "Test_Species_3"
	tracker.RecordNotificationSent(species2, sentTime1)
	tracker.RecordNotificationSent(species3, sentTime2)

	assert.Len(t, tracker.notificationLastSent, 3, "Should track multiple species")

	// Test 4: Suppression disabled
	tracker.notificationSuppressionWindow = 0
	species4 := "Test_Species_4"
	tracker.RecordNotificationSent(species4, sentTime1)
	// When suppression is disabled, recording should be a no-op
	_, exists = tracker.notificationLastSent[species4]
	assert.False(t, exists, "Should not record when suppression disabled")

	t.Logf("✓ Notification recording works correctly")
}

// TestCleanupOldNotificationRecords_CriticalReliability tests notification record cleanup
// CRITICAL: Prevents unbounded memory growth from notification tracking
func TestCleanupOldNotificationRecords_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		setupRecords    func(*SpeciesTracker, time.Time)
		currentTime     time.Time
		windowHours     int
		expectedCleaned int
		description     string
	}{
		{
			"no_old_records",
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.notificationLastSent["Recent_1"] = now.Add(-1 * time.Hour)
				tracker.notificationLastSent["Recent_2"] = now.Add(-6 * time.Hour)
			},
			time.Now(),
			168, // 7 days
			0,
			"Recent records should not be cleaned",
		},
		{
			"clean_old_records",
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.notificationLastSent["Old_1"] = now.Add(-170 * time.Hour) // Just past window
				tracker.notificationLastSent["Old_2"] = now.Add(-200 * time.Hour)
				tracker.notificationLastSent["Recent"] = now.Add(-100 * time.Hour)
			},
			time.Now(),
			168,
			2,
			"Records older than suppression window should be cleaned",
		},
		{
			"exactly_at_cutoff",
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.notificationLastSent["Exactly"] = now.Add(-168 * time.Hour)    // Exactly at window - NOT cleaned
				tracker.notificationLastSent["JustBefore"] = now.Add(-167 * time.Hour) // Within window - NOT cleaned
				tracker.notificationLastSent["JustAfter"] = now.Add(-169 * time.Hour)  // Past window - WILL be cleaned
			},
			time.Now(),
			168,
			1, // Only entries strictly before cutoff (older than 168 hours) are cleaned
			"Boundary conditions: Before() means strictly before, not at boundary",
		},
		{
			"suppression_disabled",
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.notificationLastSent["Any_1"] = now.Add(-1000 * time.Hour)
				tracker.notificationLastSent["Any_2"] = now.Add(-2000 * time.Hour)
			},
			time.Now(),
			0, // Disabled
			0,
			"Cleanup should not run when suppression disabled",
		},
		{
			"large_dataset_cleanup",
			func(tracker *SpeciesTracker, now time.Time) {
				// Add 500 old records and 100 recent ones
				for i := range 500 {
					species := "Old_" + string(rune(i))
					tracker.notificationLastSent[species] = now.Add(-time.Duration(200+i) * time.Hour)
				}
				for i := range 100 {
					species := "Recent_" + string(rune(i))
					tracker.notificationLastSent[species] = now.Add(-time.Duration(10+i) * time.Hour)
				}
			},
			time.Now(),
			168,
			500,
			"Large datasets should be cleaned efficiently",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled:                      true,
				NotificationSuppressionHours: tt.windowHours,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup test records
			tt.setupRecords(tracker, tt.currentTime)

			// Count before cleanup
			beforeCount := len(tracker.notificationLastSent)

			// Perform cleanup
			cleaned := tracker.CleanupOldNotificationRecords(tt.currentTime)

			// Verify cleanup count
			assert.Equal(t, tt.expectedCleaned, cleaned,
				"Incorrect number of records cleaned")

			// Count after cleanup
			afterCount := len(tracker.notificationLastSent)

			// Verify count reduction
			assert.Equal(t, beforeCount-tt.expectedCleaned, afterCount,
				"Record count mismatch after cleanup")

			t.Logf("✓ Successfully cleaned %d notification records", cleaned)
		})
	}
}

// TestNotificationSystem_ConcurrentAccess tests thread safety of notification system
// CRITICAL: Concurrent notification handling must not cause race conditions
func TestNotificationSystem_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NotificationSuppressionHours: 168,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Run concurrent operations
	done := make(chan bool, 300)
	now := time.Now()

	// 100 goroutines recording notifications
	for i := range 100 {
		go func(id int) {
			species := "Species_" + string(rune(id%20)) // 20 different species
			sentTime := now.Add(-time.Duration(id) * time.Hour)
			tracker.RecordNotificationSent(species, sentTime)
			done <- true
		}(i)
	}

	// 100 goroutines checking suppression
	for i := range 100 {
		go func(id int) {
			species := "Species_" + string(rune(id%20))
			_ = tracker.ShouldSuppressNotification(species, now)
			done <- true
		}(i)
	}

	// 100 goroutines performing cleanup
	for i := range 100 {
		go func(id int) {
			cleanupTime := now.Add(time.Duration(id) * time.Minute)
			_ = tracker.CleanupOldNotificationRecords(cleanupTime)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 300 {
		<-done
	}

	// Verify system is still functional
	testSpecies := "Final_Test_Species"
	testTime := time.Now()
	tracker.RecordNotificationSent(testSpecies, testTime)
	shouldSuppress := tracker.ShouldSuppressNotification(testSpecies, testTime.Add(1*time.Hour))
	assert.True(t, shouldSuppress, "System should remain functional after concurrent access")

	t.Logf("✓ Concurrent notification operations completed without race conditions")
}
