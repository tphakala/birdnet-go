// species_test.go: Package api provides tests for species-related functions and endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateRarityStatus tests the calculateRarityStatus helper function.
func TestCalculateRarityStatus(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "rarity-calculation")

	tests := []struct {
		name     string
		score    float64
		expected RarityStatus
	}{
		// Very common (score > 0.8)
		{
			name:     "Very common - score 0.95",
			score:    0.95,
			expected: RarityVeryCommon,
		},
		{
			name:     "Very common - score 0.81",
			score:    0.81,
			expected: RarityVeryCommon,
		},
		{
			name:     "Very common - boundary exactly 0.80001",
			score:    0.80001,
			expected: RarityVeryCommon,
		},

		// Common (0.5 < score <= 0.8)
		{
			name:     "Common - score 0.8 (boundary)",
			score:    0.8, // Exactly at threshold
			expected: RarityCommon,
		},
		{
			name:     "Common - score 0.65",
			score:    0.65,
			expected: RarityCommon,
		},
		{
			name:     "Common - score 0.51",
			score:    0.51,
			expected: RarityCommon,
		},

		// Uncommon (0.2 < score <= 0.5)
		{
			name:     "Uncommon - score 0.5 (boundary)",
			score:    0.5,
			expected: RarityUncommon,
		},
		{
			name:     "Uncommon - score 0.35",
			score:    0.35,
			expected: RarityUncommon,
		},
		{
			name:     "Uncommon - score 0.21",
			score:    0.21,
			expected: RarityUncommon,
		},

		// Rare (0.05 < score <= 0.2)
		{
			name:     "Rare - score 0.2 (boundary)",
			score:    0.2,
			expected: RarityRare,
		},
		{
			name:     "Rare - score 0.1",
			score:    0.1,
			expected: RarityRare,
		},
		{
			name:     "Rare - score 0.051",
			score:    0.051,
			expected: RarityRare,
		},

		// Very rare (score <= 0.05)
		{
			name:     "Very rare - score 0.05 (boundary)",
			score:    0.05,
			expected: RarityVeryRare,
		},
		{
			name:     "Very rare - score 0.01",
			score:    0.01,
			expected: RarityVeryRare,
		},
		{
			name:     "Very rare - score 0",
			score:    0.0,
			expected: RarityVeryRare,
		},

		// Edge cases
		{
			name:     "Score exactly 1.0",
			score:    1.0,
			expected: RarityVeryCommon,
		},
		{
			name:     "Negative score",
			score:    -0.1,
			expected: RarityVeryRare,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := calculateRarityStatus(tt.score)
			assert.Equal(t, tt.expected, result, "Score %.4f should map to %s", tt.score, tt.expected)
		})
	}
}

// TestRarityStatusConstants tests that rarity status constants are correctly defined.
func TestRarityStatusConstants(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "rarity-constants")

	// Test status values
	assert.Equal(t, RarityVeryCommon, RarityStatus("very_common"))
	assert.Equal(t, RarityCommon, RarityStatus("common"))
	assert.Equal(t, RarityUncommon, RarityStatus("uncommon"))
	assert.Equal(t, RarityRare, RarityStatus("rare"))
	assert.Equal(t, RarityVeryRare, RarityStatus("very_rare"))
	assert.Equal(t, RarityUnknown, RarityStatus("unknown"))

	// Test threshold values
	assert.InDelta(t, 0.8, RarityThresholdVeryCommon, 0.001)
	assert.InDelta(t, 0.5, RarityThresholdCommon, 0.001)
	assert.InDelta(t, 0.2, RarityThresholdUncommon, 0.001)
	assert.InDelta(t, 0.05, RarityThresholdRare, 0.001)
}

// TestSpeciesAPIValidation tests validation for all species endpoints in a single table-driven test.
func TestSpeciesAPIValidation(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "api-validation")

	tests := []struct {
		name           string
		url            string
		paramNames     []string
		paramValues    []string
		handler        func(*Controller) func(echo.Context) error
		expectedStatus int
		expectedBody   string
	}{
		// Missing parameter tests
		{"GetSpeciesInfo missing param", "/api/v2/species", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Missing required parameter"},
		{"GetSpeciesTaxonomy missing param", "/api/v2/species/taxonomy", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Missing required parameter"},
		{"GetSpeciesThumbnail missing code", "/api/v2/species//thumbnail", []string{"code"}, []string{""},
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesThumbnail },
			http.StatusBadRequest, "Missing species code"},

		// Invalid format tests - GetSpeciesInfo
		{"GetSpeciesInfo too short", "/api/v2/species?scientific_name=Ab", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesInfo no space", "/api/v2/species?scientific_name=Turdusmigratorius", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesInfo single word", "/api/v2/species?scientific_name=Turdus", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},

		// Invalid format tests - GetSpeciesTaxonomy
		{"GetSpeciesTaxonomy too short", "/api/v2/species/taxonomy?scientific_name=Ab", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesTaxonomy no space", "/api/v2/species/taxonomy?scientific_name=Turdusmigratorius", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesTaxonomy single word", "/api/v2/species/taxonomy?scientific_name=Turdus", nil, nil,
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},

		// Error handling - nil processor
		{"GetSpeciesThumbnail nil processor", "/api/v2/species/amro/thumbnail", []string{"code"}, []string{"amro"},
			func(c *Controller) func(echo.Context) error { return c.GetSpeciesThumbnail },
			http.StatusServiceUnavailable, "BirdNET processor not available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, tt.url, http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			if tt.paramNames != nil {
				ctx.SetParamNames(tt.paramNames...)
				ctx.SetParamValues(tt.paramValues...)
			}

			c := newMinimalController()
			err := tt.handler(c)(ctx)

			require.NoError(t, err, tt.name)
			assert.Equal(t, tt.expectedStatus, rec.Code, tt.name)
			assert.Contains(t, rec.Body.String(), tt.expectedBody, tt.name)
		})
	}
}

