package conf

import (
	"testing"
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
			if err := tt.settings.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("SpeciesTrackingSettings.Validate() error = %v, wantErr %v", err, tt.wantErr)
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
			if got := DetectHemisphere(tt.latitude); got != tt.want {
				t.Errorf("DetectHemisphere() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDefaultSeasons(t *testing.T) {
	t.Run("northern hemisphere", func(t *testing.T) {
		seasons := GetDefaultSeasons(45.0)
		if len(seasons) != 4 {
			t.Errorf("Expected 4 seasons, got %d", len(seasons))
		}
		if spring, ok := seasons["spring"]; !ok || spring.StartMonth != 3 {
			t.Errorf("Expected spring to start in March")
		}
	})

	t.Run("southern hemisphere", func(t *testing.T) {
		seasons := GetDefaultSeasons(-45.0)
		if len(seasons) != 4 {
			t.Errorf("Expected 4 seasons, got %d", len(seasons))
		}
		if spring, ok := seasons["spring"]; !ok || spring.StartMonth != 9 {
			t.Errorf("Expected spring to start in September for southern hemisphere")
		}
	})

	t.Run("equatorial region", func(t *testing.T) {
		seasons := GetDefaultSeasons(0.0)
		if len(seasons) != 4 {
			t.Errorf("Expected 4 seasons (wet/dry), got %d", len(seasons))
		}
		if wet1, ok := seasons["wet1"]; !ok || wet1.StartMonth != 3 {
			t.Errorf("Expected first wet season to start in March")
		}
		if dry1, ok := seasons["dry1"]; !ok || dry1.StartMonth != 6 {
			t.Errorf("Expected first dry season to start in June")
		}
		// Verify all expected seasons exist
		expectedSeasons := []string{"wet1", "dry1", "wet2", "dry2"}
		for _, season := range expectedSeasons {
			if _, ok := seasons[season]; !ok {
				t.Errorf("Expected season %s not found", season)
			}
		}
	})
}