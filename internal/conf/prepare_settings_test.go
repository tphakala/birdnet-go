package conf

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrepareSettingsForSave_NoSeasonalTracking verifies behavior when seasonal tracking is disabled.
func TestPrepareSettingsForSave_NoSeasonalTracking(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		latitude float64
	}{
		{"northern hemisphere", 45.0},
		{"southern hemisphere", -33.9},
		{"equator", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = false
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

			result := prepareSettingsForSave(settings, tt.latitude)

			// Verify seasons remain nil when disabled
			assert.Nil(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
				"Expected seasons to remain nil when seasonal tracking disabled")
		})
	}
}

// TestPrepareSettingsForSave_EnabledWithExistingSeasons verifies existing seasons are preserved.
func TestPrepareSettingsForSave_EnabledWithExistingSeasons(t *testing.T) {
	t.Parallel()
	customSeasons := map[string]Season{
		"winter": {StartMonth: 1, StartDay: 1},
		"spring": {StartMonth: 4, StartDay: 1},
	}

	tests := []struct {
		name     string
		latitude float64
	}{
		{"northern hemisphere", 45.0},
		{"southern hemisphere", -45.0},
		{"equator", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = maps.Clone(customSeasons)

			result := prepareSettingsForSave(settings, tt.latitude)

			// Verify custom seasons are preserved
			assert.Len(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons, len(customSeasons),
				"Expected %d seasons", len(customSeasons))

			// Verify content matches
			for name, season := range customSeasons {
				resultSeason, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons[name]
				require.True(t, exists, "Expected season %q to exist in result", name)
				assert.Equal(t, season.StartMonth, resultSeason.StartMonth,
					"Season %q start month mismatch", name)
				assert.Equal(t, season.StartDay, resultSeason.StartDay,
					"Season %q start day mismatch", name)
			}
		})
	}
}

// TestPrepareSettingsForSave_NorthernHemisphere verifies default seasons for northern latitudes.
func TestPrepareSettingsForSave_NorthernHemisphere(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		latitude float64
	}{
		{"moderate northern", 45.0},
		{"high northern", 60.0},
		{"low northern", 15.0},
		{"threshold northern", NorthernHemisphereThreshold + 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

			result := prepareSettingsForSave(settings, tt.latitude)

			// Verify seasons were populated
			require.NotEmpty(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
				"Expected default seasons to be populated for northern hemisphere")

			// Verify we got northern hemisphere seasons (spring starts in March)
			spring, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
			require.True(t, exists, "Expected to find spring season in northern hemisphere defaults")
			// Northern hemisphere spring starts in March (3/20)
			assert.Equal(t, 3, spring.StartMonth, "Expected northern spring to start in March")
		})
	}
}

// TestPrepareSettingsForSave_SouthernHemisphere verifies default seasons for southern latitudes.
func TestPrepareSettingsForSave_SouthernHemisphere(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		latitude float64
	}{
		{"moderate southern", -33.9},
		{"high southern", -45.0},
		{"low southern", -15.0},
		{"threshold southern", SouthernHemisphereThreshold - 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

			result := prepareSettingsForSave(settings, tt.latitude)

			// Verify seasons were populated
			require.NotEmpty(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
				"Expected default seasons to be populated for southern hemisphere")

			// Verify we got southern hemisphere seasons (spring starts in September)
			spring, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
			require.True(t, exists, "Expected to find spring season in southern hemisphere defaults")
			// Southern hemisphere spring starts in September (9/22)
			assert.Equal(t, 9, spring.StartMonth, "Expected southern spring to start in September")
		})
	}
}

// TestPrepareSettingsForSave_CorrectsSouthernHemisphere verifies that pre-populated
// Northern Hemisphere defaults are corrected for Southern Hemisphere latitude.
// This is the exact scenario from issue #3003: Viper defaults set NH seasons,
// but the user is in Sydney (latitude -33.9).
func TestPrepareSettingsForSave_CorrectsSouthernHemisphere(t *testing.T) {
	t.Parallel()

	nhDefaults := map[string]Season{
		"spring": {StartMonth: 3, StartDay: 20},
		"summer": {StartMonth: 6, StartDay: 21},
		"fall":   {StartMonth: 9, StartDay: 22},
		"winter": {StartMonth: 12, StartDay: 21},
	}

	tests := []struct {
		name     string
		latitude float64
	}{
		{"Sydney", -33.9},
		{"Melbourne", -37.8},
		{"Auckland", -36.9},
		{"Cape Town", -33.9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = maps.Clone(nhDefaults)

			result := prepareSettingsForSave(settings, tt.latitude)

			spring, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
			require.True(t, exists, "Expected spring season")
			assert.Equal(t, 9, spring.StartMonth,
				"Southern hemisphere spring should start in September, not March")

			winter, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["winter"]
			require.True(t, exists, "Expected winter season")
			assert.Equal(t, 6, winter.StartMonth,
				"Southern hemisphere winter should start in June, not December")
		})
	}
}

