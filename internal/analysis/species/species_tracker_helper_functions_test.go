// species_tracker_helper_functions_test.go - Tests for refactored helper functions
//
// This file contains targeted tests for low-coverage helper functions introduced during
// the species tracker refactoring. Tests prioritize bug discovery through edge cases
// and boundary conditions.

package species

import (
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// ============================================================================
// Test: checkAndUpdateLifetimeLocked Edge Cases
// Target Coverage: update.go:111, especially lines 126-134 (negative days anomaly)
// ============================================================================

func TestCheckAndUpdateLifetimeLocked_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		windowDays            int
		existingFirstSeen     *time.Time // nil means species doesn't exist
		detectionTime         time.Time
		expectedIsNew         bool
		expectedDaysSince     int
		expectedFirstSeenTime time.Time // expected final value in speciesFirstSeen
	}{
		{
			name:              "new_species_not_in_tracker",
			windowDays:        7,
			existingFirstSeen: nil,
			detectionTime:     time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedIsNew:     true,
			expectedDaysSince: 0,
		},
		{
			name:                  "detection_before_first_seen_updates_first_seen",
			windowDays:            7,
			existingFirstSeen:     new(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)),
			detectionTime:         time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC), // 5 days before
			expectedIsNew:         true,
			expectedDaysSince:     0,
			expectedFirstSeenTime: time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC),
		},
		{
			name:                  "detection_before_first_seen_by_nanoseconds",
			windowDays:            7,
			existingFirstSeen:     new(time.Date(2025, 6, 15, 10, 0, 0, 100, time.UTC)),
			detectionTime:         time.Date(2025, 6, 15, 10, 0, 0, 50, time.UTC), // 50 nanoseconds before
			expectedIsNew:         true,
			expectedDaysSince:     0,
			expectedFirstSeenTime: time.Date(2025, 6, 15, 10, 0, 0, 50, time.UTC),
		},
		{
			name:                  "exact_boundary_days_equals_window",
			windowDays:            7,
			existingFirstSeen:     new(time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)),
			detectionTime:         time.Date(2025, 6, 8, 10, 0, 0, 0, time.UTC), // exactly 7 days
			expectedIsNew:         true,                                         // daysSince == windowDays is still "new"
			expectedDaysSince:     7,
			expectedFirstSeenTime: time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name:                  "one_day_past_boundary",
			windowDays:            7,
			existingFirstSeen:     new(time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)),
			detectionTime:         time.Date(2025, 6, 9, 10, 0, 0, 0, time.UTC), // 8 days
			expectedIsNew:         false,
			expectedDaysSince:     8,
			expectedFirstSeenTime: time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name:                  "same_time_detection",
			windowDays:            7,
			existingFirstSeen:     new(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)),
			detectionTime:         time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC), // exact same time
			expectedIsNew:         true,
			expectedDaysSince:     0,
			expectedFirstSeenTime: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		},
		{
			name:                  "very_old_first_seen_large_days",
			windowDays:            7,
			existingFirstSeen:     new(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
			detectionTime:         time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedIsNew:         false,
			expectedDaysSince:     1992, // ~5.5 years
			expectedFirstSeenTime: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:                  "zero_window_days_always_not_new",
			windowDays:            0,
			existingFirstSeen:     new(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)),
			detectionTime:         time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedIsNew:         true, // daysSince 0 <= windowDays 0
			expectedDaysSince:     0,
			expectedFirstSeenTime: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := createLifetimeTracker(tt.windowDays, tt.existingFirstSeen)
			isNew, daysSince := tracker.checkAndUpdateLifetimeLocked("TestSpecies", tt.detectionTime)

			assert.Equal(t, tt.expectedIsNew, isNew, "isNew mismatch for test: %s", tt.name)
			assert.Equal(t, tt.expectedDaysSince, daysSince, "daysSince mismatch for test: %s", tt.name)
			assertFirstSeenUpdated(t, tracker, tt.existingFirstSeen, tt.detectionTime, tt.expectedFirstSeenTime, isNew)
		})
	}
}

