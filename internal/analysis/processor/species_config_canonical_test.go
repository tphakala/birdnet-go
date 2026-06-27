package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestIsSpeciesExcluded_CanonicalAliasMatch verifies that an exclude entry keyed on
// a legacy scientific name still matches once detections carry the canonical name
// (and vice versa), because both sides are canonicalized before comparison.
func TestIsSpeciesExcluded_CanonicalAliasMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		commonName     string
		scientificName string
		excludeList    []string
		want           bool
	}{
		{
			name:           "canonical detection matches legacy exclude entry",
			commonName:     "Laughing Dove",
			scientificName: "Spilopelia senegalensis", // canonical (stored after ingestion)
			excludeList:    []string{"Streptopelia senegalensis"},
			want:           true,
		},
		{
			name:           "legacy detection matches canonical exclude entry",
			commonName:     "Laughing Dove",
			scientificName: "Streptopelia senegalensis",
			excludeList:    []string{"Spilopelia senegalensis"},
			want:           true,
		},
		{
			name:           "unrelated species not excluded by alias",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"Streptopelia senegalensis"},
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSpeciesExcluded(tt.commonName, tt.scientificName, tt.excludeList))
		})
	}
}

// TestLookupSpeciesConfig_CanonicalAliasMatch verifies that a per-species config
// entry keyed on a legacy scientific name matches a detection that now carries the
// canonical scientific name.
func TestLookupSpeciesConfig_CanonicalAliasMatch(t *testing.T) {
	t.Parallel()

	cfg := map[string]conf.SpeciesConfig{
		"streptopelia senegalensis": {Threshold: 0.42}, // user keyed config on the legacy name
	}

	got, found := lookupSpeciesConfig(cfg, "Laughing Dove", "Spilopelia senegalensis")
	assert.True(t, found, "config keyed on a legacy name must match the canonical detection")
	assert.InDelta(t, 0.42, got.Threshold, 1e-9)
}
