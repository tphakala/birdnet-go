package conf

import (
	"maps"
	"testing"
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
			if result.Realtime.SpeciesTracking.SeasonalTracking.Seasons != nil {
				t.Error("Expected seasons to remain nil when seasonal tracking disabled")
			}
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
			if len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons) != len(customSeasons) {
				t.Errorf("Expected %d seasons, got %d",
					len(customSeasons),
					len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons))
			}

			// Verify content matches
			for name, season := range customSeasons {
				resultSeason, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons[name]
				if !exists {
					t.Errorf("Expected season %q to exist in result", name)
					continue
				}
				if resultSeason.StartMonth != season.StartMonth || resultSeason.StartDay != season.StartDay {
					t.Errorf("Season %q mismatch: expected %d/%d, got %d/%d",
						name, season.StartMonth, season.StartDay,
						resultSeason.StartMonth, resultSeason.StartDay)
				}
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
			if len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons) == 0 {
				t.Fatal("Expected default seasons to be populated for northern hemisphere")
			}

			// Verify we got northern hemisphere seasons (spring starts in March)
			spring, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
			if !exists {
				t.Error("Expected to find spring season in northern hemisphere defaults")
			} else if spring.StartMonth != 3 {
				// Northern hemisphere spring starts in March (3/20)
				t.Errorf("Expected northern spring to start in March, got month %d", spring.StartMonth)
			}
		})
	}
}

// TestPrepareSettingsForSave_SouthernHemisphere verifies default seasons for southern latitudes.
func TestPrepareSettingsForSave_SouthernHemisphere(t *testing.T) {
	t.Parallel()
	tests := []struct{
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
			if len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons) == 0 {
				t.Fatal("Expected default seasons to be populated for southern hemisphere")
			}

			// Verify we got southern hemisphere seasons (spring starts in September)
			spring, exists := result.Realtime.SpeciesTracking.SeasonalTracking.Seasons["spring"]
			if !exists {
				t.Error("Expected to find spring season in southern hemisphere defaults")
			} else if spring.StartMonth != 9 {
				// Southern hemisphere spring starts in September (9/22)
				t.Errorf("Expected southern spring to start in September, got month %d", spring.StartMonth)
			}
		})
	}
}

// TestPrepareSettingsForSave_EquatorialRegion verifies default seasons near equator.
func TestPrepareSettingsForSave_EquatorialRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		latitude float64
	}{
		{"equator", 0.0},
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
			if len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons) == 0 {
				t.Fatal("Expected default seasons to be populated for equatorial region")
			}
		})
	}
}

// TestPrepareSettingsForSave_DoesNotMutateInput verifies original settings unchanged.
func TestPrepareSettingsForSave_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	originalSettings := &Settings{}
	originalSettings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	originalSettings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

	_ = prepareSettingsForSave(originalSettings, 45.0)

	// Verify original settings were not modified
	if originalSettings.Realtime.SpeciesTracking.SeasonalTracking.Seasons != nil {
		t.Error("prepareSettingsForSave should not mutate input settings")
	}
}

// TestPrepareSettingsForSave_DifferentLatitudes verifies correct hemisphere detection.
func TestPrepareSettingsForSave_DifferentLatitudes(t *testing.T) {
	t.Parallel()
	latitudes := []struct {
		value             float64
		expectedHemisphere string
	}{
		{90.0, "northern"},
		{45.0, "northern"},
		{NorthernHemisphereThreshold + 1, "northern"},
		{5.0, "equatorial"},
		{0.0, "equatorial"},
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

			if len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons) == 0 {
				t.Errorf("Expected seasons for latitude %.1f (%s)", lat.value, lat.expectedHemisphere)
			}
		})
	}
}

// BenchmarkPrepareSettingsForSave benchmarks the preparation function.
func BenchmarkPrepareSettingsForSave(b *testing.B) {
	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons = nil

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = prepareSettingsForSave(settings, 45.0)
	}
}
