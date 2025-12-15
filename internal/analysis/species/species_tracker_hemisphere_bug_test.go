package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestSouthernHemisphereSeasonBug_Issue1524 reproduces the bug reported in issue #1524
// where users in the Southern hemisphere see Northern hemisphere seasons (e.g., "fall")
// when they should see Southern hemisphere seasons (e.g., "spring").
//
// ROOT CAUSE IDENTIFIED:
// GetSeasonalTrackingWithHemisphere only populates seasons if len(Seasons) == 0.
// If a user already has Northern hemisphere seasons saved in their config (from before
// the fix, or from initializeDefaultSeasons fallback), they won't be updated.
//
// This test reproduces the exact bug scenario:
// 1. User has pre-existing Northern hemisphere seasons in config
// 2. User is in Southern hemisphere
// 3. GetSeasonalTrackingWithHemisphere is called but doesn't update seasons
// 4. User sees incorrect seasons
func TestSouthernHemisphereSeasonBug_Issue1524(t *testing.T) {
	t.Parallel()

	// Southern hemisphere latitude (e.g., Sydney, Australia)
	southernLatitude := -33.8688

	// THE BUG: User has Northern hemisphere seasons pre-saved in their config
	// This happens when:
	// - User upgraded from before the hemisphere fix
	// - Config was initialized with Northern defaults before latitude was set
	// - User imported someone else's config
	northernHemisphereSeasons := map[string]conf.Season{
		"spring": {StartMonth: 3, StartDay: 20},  // March 20 (Northern)
		"summer": {StartMonth: 6, StartDay: 21},  // June 21 (Northern)
		"fall":   {StartMonth: 9, StartDay: 22},  // September 22 (Northern)
		"winter": {StartMonth: 12, StartDay: 21}, // December 21 (Northern)
	}

	testCases := []struct {
		name           string
		date           time.Time
		expectedSeason string
		description    string
	}{
		{
			name:           "December25_ShouldBeSummer_NotWinter",
			date:           time.Date(2025, 12, 25, 12, 0, 0, 0, time.UTC),
			expectedSeason: "summer", // Expected for Southern hemisphere
			description:    "December 25 in Southern hemisphere should be summer, not winter",
		},
		{
			name:           "January_ShouldBeSummer_NotWinter",
			date:           time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "summer",
			description:    "January in Southern hemisphere should be summer, not winter",
		},
		{
			name:           "April_ShouldBeFall_NotSpring",
			date:           time.Date(2025, 4, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "fall",
			description:    "April in Southern hemisphere should be fall, not spring",
		},
		{
			name:           "July_ShouldBeWinter_NotSummer",
			date:           time.Date(2025, 7, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "winter",
			description:    "July in Southern hemisphere should be winter, not summer",
		},
		{
			name:           "October_ShouldBeSpring_NotFall",
			date:           time.Date(2025, 10, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "spring",
			description:    "October in Southern hemisphere should be spring, not fall",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Step 1: Create settings with EXISTING Northern hemisphere seasons
			// This simulates a user who has an old config with Northern seasons
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled:    true,
					WindowDays: 21,
					Seasons:    northernHemisphereSeasons, // PRE-EXISTING Northern seasons!
				},
			}

			// Step 2: Call GetSeasonalTrackingWithHemisphere with Southern latitude
			// BUG: This should update seasons for Southern hemisphere, but it doesn't
			// because len(settings.Seasons) > 0
			hemisphereAwareSettings := *settings
			if hemisphereAwareSettings.SeasonalTracking.Enabled {
				hemisphereAwareSettings.SeasonalTracking = conf.GetSeasonalTrackingWithHemisphere(
					hemisphereAwareSettings.SeasonalTracking,
					southernLatitude,
				)
			}

			// Step 3: Create tracker from hemisphere-aware settings
			tracker := NewTrackerFromSettings(nil, &hemisphereAwareSettings)
			require.NotNil(t, tracker)

			// Step 4: Verify that seasons were correctly updated for Southern hemisphere
			// BUG: The seasons are NOT updated because GetSeasonalTrackingWithHemisphere
			// only sets seasons when len(Seasons) == 0
			//
			// Expected Southern hemisphere seasons:
			// - Spring: September 22
			// - Summer: December 21
			// - Fall: March 20
			// - Winter: June 21

			// Step 5: Check the actual season detection
			actualSeason := tracker.getCurrentSeason(tc.date)
			assert.Equal(t, tc.expectedSeason, actualSeason,
				"BUG #1524: %s - got %s instead of %s",
				tc.description, actualSeason, tc.expectedSeason)
		})
	}
}

