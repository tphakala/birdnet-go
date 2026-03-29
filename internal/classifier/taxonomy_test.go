package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemapLegacyCode verifies that retired eBird species codes are
// remapped to their current replacements.
func TestRemapLegacyCode(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-taxonomy")
	t.Attr("category", "remapping")

	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "legacy hergul remapped to amhgul1",
			code:     "hergul",
			expected: "amhgul1",
		},
		{
			name:     "non-legacy code unchanged",
			code:     "amerob",
			expected: "amerob",
		},
		{
			name:     "empty code unchanged",
			code:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := RemapLegacyCode(tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetSpeciesCodeFromName_LegacyRemapping verifies that looking up
// Herring Gull by name returns the remapped code "amhgul1" instead
// of the retired "hergul".
func TestGetSpeciesCodeFromName_LegacyRemapping(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-taxonomy")
	t.Attr("category", "remapping")

	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err, "failed to load embedded taxonomy data")

	// Herring Gull should return the remapped code
	code, found := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Larus argentatus_Herring Gull")
	require.True(t, found, "Herring Gull should exist in taxonomy")
	assert.Equal(t, "amhgul1", code, "legacy hergul should be remapped to amhgul1")

	// Also test with scientific-name-only format
	code2, found2 := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Larus argentatus (Herring Gull)")
	require.True(t, found2, "Herring Gull should be found with parenthetical format")
	assert.Equal(t, "amhgul1", code2, "legacy hergul should be remapped to amhgul1")
}

// TestGetSpeciesCodeFromName_NonLegacyUnchanged verifies that species
// without legacy remappings return their original codes.
func TestGetSpeciesCodeFromName_NonLegacyUnchanged(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-taxonomy")
	t.Attr("category", "remapping")

	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err, "failed to load embedded taxonomy data")

	// American Robin should return its normal code unchanged
	code, found := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Turdus migratorius_American Robin")
	require.True(t, found, "American Robin should exist in taxonomy")
	assert.Equal(t, "amerob", code, "non-legacy code should be returned unchanged")
}