// createLifetimeTracker creates a tracker for lifetime tracking tests.
func createLifetimeTracker(windowDays int, existingFirstSeen *time.Time) *SpeciesTracker {
	tracker := &SpeciesTracker{
		windowDays:       windowDays,
		speciesFirstSeen: make(map[string]time.Time),
	}
	if existingFirstSeen != nil {
		tracker.speciesFirstSeen["TestSpecies"] = *existingFirstSeen
	}
	return tracker
}

// assertFirstSeenUpdated verifies the first seen time was updated correctly.
func assertFirstSeenUpdated(t *testing.T, tracker *SpeciesTracker, existingFirstSeen *time.Time, detectionTime, expectedFirstSeenTime time.Time, isNew bool) {
	t.Helper()
	if existingFirstSeen == nil && !isNew {
		return // Nothing to verify
	}
	actualFirstSeen, exists := tracker.speciesFirstSeen["TestSpecies"]
	assert.True(t, exists, "species should exist in tracker after update")
	if existingFirstSeen == nil {
		assert.Equal(t, detectionTime, actualFirstSeen) // New species uses detection time
	} else {
		assert.Equal(t, expectedFirstSeenTime, actualFirstSeen)
	}
}

// ============================================================================
// Test: loadNotificationHistoryFromDatabase Edge Cases
// Target Coverage: database.go:294, especially type filtering and error paths
// ============================================================================

func TestLoadNotificationHistoryFromDatabase_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                    string
		suppressionWindow       time.Duration
		mockHistories           []datastore.NotificationHistory
		mockError               error
		expectedMapSize         int
		expectedSpecies         map[string]time.Time // expected species -> lastSent
		expectError             bool
		skipDatabaseCall        bool // if true, database should not be called
		notificationLastSentNil bool // if true, set notificationLastSent to nil before call
	}{
		{
			name:              "suppression_disabled_no_db_call",
			suppressionWindow: 0,
			mockHistories:     nil,
			mockError:         nil,
			expectedMapSize:   0,
			expectError:       false,
			skipDatabaseCall:  true,
		},
		{
			name:              "empty_history_from_database",
			suppressionWindow: 24 * time.Hour,
			mockHistories:     []datastore.NotificationHistory{},
			mockError:         nil,
			expectedMapSize:   0,
			expectError:       false,
		},
		{
			name:              "filters_wrong_notification_types",
			suppressionWindow: 24 * time.Hour,
			mockHistories: []datastore.NotificationHistory{
				{ScientificName: "Species A", NotificationType: "new_species", LastSent: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)},
				{ScientificName: "Species B", NotificationType: "yearly", LastSent: time.Date(2025, 6, 15, 11, 0, 0, 0, time.UTC)},   // should be filtered
				{ScientificName: "Species C", NotificationType: "seasonal", LastSent: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)}, // should be filtered
				{ScientificName: "Species D", NotificationType: "new_species", LastSent: time.Date(2025, 6, 15, 13, 0, 0, 0, time.UTC)},
			},
			mockError:       nil,
			expectedMapSize: 2, // Only "new_species" types
			expectedSpecies: map[string]time.Time{
				"Species A": time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				"Species D": time.Date(2025, 6, 15, 13, 0, 0, 0, time.UTC),
			},
			expectError: false,
		},
		{
			name:              "database_error_returns_error",
			suppressionWindow: 24 * time.Hour,
			mockHistories:     nil,
			mockError:         errors.New("database connection failed"),
			expectedMapSize:   0,
			expectError:       true,
		},
		{
			name:              "multiple_entries_same_species_last_wins",
			suppressionWindow: 24 * time.Hour,
			mockHistories: []datastore.NotificationHistory{
				{ScientificName: "Species A", NotificationType: "new_species", LastSent: time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC)},
				{ScientificName: "Species A", NotificationType: "new_species", LastSent: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)}, // more recent
			},
			mockError:       nil,
			expectedMapSize: 1,
			expectedSpecies: map[string]time.Time{
				"Species A": time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC), // last entry wins
			},
			expectError: false,
		},
		{
			name:                    "initializes_map_when_nil",
			suppressionWindow:       24 * time.Hour,
			notificationLastSentNil: true,
			mockHistories: []datastore.NotificationHistory{
				{ScientificName: "Species A", NotificationType: "new_species", LastSent: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)},
			},
			mockError:       nil,
			expectedMapSize: 1,
			expectedSpecies: map[string]time.Time{
				"Species A": time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDS := mocks.NewMockInterface(t)
			setupNotificationMock(mockDS, tt.skipDatabaseCall, tt.mockHistories, tt.mockError)
			tracker := createNotificationTracker(mockDS, tt.suppressionWindow, tt.notificationLastSentNil)

			now := time.Date(2025, 6, 20, 10, 0, 0, 0, time.UTC)
			err := tracker.loadNotificationHistoryFromDatabase(now)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assertNotificationHistoryLoaded(t, tracker, tt.expectedMapSize, tt.expectedSpecies)
		})
	}
}

