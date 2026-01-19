package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestGetSpeciesOccurrence(t *testing.T) {
	tests := []struct {
		name        string
		latitude    float64
		longitude   float64
		species     string
		expected    float64
		description string
	}{
		{
			name:        "location_not_set",
			latitude:    0,
			longitude:   0,
			species:     "Turdus_merula",
			expected:    0.0,
			description: "Should return 0 when location is not set",
		},
		{
			name:        "no_interpreter",
			latitude:    52.5200,
			longitude:   13.4050,
			species:     "Turdus_merula",
			expected:    0.0,
			description: "Should return 0 when range filter model is not loaded",
		},
		{
			name:        "unknown_species",
			latitude:    52.5200,
			longitude:   13.4050,
			species:     "Unknown_species",
			expected:    0.0,
			description: "Should return 0 for unknown species",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a fresh BirdNET instance for each subtest
			settings := &conf.Settings{
				BirdNET: conf.BirdNETConfig{
					Latitude:  tt.latitude,
					Longitude: tt.longitude,
					RangeFilter: conf.RangeFilterSettings{
						Threshold: 0.01,
					},
				},
			}

			bn := &BirdNET{
				Settings: settings,
			}

			occurrence := bn.GetSpeciesOccurrence(tt.species)
			assert.InDelta(t, tt.expected, occurrence, 0.001, tt.description)
		})
	}
}