// TestSpeciesInfoJSONSerialization tests that SpeciesInfo serializes correctly to JSON.
func TestSpeciesInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	info := SpeciesInfo{
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Rarity: &SpeciesRarityInfo{
			Status:           RarityCommon,
			Score:            0.65,
			LocationBased:    true,
			Latitude:         40.7128,
			Longitude:        -74.006,
			Date:             "2024-01-15",
			ThresholdApplied: 0.03,
		},
		Metadata: map[string]any{
			"source": "local",
		},
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	// Verify JSON structure
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Turdus migratorius", parsed["scientific_name"])
	assert.Equal(t, "American Robin", parsed["common_name"])
	assert.NotNil(t, parsed["rarity"])
	assert.NotNil(t, parsed["metadata"])

	// Verify rarity structure
	rarity, ok := parsed["rarity"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "common", rarity["status"])
	assert.InDelta(t, 0.65, rarity["score"].(float64), 0.001)
}

// TestSpeciesRarityInfoJSONSerialization tests that SpeciesRarityInfo serializes correctly.
func TestSpeciesRarityInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	info := SpeciesRarityInfo{
		Status:           RarityRare,
		Score:            0.08,
		LocationBased:    true,
		Latitude:         60.1699,
		Longitude:        24.9384,
		Date:             "2024-06-15",
		ThresholdApplied: 0.05,
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "rare", parsed["status"])
	assert.InDelta(t, 0.08, parsed["score"].(float64), 0.001)
	assert.Equal(t, true, parsed["location_based"])
	assert.InDelta(t, 60.1699, parsed["latitude"].(float64), 0.001)
	assert.InDelta(t, 24.9384, parsed["longitude"].(float64), 0.001)
	assert.Equal(t, "2024-06-15", parsed["date"])
	assert.InDelta(t, 0.05, parsed["threshold_applied"].(float64), 0.001)
}

// TestTaxonomyHierarchyJSONSerialization tests that TaxonomyHierarchy serializes correctly.
func TestTaxonomyHierarchyJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	hierarchy := TaxonomyHierarchy{
		Kingdom:       "Animalia",
		Phylum:        "Chordata",
		Class:         "Aves",
		Order:         "Passeriformes",
		Family:        "Turdidae",
		FamilyCommon:  "Thrushes and Allies",
		Genus:         "Turdus",
		Species:       "Turdus migratorius",
		SpeciesCommon: "American Robin",
	}

	data, err := json.Marshal(hierarchy)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Animalia", parsed["kingdom"])
	assert.Equal(t, "Chordata", parsed["phylum"])
	assert.Equal(t, "Aves", parsed["class"])
	assert.Equal(t, "Passeriformes", parsed["order"])
	assert.Equal(t, "Turdidae", parsed["family"])
	assert.Equal(t, "Thrushes and Allies", parsed["family_common"])
	assert.Equal(t, "Turdus", parsed["genus"])
	assert.Equal(t, "Turdus migratorius", parsed["species"])
	assert.Equal(t, "American Robin", parsed["species_common"])
}

// TestSubspeciesInfoJSONSerialization tests that SubspeciesInfo serializes correctly.
func TestSubspeciesInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	subspecies := SubspeciesInfo{
		ScientificName: "Turdus migratorius migratorius",
		CommonName:     "Eastern American Robin",
		Region:         "Eastern North America",
	}

	data, err := json.Marshal(subspecies)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Turdus migratorius migratorius", parsed["scientific_name"])
	assert.Equal(t, "Eastern American Robin", parsed["common_name"])
	assert.Equal(t, "Eastern North America", parsed["region"])
}

// TestTaxonomyInfoJSONSerialization tests that TaxonomyInfo serializes correctly.
func TestTaxonomyInfoJSONSerialization(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "json-serialization")

	info := TaxonomyInfo{
		ScientificName: "Turdus migratorius",
		SpeciesCode:    "amero",
		Taxonomy: &TaxonomyHierarchy{
			Kingdom: "Animalia",
			Phylum:  "Chordata",
			Class:   "Aves",
			Order:   "Passeriformes",
			Family:  "Turdidae",
			Genus:   "Turdus",
			Species: "Turdus migratorius",
		},
		Subspecies: []SubspeciesInfo{
			{ScientificName: "Turdus migratorius migratorius", CommonName: "Eastern Robin"},
		},
		Metadata: map[string]any{
			"source": "local",
		},
	}

	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, "Turdus migratorius", parsed["scientific_name"])
	assert.Equal(t, "amero", parsed["species_code"])
	assert.NotNil(t, parsed["taxonomy"])
	assert.NotNil(t, parsed["subspecies"])
	assert.NotNil(t, parsed["metadata"])

	// Verify subspecies array
	subspecies, ok := parsed["subspecies"].([]any)
	require.True(t, ok)
	assert.Len(t, subspecies, 1)
}