// setupNotificationMock configures mock expectations for notification history tests.
func setupNotificationMock(mockDS *mocks.MockInterface, skipCall bool, histories []datastore.NotificationHistory, mockErr error) {
	if skipCall {
		return
	}
	mockDS.EXPECT().
		GetActiveNotificationHistory(mock.AnythingOfType("time.Time")).
		Return(histories, mockErr).
		Once()
}

// createNotificationTracker creates a tracker for notification history tests.
func createNotificationTracker(mockDS *mocks.MockInterface, window time.Duration, nilMap bool) *SpeciesTracker {
	tracker := &SpeciesTracker{
		ds:                            mockDS,
		notificationSuppressionWindow: window,
	}
	if !nilMap {
		tracker.notificationLastSent = make(map[string]time.Time)
	}
	return tracker
}

// assertNotificationHistoryLoaded verifies the notification history was loaded correctly.
func assertNotificationHistoryLoaded(t *testing.T, tracker *SpeciesTracker, expectedSize int, expectedSpecies map[string]time.Time) {
	t.Helper()
	if expectedSize > 0 {
		assert.Len(t, tracker.notificationLastSent, expectedSize)
	}
	for species, expectedTime := range expectedSpecies {
		actualTime, exists := tracker.notificationLastSent[species]
		assert.True(t, exists, "species %s should exist in map", species)
		assert.Equal(t, expectedTime, actualTime, "wrong lastSent time for species %s", species)
	}
}

// ============================================================================
// Test: pruneYearlyEntriesLocked with Custom Reset Dates
// Target Coverage: maintenance.go:90, especially lines 95-98 (before currentYearStart)
// ============================================================================