// TestGetSeasonalTrackingWithHemisphere_ShouldUpdateExistingSeasons tests that
// GetSeasonalTrackingWithHemisphere correctly updates seasons even when seasons
// already exist in the configuration.
//
// This is the core fix needed for issue #1524.
func TestGetSeasonalTrackingWithHemisphere_ShouldUpdateExistingSeasons(t *testing.T) {
	t.Parallel()

	// Pre-existing Northern hemisphere seasons (the bug scenario)
	northernSeasons := map[string]conf.Season{
		"spring": {StartMonth: 3, StartDay: 20},
		"summer": {StartMonth: 6, StartDay: 21},
		"fall":   {StartMonth: 9, StartDay: 22},
		"winter": {StartMonth: 12, StartDay: 21},
	}

	testCases := []struct {
		name             string
		latitude         float64
		expectedSeasons  map[string]conf.Season
		description      string
	}{
		{
			name:     "SouthernHemisphere_ShouldOverrideNorthernSeasons",
			latitude: -33.8688, // Sydney
			expectedSeasons: map[string]conf.Season{
				"spring": {StartMonth: 9, StartDay: 22},  // September
				"summer": {StartMonth: 12, StartDay: 21}, // December
				"fall":   {StartMonth: 3, StartDay: 20},  // March
				"winter": {StartMonth: 6, StartDay: 21},  // June
			},
			description: "Southern hemisphere should have inverted seasons",
		},
		{
			name:     "NorthernHemisphere_ShouldKeepNorthernSeasons",
			latitude: 60.1699, // Helsinki
			expectedSeasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
			description: "Northern hemisphere should keep Northern seasons",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Start with Northern hemisphere seasons
			settings := conf.SeasonalTrackingSettings{
				Enabled:    true,
				WindowDays: 21,
				Seasons:    northernSeasons,
			}

			// Apply hemisphere detection
			result := conf.GetSeasonalTrackingWithHemisphere(settings, tc.latitude)

			// Verify seasons were correctly updated for the hemisphere
			for seasonName, expectedSeason := range tc.expectedSeasons {
				actualSeason, exists := result.Seasons[seasonName]
				assert.True(t, exists, "Season %s should exist", seasonName)
				assert.Equal(t, expectedSeason.StartMonth, actualSeason.StartMonth,
					"%s: %s season should start in month %d, got %d",
					tc.description, seasonName, expectedSeason.StartMonth, actualSeason.StartMonth)
				assert.Equal(t, expectedSeason.StartDay, actualSeason.StartDay,
					"%s: %s season should start on day %d, got %d",
					tc.description, seasonName, expectedSeason.StartDay, actualSeason.StartDay)
			}
		})
	}
}

// TestNorthernHemisphereSeasonDetection_Baseline verifies Northern hemisphere still works correctly
// This is a baseline test to ensure we don't break existing functionality when fixing #1524
func TestNorthernHemisphereSeasonDetection_Baseline(t *testing.T) {
	t.Parallel()

	// Northern hemisphere latitude (e.g., Helsinki, Finland)
	northernLatitude := 60.1699

	testCases := []struct {
		name           string
		date           time.Time
		expectedSeason string
	}{
		{
			name:           "December_ShouldBeWinter",
			date:           time.Date(2025, 12, 25, 12, 0, 0, 0, time.UTC),
			expectedSeason: "winter",
		},
		{
			name:           "January_ShouldBeWinter",
			date:           time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "winter",
		},
		{
			name:           "April_ShouldBeSpring",
			date:           time.Date(2025, 4, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "spring",
		},
		{
			name:           "July_ShouldBeSummer",
			date:           time.Date(2025, 7, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "summer",
		},
		{
			name:           "October_ShouldBeFall",
			date:           time.Date(2025, 10, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "fall",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled:    true,
					WindowDays: 21,
					Seasons:    nil, // NO custom seasons
				},
			}

			// Apply hemisphere detection
			hemisphereAwareSettings := *settings
			if hemisphereAwareSettings.SeasonalTracking.Enabled {
				hemisphereAwareSettings.SeasonalTracking = conf.GetSeasonalTrackingWithHemisphere(
					hemisphereAwareSettings.SeasonalTracking,
					northernLatitude,
				)
			}

			tracker := NewTrackerFromSettings(nil, &hemisphereAwareSettings)
			require.NotNil(t, tracker)

			actualSeason := tracker.getCurrentSeason(tc.date)
			assert.Equal(t, tc.expectedSeason, actualSeason,
				"Northern hemisphere: %s should be %s, got %s",
				tc.date.Format("January"), tc.expectedSeason, actualSeason)
		})
	}
}

