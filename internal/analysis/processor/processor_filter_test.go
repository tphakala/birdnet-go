package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestShouldApplyRangeFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		modelID          string
		rangeFilterModel string
		expected         bool
	}{
		{
			name:             "BirdNET V2.4 always filtered",
			modelID:          "BirdNET_V2.4",
			rangeFilterModel: "",
			expected:         true,
		},
		{
			name:             "BirdNET V2.4 filtered with legacy range model",
			modelID:          "BirdNET_V2.4",
			rangeFilterModel: "legacy",
			expected:         true,
		},
		{
			name:             "BirdNET V2.4 filtered with v3 range model",
			modelID:          "BirdNET_V2.4",
			rangeFilterModel: "v3",
			expected:         true,
		},
		{
			name:             "BirdNET V3.0 always filtered",
			modelID:          "BirdNET_V3.0",
			rangeFilterModel: "",
			expected:         true,
		},
		{
			name:             "BirdNET V3.0 filtered with v3 range model",
			modelID:          "BirdNET_V3.0",
			rangeFilterModel: "v3",
			expected:         true,
		},
		{
			name:             "Perch V2 filtered when v3 geomodel active",
			modelID:          "Perch_V2",
			rangeFilterModel: "v3",
			expected:         true,
		},
		{
			name:             "Perch V2 not filtered when range model empty",
			modelID:          "Perch_V2",
			rangeFilterModel: "",
			expected:         false,
		},
		{
			name:             "Perch V2 not filtered when legacy range model",
			modelID:          "Perch_V2",
			rangeFilterModel: "legacy",
			expected:         false,
		},
		{
			name:             "Bat never filtered",
			modelID:          "Bat",
			rangeFilterModel: "v3",
			expected:         false,
		},
		{
			name:             "BSG never filtered",
			modelID:          "BSG",
			rangeFilterModel: "v3",
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.Settings{}
			settings.BirdNET.RangeFilter.Model = tt.rangeFilterModel

			result := shouldApplyRangeFilter(tt.modelID, settings)
			assert.Equal(t, tt.expected, result)
		})
	}
}
