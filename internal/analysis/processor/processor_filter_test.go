package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestShouldApplyRangeFilter(t *testing.T) {
	t.Parallel()

	assert.False(t, shouldApplyRangeFilter("Perch_V2", nil))

	tests := []struct {
		name               string
		modelID            string
		locationConfigured bool
		expected           bool
	}{
		{
			name:               "BirdNET V2.4 filtered when location configured",
			modelID:            "BirdNET_V2.4",
			locationConfigured: true,
			expected:           true,
		},
		{
			name:               "BirdNET V2.4 not filtered when location not configured",
			modelID:            "BirdNET_V2.4",
			locationConfigured: false,
			expected:           false,
		},
		{
			name:               "BirdNET V3.0 filtered when location configured",
			modelID:            "BirdNET_V3.0",
			locationConfigured: true,
			expected:           true,
		},
		{
			name:               "Perch V2 filtered when location configured",
			modelID:            "Perch_V2",
			locationConfigured: true,
			expected:           true,
		},
		{
			name:               "Perch V2 not filtered when location not configured",
			modelID:            "Perch_V2",
			locationConfigured: false,
			expected:           false,
		},
		{
			name:               "Bat never filtered",
			modelID:            "Bat",
			locationConfigured: true,
			expected:           false,
		},
		{
			name:               "BSG never filtered",
			modelID:            "BSG",
			locationConfigured: true,
			expected:           false,
		},
		{
			name:               "unknown model ID defaults to BirdNET via DetectionModelInfoForID",
			modelID:            "SomeUnknownModel",
			locationConfigured: true,
			expected:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.Settings{}
			settings.BirdNET.LocationConfigured = tt.locationConfigured

			result := shouldApplyRangeFilter(tt.modelID, settings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldFilterDetection_AppliesRangeFilterToPerch(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Model = "latest"
	settings.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{
		"turdus migratorius": {},
	}

	p := &Processor{}
	tests := []struct {
		name           string
		result         datastore.Results
		commonName     string
		scientificName string
		speciesLower   string
		expectedFilter bool
	}{
		{
			name: "filters out-of-range Perch species",
			result: datastore.Results{
				Species:    "Pitangus sulphuratus",
				Confidence: 0.95,
			},
			commonName:     "Great Kiskadee",
			scientificName: "Pitangus sulphuratus",
			speciesLower:   "great kiskadee",
			expectedFilter: true,
		},
		{
			name: "allows in-range Perch species",
			result: datastore.Results{
				Species:    "Turdus migratorius",
				Confidence: 0.95,
			},
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			speciesLower:   "american robin",
			expectedFilter: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			shouldFilter, threshold := p.shouldFilterDetection(
				settings,
				tt.result,
				tt.commonName,
				tt.scientificName,
				tt.speciesLower,
				0.7,
				"Backyard",
				"Perch_V2",
			)

			assert.Equal(t, tt.expectedFilter, shouldFilter)
			assert.InDelta(t, 0.7, threshold, 0.001)
		})
	}
}
