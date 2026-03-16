package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSettings_MigrateLocationConfigured(t *testing.T) {
	tests := []struct {
		name               string
		latitude           float64
		longitude          float64
		locationConfigured bool
		wantConfigured     bool
	}{
		{
			name:               "already configured flag set",
			latitude:           40.7128,
			longitude:          -74.006,
			locationConfigured: true,
			wantConfigured:     true,
		},
		{
			name:               "non-zero coordinates without flag",
			latitude:           40.7128,
			longitude:          -74.006,
			locationConfigured: false,
			wantConfigured:     true,
		},
		{
			name:               "zero coordinates without flag stays unconfigured",
			latitude:           0,
			longitude:          0,
			locationConfigured: false,
			wantConfigured:     false,
		},
		{
			name:               "only latitude non-zero sets flag",
			latitude:           51.5074,
			longitude:          0,
			locationConfigured: false,
			wantConfigured:     true,
		},
		{
			name:               "only longitude non-zero sets flag",
			latitude:           0,
			longitude:          -0.1278,
			locationConfigured: false,
			wantConfigured:     true,
		},
		{
			name:               "negative coordinates set flag",
			latitude:           -33.8688,
			longitude:          151.2093,
			locationConfigured: false,
			wantConfigured:     true,
		},
		{
			name:               "zero lat/lon with flag true stays true",
			latitude:           0,
			longitude:          0,
			locationConfigured: true,
			wantConfigured:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &Settings{
				BirdNET: BirdNETConfig{
					Latitude:           tt.latitude,
					Longitude:          tt.longitude,
					LocationConfigured: tt.locationConfigured,
				},
			}

			settings.MigrateLocationConfigured()

			assert.Equal(t, tt.wantConfigured, settings.BirdNET.LocationConfigured,
				"LocationConfigured should be %v for lat=%v, lon=%v",
				tt.wantConfigured, tt.latitude, tt.longitude)
		})
	}
}

func TestSettings_MigrateLocationConfigured_Idempotent(t *testing.T) {
	settings := &Settings{
		BirdNET: BirdNETConfig{
			Latitude:           40.7128,
			Longitude:          -74.006,
			LocationConfigured: false,
		},
	}

	// First migration
	settings.MigrateLocationConfigured()
	assert.True(t, settings.BirdNET.LocationConfigured)

	// Second migration should not change anything
	settings.MigrateLocationConfigured()
	assert.True(t, settings.BirdNET.LocationConfigured)
}
