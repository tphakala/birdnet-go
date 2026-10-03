package conf

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRarityFilterSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings RarityFilterSettings
		wantErr  bool
		errType  string
	}{
		{
			name:     "disabled with no bands passes",
			settings: RarityFilterSettings{Enabled: false},
		},
		{
			name:     "disabled with invalid bands passes",
			settings: RarityFilterSettings{Enabled: false, Bands: []RarityBand{{MaxOccurrence: 2, MinDetections: 0}}},
		},
		{
			name: "valid bands pass",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{
				{MaxOccurrence: 0.1, MinDetections: 3},
				{MaxOccurrence: 1, MinDetections: 2},
			}},
		},
		{
			name:     "enabled without bands rejected",
			settings: RarityFilterSettings{Enabled: true},
			wantErr:  true,
			errType:  "rarity-filter-bands",
		},
		{
			name:     "zero max occurrence rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{{MaxOccurrence: 0, MinDetections: 2}}},
			wantErr:  true,
			errType:  "rarity-filter-max-occurrence",
		},
		{
			name:     "max occurrence above 1 rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{{MaxOccurrence: 1.01, MinDetections: 2}}},
			wantErr:  true,
			errType:  "rarity-filter-max-occurrence",
		},
		{
			name:     "NaN max occurrence rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{{MaxOccurrence: math.NaN(), MinDetections: 2}}},
			wantErr:  true,
			errType:  "rarity-filter-max-occurrence",
		},
		{
			name:     "min detections below minimum rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{{MaxOccurrence: 0.5, MinDetections: 0}}},
			wantErr:  true,
			errType:  "rarity-filter-min-detections",
		},
		{
			name: "duplicate max occurrence rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{
				{MaxOccurrence: 0.5, MinDetections: 2},
				{MaxOccurrence: 0.5, MinDetections: 4},
			}},
			wantErr: true,
			errType: "rarity-filter-duplicate-max-occurrence",
		},
		{
			name:     "too many bands rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: distinctBands(MaxRarityBands + 1)},
			wantErr:  true,
			errType:  "rarity-filter-band-count",
		},
		{
			name:     "maximum band count passes",
			settings: RarityFilterSettings{Enabled: true, Bands: distinctBands(MaxRarityBands)},
		},
		{
			name: "min detections above maximum rejected",
			settings: RarityFilterSettings{Enabled: true, Bands: []RarityBand{
				{MaxOccurrence: 0.5, MinDetections: MaxRarityBandDetections + 1},
			}},
			wantErr: true,
			errType: "rarity-filter-min-detections",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateRarityFilterSettings(&tt.settings)

			if tt.wantErr {
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRarityFilterSettings_SortsBands(t *testing.T) {
	t.Parallel()

	settings := RarityFilterSettings{Enabled: true, Bands: []RarityBand{
		{MaxOccurrence: 0.9, MinDetections: 2},
		{MaxOccurrence: 0.1, MinDetections: 3},
		{MaxOccurrence: 0.5, MinDetections: 4},
	}}

	require.NoError(t, validateRarityFilterSettings(&settings))
	assert.Equal(t, []RarityBand{
		{MaxOccurrence: 0.1, MinDetections: 3},
		{MaxOccurrence: 0.5, MinDetections: 4},
		{MaxOccurrence: 0.9, MinDetections: 2},
	}, settings.Bands)
}

// distinctBands returns n valid bands with distinct, ascending limits in (0, 1].
func distinctBands(n int) []RarityBand {
	bands := make([]RarityBand, 0, n)
	for i := range n {
		bands = append(bands, RarityBand{MaxOccurrence: float64(i+1) / float64(n), MinDetections: 2})
	}
	return bands
}

func TestNormalizeRealtimeFeatures_DisablesRarityFilterWithoutBands(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.RarityFilter = RarityFilterSettings{Enabled: true, Bands: []RarityBand{}}

	normalizeIncompleteFeatures(s)

	assert.False(t, s.Realtime.RarityFilter.Enabled, "an enabled filter with no bands must be switched off on load")
	assert.Contains(t, warningsText(s), "rarity filter is enabled but has no bands")
	require.NoError(t, ValidateSettings(s))
}

func TestNormalizeRealtimeFeatures_KeepsConfiguredRarityFilter(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.RarityFilter = RarityFilterSettings{Enabled: true, Bands: []RarityBand{
		{MaxOccurrence: DefaultRarityRareMaxOccurrence, MinDetections: DefaultRarityRareMinDetections},
	}}

	normalizeIncompleteFeatures(s)

	assert.True(t, s.Realtime.RarityFilter.Enabled)
	assert.NotContains(t, warningsText(s), "rarity filter")
}
