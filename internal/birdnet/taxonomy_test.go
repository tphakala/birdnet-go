package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadTaxonomyData_EmbeddedDefaults(t *testing.T) {
	t.Parallel()

	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err, "loading embedded taxonomy data should not error")
	require.NotEmpty(t, taxonomyMap, "taxonomy map should not be empty")
	require.NotEmpty(t, scientificIndex, "scientific name index should not be empty")
}

func TestGetSpeciesCodeFromName(t *testing.T) {
	t.Parallel()

	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err)

	tests := []struct {
		name         string
		speciesName  string
		expectedCode string
		expectFound  bool
	}{
		{
			name:         "common blackbird by label",
			speciesName:  "Turdus merula_Common Blackbird",
			expectedCode: "eurbla",
			expectFound:  true,
		},
		{
			name:         "american robin by label",
			speciesName:  "Turdus migratorius_American Robin",
			expectedCode: "amerob",
			expectFound:  true,
		},
		{
			name:         "herring gull returns current eBird code",
			speciesName:  "Larus argentatus_Herring Gull",
			expectedCode: "euhgul1",
			expectFound:  true,
		},
		{
			name:         "herring gull by scientific name in parens format",
			speciesName:  "Larus argentatus (Herring Gull)",
			expectedCode: "euhgul1",
			expectFound:  true,
		},
		{
			name:        "unknown species returns placeholder",
			speciesName: "Fakeus speciesus_Fake Bird",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, found := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, tt.speciesName)

			assert.Equal(t, tt.expectFound, found, "found mismatch for %s", tt.speciesName)
			if tt.expectFound {
				assert.Equal(t, tt.expectedCode, code, "code mismatch for %s", tt.speciesName)
			} else {
				assert.NotEmpty(t, code, "placeholder code should not be empty")
			}
		})
	}
}

func TestHerringGullCodeNotRetired(t *testing.T) {
	t.Parallel()

	// Regression test: eBird retired the "hergul" code after the
	// American/European Herring Gull taxonomic split. The embedded taxonomy
	// must use the current code "euhgul1" for Larus argentatus (European
	// Herring Gull), not the retired "hergul".
	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err)

	// Verify the retired code "hergul" is NOT in the taxonomy
	_, hasRetiredCode := taxonomyMap["hergul"]
	assert.False(t, hasRetiredCode, "retired eBird code 'hergul' should not be in taxonomy")

	// Verify the current code "euhgul1" IS in the taxonomy
	speciesName, hasCurrentCode := taxonomyMap["euhgul1"]
	assert.True(t, hasCurrentCode, "current eBird code 'euhgul1' should be in taxonomy")
	assert.Equal(t, "Larus argentatus_Herring Gull", speciesName)

	// Verify lookup by species label returns the correct code
	code, found := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Larus argentatus_Herring Gull")
	assert.True(t, found, "Herring Gull should be found in taxonomy")
	assert.Equal(t, "euhgul1", code, "Herring Gull should map to 'euhgul1', not the retired 'hergul'")
}

func TestGetSpeciesNameFromCode(t *testing.T) {
	t.Parallel()

	taxonomyMap, _, err := LoadTaxonomyData("")
	require.NoError(t, err)

	tests := []struct {
		name         string
		code         string
		expectedName string
		expectFound  bool
	}{
		{
			name:         "valid code returns species name",
			code:         "amerob",
			expectedName: "Turdus migratorius_American Robin",
			expectFound:  true,
		},
		{
			name:         "euhgul1 returns Herring Gull",
			code:         "euhgul1",
			expectedName: "Larus argentatus_Herring Gull",
			expectFound:  true,
		},
		{
			name:        "retired hergul code returns nothing",
			code:        "hergul",
			expectFound: false,
		},
		{
			name:        "nonexistent code returns nothing",
			code:        "zzzzz99",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name, found := GetSpeciesNameFromCode(taxonomyMap, tt.code)

			assert.Equal(t, tt.expectFound, found)
			if tt.expectFound {
				assert.Equal(t, tt.expectedName, name)
			}
		})
	}
}

func TestSplitSpeciesName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		input              string
		expectedScientific string
		expectedCommon     string
	}{
		{
			name:               "standard format",
			input:              "Larus argentatus_Herring Gull",
			expectedScientific: "Larus argentatus",
			expectedCommon:     "Herring Gull",
		},
		{
			name:               "empty string",
			input:              "",
			expectedScientific: "",
			expectedCommon:     "",
		},
		{
			name:               "single word",
			input:              "Unknown",
			expectedScientific: "Unknown",
			expectedCommon:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scientific, common := SplitSpeciesName(tt.input)
			assert.Equal(t, tt.expectedScientific, scientific)
			assert.Equal(t, tt.expectedCommon, common)
		})
	}
}