// TestEquatorialSeasonDetection_Baseline verifies Equatorial region still works correctly
func TestEquatorialSeasonDetection_Baseline(t *testing.T) {
	t.Parallel()

	// Equatorial latitude (e.g., Singapore)
	equatorialLatitude := 1.3521

	testCases := []struct {
		name           string
		date           time.Time
		expectedSeason string
	}{
		{
			name:           "March_ShouldBeWet1",
			date:           time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "wet1",
		},
		{
			name:           "June_ShouldBeDry1",
			date:           time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "dry1",
		},
		{
			name:           "September_ShouldBeWet2",
			date:           time.Date(2025, 9, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "wet2",
		},
		{
			name:           "December_ShouldBeDry2",
			date:           time.Date(2025, 12, 15, 12, 0, 0, 0, time.UTC),
			expectedSeason: "dry2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled:    true,
					WindowDays: 21,
					Seasons:    nil, // NO custom seasons
				},
			}

			// Apply hemisphere detection
			hemisphereAwareSettings := *settings
			if hemisphereAwareSettings.SeasonalTracking.Enabled {
				hemisphereAwareSettings.SeasonalTracking = conf.GetSeasonalTrackingWithHemisphere(
					hemisphereAwareSettings.SeasonalTracking,
					equatorialLatitude,
				)
			}

			tracker := NewTrackerFromSettings(nil, &hemisphereAwareSettings)
			require.NotNil(t, tracker)

			actualSeason := tracker.getCurrentSeason(tc.date)
			assert.Equal(t, tc.expectedSeason, actualSeason,
				"Equatorial: %s should be %s, got %s",
				tc.date.Format("January"), tc.expectedSeason, actualSeason)
		})
	}
}

// TestSeasonOrderInitialization_SouthernHemisphere verifies that the season order cache
// is correctly initialized for Southern hemisphere seasons
func TestSeasonOrderInitialization_SouthernHemisphere(t *testing.T) {
	t.Parallel()

	// Create settings with Southern hemisphere seasons
	southernSeasons := conf.GetDefaultSeasons(-45.0) // Southern hemisphere
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons:    southernSeasons,
		},
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Verify seasons were loaded correctly
	assert.Len(t, tracker.seasons, 4, "Should have 4 seasons")

	// Verify Southern hemisphere season dates
	// Southern hemisphere:
	// - Spring: September 22
	// - Summer: December 21
	// - Fall: March 20
	// - Winter: June 21
	spring, hasSpring := tracker.seasons["spring"]
	assert.True(t, hasSpring, "Should have spring season")
	assert.Equal(t, 9, spring.month, "Southern spring should start in September")
	assert.Equal(t, 22, spring.day, "Southern spring should start on 22nd")

	summer, hasSummer := tracker.seasons["summer"]
	assert.True(t, hasSummer, "Should have summer season")
	assert.Equal(t, 12, summer.month, "Southern summer should start in December")
	assert.Equal(t, 21, summer.day, "Southern summer should start on 21st")

	fall, hasFall := tracker.seasons["fall"]
	assert.True(t, hasFall, "Should have fall season")
	assert.Equal(t, 3, fall.month, "Southern fall should start in March")
	assert.Equal(t, 20, fall.day, "Southern fall should start on 20th")

	winter, hasWinter := tracker.seasons["winter"]
	assert.True(t, hasWinter, "Should have winter season")
	assert.Equal(t, 6, winter.month, "Southern winter should start in June")
	assert.Equal(t, 21, winter.day, "Southern winter should start on 21st")
}
