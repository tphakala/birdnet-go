package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpeciesTrackingSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		settings SpeciesTrackingSettings
		wantErr bool
	}{
		{
			name: "valid settings",
			settings: SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
			},
			wantErr: false,
		},
		{
			name: "window days too small",
			settings: SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 0,
				SyncIntervalMinutes:  60,
			},
			wantErr: true,
		},
		{
			name: "window days too large",
			settings: SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 366,
				SyncIntervalMinutes:  60,
			},
			wantErr: true,
		},
		{
			name: "sync interval too small",
			settings: SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  0,
			},
			wantErr: true,
		},
		{
			name: "sync interval too large",
			settings: SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  1441,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			err := tt.settings.Validate()
			if tt.wantErr {
				assert.Error(t, err, "Expected validation to return an error")
			} else {
				assert.NoError(t, err, "Expected validation to pass without error")
			}
		})
	}
}

func TestDetectHemisphere(t *testing.T) {
	tests := []struct {
		name     string
		latitude float64
		want     string
	}{
		{"northern positive", 45.5, "northern"},
		{"equator zero", 0.0, "equatorial"},
		{"equatorial north", 5.0, "equatorial"},
		{"equatorial south", -5.0, "equatorial"},
		{"boundary north", 10.1, "northern"},
		{"boundary south", -10.1, "southern"},
		{"exactly 10", 10.0, "equatorial"},
		{"exactly -10", -10.0, "equatorial"},
		{"southern negative", -33.8, "southern"},
		{"far north", 90.0, "northern"},
		{"far south", -90.0, "southern"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			got := DetectHemisphere(tt.latitude)
			assert.Equal(t, tt.want, got, "DetectHemisphere() returned unexpected hemisphere for latitude %v", tt.latitude)
		})
	}
}

// validateSeasonConfiguration is a helper function that validates season configuration
// for different hemispheres/regions to eliminate code duplication in tests
func validateSeasonConfiguration(t *testing.T, latitude float64, expectedSeasons map[string]Season, regionName string) {
	t.Helper()
	
	seasons := GetDefaultSeasons(latitude)
	assert.Len(t, seasons, 4, "Expected 4 seasons for %s", regionName)
	
	// Verify all expected seasons exist with correct start months and days
	for seasonName, expectedSeason := range expectedSeasons {
		season, exists := seasons[seasonName]
		assert.True(t, exists, "Expected season %s to exist in %s", seasonName, regionName)
		assert.Equal(t, expectedSeason.StartMonth, season.StartMonth,
			"Expected %s to start in month %d for %s, got %d", 
			seasonName, expectedSeason.StartMonth, regionName, season.StartMonth)
		assert.Equal(t, expectedSeason.StartDay, season.StartDay,
			"Expected %s to start on day %d for %s, got %d", 
			seasonName, expectedSeason.StartDay, regionName, season.StartDay)
	}
	
	// Verify no unexpected seasons exist
	for seasonName := range seasons {
		assert.Contains(t, expectedSeasons, seasonName,
			"Unexpected season %s found in %s", seasonName, regionName)
	}
}

func TestGetDefaultSeasons(t *testing.T) {
	t.Run("northern hemisphere", func(t *testing.T) {
		t.Parallel()

		expectedSeasons := map[string]Season{
			"spring": {StartMonth: 3, StartDay: 20},   // March 20
			"summer": {StartMonth: 6, StartDay: 21},   // June 21
			"fall":   {StartMonth: 9, StartDay: 22},   // September 22
			"winter": {StartMonth: 12, StartDay: 21},  // December 21
		}

		validateSeasonConfiguration(t, 45.0, expectedSeasons, "northern hemisphere")
	})

	t.Run("southern hemisphere", func(t *testing.T) {
		t.Parallel()

		// Seasons shifted by 6 months for southern hemisphere
		expectedSeasons := map[string]Season{
			"spring": {StartMonth: 9, StartDay: 22},   // September 22
			"summer": {StartMonth: 12, StartDay: 21},  // December 21
			"fall":   {StartMonth: 3, StartDay: 20},   // March 20
			"winter": {StartMonth: 6, StartDay: 21},   // June 21
		}

		validateSeasonConfiguration(t, -45.0, expectedSeasons, "southern hemisphere")
	})

	t.Run("equatorial region", func(t *testing.T) {
		t.Parallel()

		// Wet/dry season cycle for equatorial regions
		expectedSeasons := map[string]Season{
			"wet1": {StartMonth: 3, StartDay: 1},   // March-May wet season
			"dry1": {StartMonth: 6, StartDay: 1},   // June-August dry season
			"wet2": {StartMonth: 9, StartDay: 1},   // September-November wet season
			"dry2": {StartMonth: 12, StartDay: 1},  // December-February dry season
		}

		validateSeasonConfiguration(t, 0.0, expectedSeasons, "equatorial region")
	})
}