func TestPruneYearlyEntriesLocked_CustomResetDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		resetMonth      int
		resetDay        int
		yearlyEnabled   bool
		currentTime     time.Time
		existingEntries map[string]time.Time
		expectedPruned  int
		expectedRemain  []string // species that should remain
	}{
		{
			name:          "yearly_disabled_returns_zero",
			resetMonth:    7,
			resetDay:      1,
			yearlyEnabled: false,
			currentTime:   time.Date(2025, 8, 15, 10, 0, 0, 0, time.UTC),
			existingEntries: map[string]time.Time{
				"Species A": time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			},
			expectedPruned: 0,
			expectedRemain: []string{"Species A"}, // not pruned because disabled
		},
		{
			name:            "empty_map_returns_zero",
			resetMonth:      7,
			resetDay:        1,
			yearlyEnabled:   true,
			currentTime:     time.Date(2025, 8, 15, 10, 0, 0, 0, time.UTC),
			existingEntries: map[string]time.Time{},
			expectedPruned:  0,
			expectedRemain:  []string{},
		},
		{
			name:          "july_reset_after_reset_prunes_old",
			resetMonth:    7,
			resetDay:      1,
			yearlyEnabled: true,
			currentTime:   time.Date(2025, 8, 15, 10, 0, 0, 0, time.UTC), // After July 1 2025
			existingEntries: map[string]time.Time{
				"Old Species":  time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC),  // Before July 1 2025
				"New Species":  time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC), // After July 1 2025
				"Edge Species": time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),  // Exactly July 1 2025
			},
			expectedPruned: 1,
			expectedRemain: []string{"New Species", "Edge Species"},
		},
		{
			name:          "july_reset_before_reset_uses_previous_year_start",
			resetMonth:    7,
			resetDay:      1,
			yearlyEnabled: true,
			currentTime:   time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC), // Before July 1 2025
			existingEntries: map[string]time.Time{
				"Very Old":     time.Date(2023, 8, 1, 0, 0, 0, 0, time.UTC),  // Before July 1 2024
				"Current Year": time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC),  // After July 1 2024
				"Edge Case":    time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),  // Exactly July 1 2024
				"Recent":       time.Date(2025, 2, 15, 0, 0, 0, 0, time.UTC), // Within current tracking year
			},
			expectedPruned: 1,
			expectedRemain: []string{"Current Year", "Edge Case", "Recent"},
		},
		{
			name:          "dec_31_reset",
			resetMonth:    12,
			resetDay:      31,
			yearlyEnabled: true,
			currentTime:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC), // After Dec 31 2024
			existingEntries: map[string]time.Time{
				"Old":     time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),  // Before Dec 31 2024
				"Current": time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC), // On reset date
			},
			expectedPruned: 1,
			expectedRemain: []string{"Current"},
		},
		{
			name:          "jan_1_standard_reset",
			resetMonth:    1,
			resetDay:      1,
			yearlyEnabled: true,
			currentTime:   time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			existingEntries: map[string]time.Time{
				"Last Year": time.Date(2024, 12, 15, 0, 0, 0, 0, time.UTC),
				"This Year": time.Date(2025, 2, 15, 0, 0, 0, 0, time.UTC),
			},
			expectedPruned: 1,
			expectedRemain: []string{"This Year"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := &SpeciesTracker{
				resetMonth:      tt.resetMonth,
				resetDay:        tt.resetDay,
				yearlyEnabled:   tt.yearlyEnabled,
				speciesThisYear: make(map[string]time.Time),
			}

			// Copy entries to avoid mutation across tests
			maps.Copy(tracker.speciesThisYear, tt.existingEntries)

			pruned := tracker.pruneYearlyEntriesLocked(tt.currentTime)

			assert.Equal(t, tt.expectedPruned, pruned, "wrong number of entries pruned")

			// Verify remaining entries
			for _, species := range tt.expectedRemain {
				_, exists := tracker.speciesThisYear[species]
				assert.True(t, exists, "species %s should remain after pruning", species)
			}

			// Verify total count matches
			if tt.yearlyEnabled {
				assert.Len(t, tracker.speciesThisYear, len(tt.expectedRemain),
					"remaining entries count mismatch")
			}
		})
	}
}

// ============================================================================
// Test: isInEarlyWinterMonths Hemisphere Variations
// Target Coverage: season.go:89, all branches
// ============================================================================