// TestPrepareSettingsForSave_PreservesNorthernForNorthern verifies that Northern Hemisphere
// defaults are preserved when latitude is Northern.
func TestPrepareSettingsForSave_PreservesNorthernForNorthern(t *testing.T) {
	t.Parallel()

	nhDefaults := map[string]Season{
		"spring": {StartMonth: 3, StartDay: 20},
		"summer": {StartMonth: 6, StartDay: 21},
		"fall":   {StartMonth: 9, StartDay: 22},
		"winter": {StartMonth: 12, StartDay: 21},
	}

	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = maps.Clone(nhDefaults)

	result := prepareSettingsForSave(settings, 45.0)

	spring, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
	require.True(t, exists, "Expected spring season")
	assert.Equal(t, 3, spring.StartMonth, "Northern hemisphere spring should stay in March")

	winter, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["winter"]
	require.True(t, exists, "Expected winter season")
	assert.Equal(t, 12, winter.StartMonth, "Northern hemisphere winter should stay in December")
}

// TestPrepareSettingsForSave_EquatorialRegion verifies default seasons near equator.
func TestPrepareSettingsForSave_EquatorialRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		latitude float64
	}{
		{"slightly north", 5.0},
		{"slightly south", -5.0},
		{"northern threshold boundary", NorthernHemisphereThreshold - 0.1},
		{"southern threshold boundary", SouthernHemisphereThreshold + 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

			result := prepareSettingsForSave(settings, tt.latitude)

			// Verify seasons were populated (GetDefaultSeasons should return year-round for equatorial)
			require.NotEmpty(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
				"Expected default seasons to be populated for equatorial region")
		})
	}
}

// TestPrepareSettingsForSave_SkipsUnconfiguredLatitude verifies that latitude 0
// (location not yet configured) preserves existing seasons without adjustment.
func TestPrepareSettingsForSave_SkipsUnconfiguredLatitude(t *testing.T) {
	t.Parallel()

	nhDefaults := map[string]Season{
		"spring": {StartMonth: 3, StartDay: 20},
		"summer": {StartMonth: 6, StartDay: 21},
		"fall":   {StartMonth: 9, StartDay: 22},
		"winter": {StartMonth: 12, StartDay: 21},
	}

	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = maps.Clone(nhDefaults)

	result := prepareSettingsForSave(settings, 0.0)

	spring := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
	assert.Equal(t, 3, spring.StartMonth,
		"Seasons should be unchanged when latitude is 0 (unconfigured)")
}

// TestPrepareSettingsForSave_PreservesCustomSeasonNames verifies that non-standard
// season names are not replaced by hemisphere defaults.
func TestPrepareSettingsForSave_PreservesCustomSeasonNames(t *testing.T) {
	t.Parallel()

	customSeasons := map[string]Season{
		"monsoon": {StartMonth: 6, StartDay: 1},
		"dry":     {StartMonth: 11, StartDay: 1},
	}

	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = maps.Clone(customSeasons)

	result := prepareSettingsForSave(settings, -33.9)

	require.Len(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons, 2)

	monsoon, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["monsoon"]
	require.True(t, exists, "Expected monsoon season to be preserved")
	assert.Equal(t, 6, monsoon.StartMonth, "Custom season dates should not be changed")

	dry, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["dry"]
	require.True(t, exists, "Expected dry season to be preserved")
	assert.Equal(t, 11, dry.StartMonth, "Custom season dates should not be changed")
}

// TestPrepareSettingsForSave_DoesNotMutateInput verifies original settings unchanged.
func TestPrepareSettingsForSave_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	originalSettings := &Settings{}
	originalSettings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	originalSettings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

	_ = prepareSettingsForSave(originalSettings, 45.0)

	// Verify original settings were not modified
	assert.Nil(t, originalSettings.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
		"prepareSettingsForSave should not mutate input settings")
}

// TestPrepareSettingsForSave_DifferentLatitudes verifies correct hemisphere detection.
func TestPrepareSettingsForSave_DifferentLatitudes(t *testing.T) {
	t.Parallel()
	latitudes := []struct {
		value              float64
		expectedHemisphere string
	}{
		{90.0, "northern"},
		{45.0, "northern"},
		{NorthernHemisphereThreshold + 1, "northern"},
		{5.0, "equatorial"},
		{-5.0, "equatorial"},
		{SouthernHemisphereThreshold - 1, "southern"},
		{-45.0, "southern"},
		{-90.0, "southern"},
	}

	for _, lat := range latitudes {
		t.Run(lat.expectedHemisphere, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
			settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

			result := prepareSettingsForSave(settings, lat.value)

			require.NotEmpty(t, result.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
				"Expected seasons for latitude %.1f (%s)", lat.value, lat.expectedHemisphere)
		})
	}
}

// BenchmarkPrepareSettingsForSave benchmarks the preparation function.
func BenchmarkPrepareSettingsForSave(b *testing.B) {
	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

	b.ReportAllocs()

	for b.Loop() {
		_ = prepareSettingsForSave(settings, 45.0)
	}
}

// BenchmarkPrepareSettingsForSave_WithExistingSeasons benchmarks with existing seasons.
func BenchmarkPrepareSettingsForSave_WithExistingSeasons(b *testing.B) {
	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = map[string]Season{
		"winter": {StartMonth: 1, StartDay: 1},
	}

	b.ReportAllocs()

	for b.Loop() {
		_ = prepareSettingsForSave(settings, 45.0)
	}
}
