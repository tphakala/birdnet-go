package imageprovider

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// minExpected2022Species is the minimum expected species count in the
	// Avicommons 2022 taxonomy dataset, used as a sanity check.
	minExpected2022Species = 8000
)

// TestAvicommonsTaxonomy2022Alignment verifies that the Avicommons data uses
// taxonomy that aligns with BirdNET V2.4's 2021E taxonomy.
//
// Background: BirdNET uses 2021E taxonomy (e.g., "Corvus monedula" for Jackdaw),
// but newer Avicommons data uses updated taxonomy (e.g., "Coloeus monedula").
// This causes image lookups to fail for reclassified species.
//
// This test ensures the data file uses taxonomy compatible with BirdNET.
func TestAvicommonsTaxonomy2022Alignment(t *testing.T) {
	// Species that were reclassified between 2021 and 2024 taxonomy
	// These use the BirdNET V2.4 (2021E) scientific names
	testCases := []struct {
		scientificName string // BirdNET 2021E taxonomy
		commonName     string
		description    string
	}{
		{
			scientificName: "Corvus monedula",
			commonName:     "Eurasian Jackdaw",
			description:    "Jackdaw was moved from Corvus to Coloeus in newer taxonomy",
		},
		{
			scientificName: "Corvus dauuricus",
			commonName:     "Daurian Jackdaw",
			description:    "Daurian Jackdaw was moved from Corvus to Coloeus in newer taxonomy",
		},
	}

	// Create the Avicommons provider using the embedded data
	provider, err := NewAviCommonsProvider(os.DirFS("."), false)
	require.NoError(t, err, "failed to create AviCommons provider")

	for _, tc := range testCases {
		t.Run(tc.commonName, func(t *testing.T) {
			image, err := provider.Fetch(tc.scientificName)
			require.NoError(t, err, "failed to find image for %s (%s): %s",
				tc.scientificName, tc.commonName, tc.description)

			assert.NotEmpty(t, image.URL, "empty URL returned for %s (%s): %s",
				tc.scientificName, tc.commonName, tc.description)

			t.Logf("Found image for %s: %s", tc.scientificName, image.URL)
		})
	}
}

// TestAvicommonsDataFileVersion verifies the data file contains expected species count
// and is from the 2022 taxonomy version.
func TestAvicommonsDataFileVersion(t *testing.T) {
	provider, err := NewAviCommonsProvider(os.DirFS("."), false)
	require.NoError(t, err, "failed to create AviCommons provider")

	// The 2022 version should have ~9000+ species entries
	// This is a sanity check that we have a valid data file
	actualSpecies := len(provider.data)

	assert.GreaterOrEqual(t, actualSpecies, minExpected2022Species,
		"expected at least %d species in Avicommons data", minExpected2022Species)

	t.Logf("Avicommons data contains %d species entries", actualSpecies)
}
