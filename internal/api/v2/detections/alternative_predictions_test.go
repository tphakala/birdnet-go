package detections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestBuildAlternativePredictionResponsesUsesResolverForScientificOnlyLabels(t *testing.T) {
	results := []datastore.Results{
		{Species: "Pseudacris crucifer", Confidence: 0.74},
		{Species: "Corvus brachyrhynchos", Confidence: 0.95},
	}

	alternatives := buildAlternativePredictionResponses(
		results,
		"Corvus brachyrhynchos",
		nil,
		map[string]string{},
		func(label string) detection.Species {
			if label == "Pseudacris crucifer" {
				return detection.Species{ScientificName: label, CommonName: "Spring Peeper"}
			}
			return detection.Species{ScientificName: label, CommonName: label}
		},
	)

	require.Len(t, alternatives, 1)
	assert.Equal(t, 2, alternatives[0].Rank)
	assert.Equal(t, "Pseudacris crucifer", alternatives[0].ScientificName)
	assert.Equal(t, "Spring Peeper", alternatives[0].CommonName)
	assert.InDelta(t, 0.74, alternatives[0].Confidence, 0.001)
}

func TestBuildAlternativePredictionResponsesRespectsLocationFilter(t *testing.T) {
	settings := &conf.Settings{}
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{
		"corvus brachyrhynchos": {},
		"melanerpes carolinus":  {},
	}
	results := []datastore.Results{
		{Species: "Melanerpes carolinus_Red-bellied Woodpecker_RBWO", Confidence: 0.86},
		{Species: "Cyanocitta cristata_Blue Jay_BLJA", Confidence: 0.84},
		{Species: "Corvus brachyrhynchos_American Crow_AMCRO", Confidence: 0.95},
	}

	alternatives := buildAlternativePredictionResponses(
		results,
		"Corvus brachyrhynchos",
		settings,
		nil,
		nil,
	)

	require.Len(t, alternatives, 1)
	assert.Equal(t, 2, alternatives[0].Rank)
	assert.Equal(t, "Melanerpes carolinus", alternatives[0].ScientificName)
	assert.Equal(t, "Red-bellied Woodpecker", alternatives[0].CommonName)
}

func TestBuildAlternativePredictionResponsesUsesConfiguredCommonNames(t *testing.T) {
	results := []datastore.Results{
		{Species: "Melanerpes carolinus", Confidence: 0.86},
		{Species: "Corvus brachyrhynchos", Confidence: 0.95},
		{Species: "Cyanocitta cristata", Confidence: 0.72},
	}
	commonNames := map[string]string{
		"Melanerpes carolinus": "Red-bellied Woodpecker",
		"Cyanocitta cristata":  "Blue Jay",
	}

	alternatives := buildAlternativePredictionResponses(
		results,
		"Corvus brachyrhynchos",
		nil,
		commonNames,
		nil,
	)

	require.Len(t, alternatives, 2)
	assert.Equal(t, "Melanerpes carolinus", alternatives[0].ScientificName)
	assert.Equal(t, "Red-bellied Woodpecker", alternatives[0].CommonName)
	assert.Equal(t, "Cyanocitta cristata", alternatives[1].ScientificName)
	assert.Equal(t, "Blue Jay", alternatives[1].CommonName)
}

func TestBuildAlternativePredictionResponsesExcludesAliasedPrimary(t *testing.T) {
	results := []datastore.Results{
		{Species: "Streptopelia senegalensis_Laughing Dove", Confidence: 0.91},
		{Species: "Turdus merula_Eurasian Blackbird", Confidence: 0.40},
	}

	alternatives := buildAlternativePredictionResponses(
		results,
		"Spilopelia senegalensis",
		nil,
		nil,
		nil,
	)

	require.Len(t, alternatives, 1)
	assert.Equal(t, "Turdus merula", alternatives[0].ScientificName)
}