// TestIsDefaultSeasonConfiguration tests the isDefaultSeasonConfiguration helper function
func TestIsDefaultSeasonConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		seasons  map[string]Season
		expected bool
	}{
		{
			name: "traditional seasons",
			seasons: map[string]Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
			expected: true,
		},
		{
			name: "equatorial seasons",
			seasons: map[string]Season{
				"wet1": {StartMonth: 3, StartDay: 1},
				"dry1": {StartMonth: 6, StartDay: 1},
				"wet2": {StartMonth: 9, StartDay: 1},
				"dry2": {StartMonth: 12, StartDay: 1},
			},
			expected: true,
		},
		{
			name: "custom seasons - different names",
			seasons: map[string]Season{
				"rainy":  {StartMonth: 6, StartDay: 1},
				"cool":   {StartMonth: 10, StartDay: 1},
				"hot":    {StartMonth: 2, StartDay: 1},
				"windy":  {StartMonth: 12, StartDay: 1},
			},
			expected: false,
		},
		{
			name: "partial traditional seasons",
			seasons: map[string]Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"autumn": {StartMonth: 9, StartDay: 22}, // "autumn" instead of "fall"
				"winter": {StartMonth: 12, StartDay: 21},
			},
			expected: false,
		},
		{
			name:     "empty seasons",
			seasons:  map[string]Season{},
			expected: false,
		},
		{
			name:     "nil seasons",
			seasons:  nil,
			expected: false,
		},
		{
			name: "only 3 seasons",
			seasons: map[string]Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
			},
			expected: false,
		},
		{
			name: "5 seasons",
			seasons: map[string]Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
				"extra":  {StartMonth: 1, StartDay: 1},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isDefaultSeasonConfiguration(tt.seasons)
			assert.Equal(t, tt.expected, got, "isDefaultSeasonConfiguration() returned unexpected result")
		})
	}
}

// TestGetSeasonalTrackingWithHemisphere_Issue1524 tests the fix for issue #1524
// where users with pre-existing Northern hemisphere seasons would not get updated
// to Southern hemisphere seasons when their latitude indicated Southern hemisphere.
func TestGetSeasonalTrackingWithHemisphere_Issue1524(t *testing.T) {
	t.Run("empty seasons get populated", func(t *testing.T) {
		t.Parallel()

		settings := SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons:    nil,
		}

		result := GetSeasonalTrackingWithHemisphere(settings, -33.8688) // Sydney
		assert.Len(t, result.Seasons, 4, "Should have 4 seasons")
		assert.Equal(t, 9, result.Seasons["spring"].StartMonth, "Southern spring should start in September")
	})

	t.Run("northern seasons updated to southern", func(t *testing.T) {
		t.Parallel()

		// Pre-existing Northern hemisphere seasons (the bug scenario)
		settings := SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		}

		// Apply to Southern hemisphere user
		result := GetSeasonalTrackingWithHemisphere(settings, -33.8688) // Sydney

		// Verify seasons were updated to Southern hemisphere
		assert.Equal(t, 9, result.Seasons["spring"].StartMonth, "Southern spring should start in September")
		assert.Equal(t, 12, result.Seasons["summer"].StartMonth, "Southern summer should start in December")
		assert.Equal(t, 3, result.Seasons["fall"].StartMonth, "Southern fall should start in March")
		assert.Equal(t, 6, result.Seasons["winter"].StartMonth, "Southern winter should start in June")
	})

	t.Run("southern seasons kept for southern hemisphere", func(t *testing.T) {
		t.Parallel()

		// Southern hemisphere seasons already set
		settings := SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]Season{
				"spring": {StartMonth: 9, StartDay: 22},
				"summer": {StartMonth: 12, StartDay: 21},
				"fall":   {StartMonth: 3, StartDay: 20},
				"winter": {StartMonth: 6, StartDay: 21},
			},
		}

		result := GetSeasonalTrackingWithHemisphere(settings, -33.8688) // Sydney

		// Should stay Southern
		assert.Equal(t, 9, result.Seasons["spring"].StartMonth, "Southern spring should stay in September")
	})

	t.Run("northern seasons kept for northern hemisphere", func(t *testing.T) {
		t.Parallel()

		settings := SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		}

		result := GetSeasonalTrackingWithHemisphere(settings, 60.1699) // Helsinki

		// Should stay Northern
		assert.Equal(t, 3, result.Seasons["spring"].StartMonth, "Northern spring should stay in March")
	})

	t.Run("custom seasons preserved", func(t *testing.T) {
		t.Parallel()

		// User has custom season names
		settings := SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]Season{
				"rainy": {StartMonth: 6, StartDay: 1},
				"cool":  {StartMonth: 10, StartDay: 1},
				"hot":   {StartMonth: 2, StartDay: 1},
				"windy": {StartMonth: 12, StartDay: 1},
			},
		}

		result := GetSeasonalTrackingWithHemisphere(settings, -33.8688) // Sydney

		// Custom seasons should be preserved
		assert.Equal(t, 6, result.Seasons["rainy"].StartMonth, "Custom rainy season should be preserved")
		_, exists := result.Seasons["spring"]
		assert.False(t, exists, "Should not add default seasons when custom seasons exist")
	})

	t.Run("equatorial to traditional - update based on latitude", func(t *testing.T) {
		t.Parallel()

		// User has equatorial seasons but is at Northern latitude
		settings := SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]Season{
				"wet1": {StartMonth: 3, StartDay: 1},
				"dry1": {StartMonth: 6, StartDay: 1},
				"wet2": {StartMonth: 9, StartDay: 1},
				"dry2": {StartMonth: 12, StartDay: 1},
			},
		}

		// Apply to Northern hemisphere user (outside equatorial zone)
		result := GetSeasonalTrackingWithHemisphere(settings, 60.1699) // Helsinki

		// Should be updated to Northern hemisphere traditional seasons
		_, hasSpring := result.Seasons["spring"]
		assert.True(t, hasSpring, "Should have traditional spring for Northern hemisphere")
		assert.Equal(t, 3, result.Seasons["spring"].StartMonth, "Northern spring should start in March")
	})
}