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

func TestGetDefaultSeasons(t *testing.T) {
	t.Run("northern hemisphere", func(t *testing.T) {
		t.Parallel()
		
		seasons := GetDefaultSeasons(45.0)
		assert.Len(t, seasons, 4, "Expected 4 seasons for northern hemisphere")
		
		// Verify all four seasons exist with correct start months and days
		expectedSeasons := map[string]Season{
			"spring": {StartMonth: 3, StartDay: 20},   // March 20
			"summer": {StartMonth: 6, StartDay: 21},   // June 21
			"fall":   {StartMonth: 9, StartDay: 22},   // September 22
			"winter": {StartMonth: 12, StartDay: 21},  // December 21
		}
		
		for seasonName, expectedSeason := range expectedSeasons {
			season, exists := seasons[seasonName]
			assert.True(t, exists, "Expected season %s to exist in northern hemisphere", seasonName)
			assert.Equal(t, expectedSeason.StartMonth, season.StartMonth, 
				"Expected %s to start in month %d, got %d", seasonName, expectedSeason.StartMonth, season.StartMonth)
			assert.Equal(t, expectedSeason.StartDay, season.StartDay,
				"Expected %s to start on day %d, got %d", seasonName, expectedSeason.StartDay, season.StartDay)
		}
		
		// Verify no unexpected seasons exist
		for seasonName := range seasons {
			assert.Contains(t, expectedSeasons, seasonName, 
				"Unexpected season %s found in northern hemisphere", seasonName)
		}
	})

	t.Run("southern hemisphere", func(t *testing.T) {
		t.Parallel()
		
		seasons := GetDefaultSeasons(-45.0)
		assert.Len(t, seasons, 4, "Expected 4 seasons for southern hemisphere")
		
		// Verify all four seasons exist with correct start months and days (shifted by 6 months)
		expectedSeasons := map[string]Season{
			"spring": {StartMonth: 9, StartDay: 22},   // September 22
			"summer": {StartMonth: 12, StartDay: 21},  // December 21
			"fall":   {StartMonth: 3, StartDay: 20},   // March 20
			"winter": {StartMonth: 6, StartDay: 21},   // June 21
		}
		
		for seasonName, expectedSeason := range expectedSeasons {
			season, exists := seasons[seasonName]
			assert.True(t, exists, "Expected season %s to exist in southern hemisphere", seasonName)
			assert.Equal(t, expectedSeason.StartMonth, season.StartMonth,
				"Expected %s to start in month %d for southern hemisphere, got %d", 
				seasonName, expectedSeason.StartMonth, season.StartMonth)
			assert.Equal(t, expectedSeason.StartDay, season.StartDay,
				"Expected %s to start on day %d for southern hemisphere, got %d", 
				seasonName, expectedSeason.StartDay, season.StartDay)
		}
		
		// Verify no unexpected seasons exist
		for seasonName := range seasons {
			assert.Contains(t, expectedSeasons, seasonName,
				"Unexpected season %s found in southern hemisphere", seasonName)
		}
	})

	t.Run("equatorial region", func(t *testing.T) {
		t.Parallel()
		
		seasons := GetDefaultSeasons(0.0)
		assert.Len(t, seasons, 4, "Expected 4 seasons (wet/dry cycles) for equatorial region")
		
		// Verify wet/dry season cycle with correct start months and days
		expectedSeasons := map[string]Season{
			"wet1": {StartMonth: 3, StartDay: 1},   // March-May wet season
			"dry1": {StartMonth: 6, StartDay: 1},   // June-August dry season
			"wet2": {StartMonth: 9, StartDay: 1},   // September-November wet season
			"dry2": {StartMonth: 12, StartDay: 1},  // December-February dry season
		}
		
		for seasonName, expectedSeason := range expectedSeasons {
			season, exists := seasons[seasonName]
			assert.True(t, exists, "Expected season %s to exist in equatorial region", seasonName)
			assert.Equal(t, expectedSeason.StartMonth, season.StartMonth,
				"Expected %s to start in month %d for equatorial region, got %d", 
				seasonName, expectedSeason.StartMonth, season.StartMonth)
			assert.Equal(t, expectedSeason.StartDay, season.StartDay,
				"Expected %s to start on day %d for equatorial region, got %d", 
				seasonName, expectedSeason.StartDay, season.StartDay)
		}
		
		// Verify no unexpected seasons exist
		for seasonName := range seasons {
			assert.Contains(t, expectedSeasons, seasonName,
				"Unexpected season %s found in equatorial region", seasonName)
		}
	})
}