func TestIsInEarlyWinterMonths_HemisphereVariations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		winterMonth  int // 0 means no winter configured
		currentMonth time.Month
		expected     bool
	}{
		// Northern Hemisphere (winter in December)
		{
			name:         "northern_january_is_early_winter",
			winterMonth:  12,
			currentMonth: time.January,
			expected:     true,
		},
		{
			name:         "northern_february_is_early_winter",
			winterMonth:  12,
			currentMonth: time.February,
			expected:     true,
		},
		{
			name:         "northern_march_not_early_winter",
			winterMonth:  12,
			currentMonth: time.March,
			expected:     false,
		},
		{
			name:         "northern_december_not_early_winter",
			winterMonth:  12,
			currentMonth: time.December,
			expected:     false,
		},
		{
			name:         "northern_november_not_early_winter",
			winterMonth:  12,
			currentMonth: time.November,
			expected:     false,
		},

		// Southern Hemisphere (winter in June)
		{
			name:         "southern_july_is_early_winter",
			winterMonth:  6,
			currentMonth: time.July,
			expected:     true,
		},
		{
			name:         "southern_august_is_early_winter",
			winterMonth:  6,
			currentMonth: time.August,
			expected:     true,
		},
		{
			name:         "southern_september_not_early_winter",
			winterMonth:  6,
			currentMonth: time.September,
			expected:     false,
		},
		{
			name:         "southern_june_not_early_winter",
			winterMonth:  6,
			currentMonth: time.June,
			expected:     false,
		},

		// No winter configured
		{
			name:         "no_winter_returns_false",
			winterMonth:  0,
			currentMonth: time.January,
			expected:     false,
		},

		// Edge case: winter in November
		{
			name:         "november_winter_december_is_early",
			winterMonth:  11,
			currentMonth: time.December,
			expected:     true,
		},
		{
			name:         "november_winter_january_is_early",
			winterMonth:  11,
			currentMonth: time.January,
			expected:     true,
		},
		{
			name:         "november_winter_february_not_early",
			winterMonth:  11,
			currentMonth: time.February,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := &SpeciesTracker{
				seasons: make(map[string]seasonDates),
			}

			// Configure winter season if specified
			if tt.winterMonth > 0 {
				tracker.seasons["winter"] = seasonDates{
					month: tt.winterMonth,
					day:   21, // typical winter solstice
				}
			}

			result := tracker.isInEarlyWinterMonths(tt.currentMonth)

			assert.Equal(t, tt.expected, result, "isInEarlyWinterMonths mismatch for %s", tt.name)
		})
	}
}

// ============================================================================
// Test: loadSingleSeasonData Error Paths
// Target Coverage: database.go:203
// ============================================================================

func TestLoadSingleSeasonData_ErrorPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		seasonName        string
		mockData          []datastore.NewSpeciesData
		mockError         error
		expectedMapSize   int
		expectedSpecies   []string
		expectError       bool
		expectEmptyResult bool
	}{
		{
			name:        "database_error_returns_error",
			seasonName:  "winter",
			mockData:    nil,
			mockError:   errors.New("database unavailable"),
			expectError: true,
		},
		{
			name:       "empty_first_seen_date_skipped",
			seasonName: "spring",
			mockData: []datastore.NewSpeciesData{
				{ScientificName: "Species A", FirstSeenDate: "2025-03-15"},
				{ScientificName: "Species B", FirstSeenDate: ""}, // Empty - should be skipped
				{ScientificName: "Species C", FirstSeenDate: "2025-04-01"},
			},
			mockError:       nil,
			expectedMapSize: 2,
			expectedSpecies: []string{"Species A", "Species C"},
			expectError:     false,
		},
		{
			name:       "invalid_date_format_skipped",
			seasonName: "summer",
			mockData: []datastore.NewSpeciesData{
				{ScientificName: "Species A", FirstSeenDate: "2025-06-15"},
				{ScientificName: "Species B", FirstSeenDate: "invalid-date"}, // Invalid - should be skipped
				{ScientificName: "Species C", FirstSeenDate: "15/06/2025"},   // Wrong format - should be skipped
			},
			mockError:       nil,
			expectedMapSize: 1,
			expectedSpecies: []string{"Species A"},
			expectError:     false,
		},
		{
			name:       "all_valid_data",
			seasonName: "fall",
			mockData: []datastore.NewSpeciesData{
				{ScientificName: "Species A", FirstSeenDate: "2025-09-15"},
				{ScientificName: "Species B", FirstSeenDate: "2025-10-01"},
				{ScientificName: "Species C", FirstSeenDate: "2025-10-20"},
			},
			mockError:       nil,
			expectedMapSize: 3,
			expectedSpecies: []string{"Species A", "Species B", "Species C"},
			expectError:     false,
		},
		{
			name:              "empty_result_from_database",
			seasonName:        "winter",
			mockData:          []datastore.NewSpeciesData{},
			mockError:         nil,
			expectedMapSize:   0,
			expectError:       false,
			expectEmptyResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockDS := mocks.NewMockInterface(t)

			mockDS.EXPECT().
				GetSpeciesFirstDetectionInPeriod(
					mock.Anything,
					mock.AnythingOfType("string"),
					mock.AnythingOfType("string"),
					mock.AnythingOfType("int"),
					mock.AnythingOfType("int"),
				).
				Return(tt.mockData, tt.mockError).
				Once()

			tracker := &SpeciesTracker{
				ds:      mockDS,
				seasons: make(map[string]seasonDates),
			}

			// Configure the season
			tracker.seasons[tt.seasonName] = seasonDates{
				month: 3, // March
				day:   20,
			}

			now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
			result, err := tracker.loadSingleSeasonData(tt.seasonName, now)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			if tt.expectEmptyResult {
				assert.Empty(t, result)
				return
			}

			assert.Len(t, result, tt.expectedMapSize)

			for _, species := range tt.expectedSpecies {
				_, exists := result[species]
				assert.True(t, exists, "species %s should be in result", species)
			}
		})
	}
}

