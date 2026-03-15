package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestGetBaseConfidenceThreshold(t *testing.T) {
	tests := []struct {
		name           string
		globalThresh   float64
		speciesConfig  map[string]conf.SpeciesConfig
		commonName     string
		scientificName string
		expected       float64
		message        string
	}{
		{
			name:         "zero threshold falls through to global",
			globalThresh: 0.75,
			speciesConfig: map[string]conf.SpeciesConfig{
				"american robin": {Threshold: 0}, // actions-only, no custom threshold
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			expected:       0.75,
			message:        "Species with threshold:0 should fall through to global threshold, not return 0.0",
		},
		{
			name:         "explicit threshold used",
			globalThresh: 0.75,
			speciesConfig: map[string]conf.SpeciesConfig{
				"american robin": {Threshold: 0.90},
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			expected:       0.90,
			message:        "Species with explicit threshold should use that value",
		},
		{
			name:           "not in config uses global",
			globalThresh:   0.75,
			speciesConfig:  map[string]conf.SpeciesConfig{},
			commonName:     "Unknown Bird",
			scientificName: "Unknownus birdus",
			expected:       0.75,
			message:        "Species not in config should use global threshold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProcessor()
			p.Settings.BirdNET.Threshold = tt.globalThresh
			p.Settings.Realtime.Species = conf.SpeciesSettings{
				Config: tt.speciesConfig,
			}

			threshold := p.getBaseConfidenceThreshold(tt.commonName, tt.scientificName)

			assert.InDelta(t, tt.expected, float64(threshold), 0.001, tt.message)
		})
	}
}