// ============================================================================
// Test: calculateDaysSince edge cases
// Target: status.go:110 - ensure max(0, days) is applied correctly
// ============================================================================

func TestCalculateDaysSince_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		currentTime   time.Time
		referenceTime time.Time
		expectedDays  int
	}{
		{
			name:          "same_time_returns_zero",
			currentTime:   time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			referenceTime: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedDays:  0,
		},
		{
			name:          "current_before_reference_returns_zero",
			currentTime:   time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC),
			referenceTime: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedDays:  0, // max(0, negative) = 0
		},
		{
			name:          "one_day_difference",
			currentTime:   time.Date(2025, 6, 16, 10, 0, 0, 0, time.UTC),
			referenceTime: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedDays:  1,
		},
		{
			name:          "partial_day_rounds_down",
			currentTime:   time.Date(2025, 6, 16, 22, 0, 0, 0, time.UTC), // 36 hours later
			referenceTime: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedDays:  1, // 36 hours = 1.5 days, rounds to 1
		},
		{
			name:          "large_difference",
			currentTime:   time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			referenceTime: time.Date(2020, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedDays:  1826, // ~5 years including leap year
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := calculateDaysSince(tt.currentTime, tt.referenceTime)

			assert.Equal(t, tt.expectedDays, result)
		})
	}
}

// ============================================================================
// Test: allSeasonsEmpty helper
// Target: database.go:253
// ============================================================================

func TestAllSeasonsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		seasons  map[string]map[string]time.Time
		expected bool
	}{
		{
			name:     "nil_map_is_empty",
			seasons:  nil,
			expected: true,
		},
		{
			name:     "empty_map_is_empty",
			seasons:  map[string]map[string]time.Time{},
			expected: true,
		},
		{
			name: "all_season_maps_empty",
			seasons: map[string]map[string]time.Time{
				"spring": {},
				"summer": {},
			},
			expected: true,
		},
		{
			name: "one_season_has_data",
			seasons: map[string]map[string]time.Time{
				"spring": {},
				"summer": {"Species A": time.Now()},
			},
			expected: false,
		},
		{
			name: "all_seasons_have_data",
			seasons: map[string]map[string]time.Time{
				"spring": {"Species A": time.Now()},
				"summer": {"Species B": time.Now()},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := &SpeciesTracker{
				speciesBySeason: tt.seasons,
			}

			result := tracker.allSeasonsEmpty()

			assert.Equal(t, tt.expected, result)
		})
	}
}

// ============================================================================
// Helper functions
// ============================================================================
