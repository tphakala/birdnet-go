// species_test.go: tests for species-related functions and endpoints in the
// api/v2 species domain package.

package species

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/api/v2/dto"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Species fixtures for the name-resolution and rarity tests. testAliasName and
// testCanonName are a real legacy/current synonym pair from the vendored OpenFauna
// alias map, so these tests exercise the actual alias data rather than a stub.
const (
	testSciName     = "Turdus migratorius"
	testCommonName  = "American Robin"
	testAliasName   = "Streptopelia senegalensis"
	testCanonName   = "Spilopelia senegalensis"
	testCanonCommon = "Laughing Dove"
)

// Two species BirdNET v2.4 ships separately while the OpenFauna alias map maps the
// second onto the first. Any match keyed only on the canonical name merges them.
const (
	collidingSciA    = "Dicrurus adsimilis"
	collidingCommonA = "Fork-tailed Drongo"
	collidingSciB    = "Dicrurus divaricatus"
	collidingCommonB = "Glossy-backed Drongo"
)

// newSpeciesHandler builds a minimal species Handler with valid default settings
// for validation tests. The injected facade dependencies (commonNameMap,
// serveImageProxy) are left nil because the validation paths exercised here never
// reach them.
func newSpeciesHandler() *Handler {
	h := &Handler{Core: &apicore.Core{}}
	h.Settings.Store(apitest.NewValidTestSettings())
	return h
}

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
		handler        func(*Handler) func(echo.Context) error
		expectedStatus int
		expectedBody   string
	}{
		// Missing parameter tests
		{"GetSpeciesInfo missing param", "/api/v2/species", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Missing required parameter"},
		{"GetSpeciesTaxonomy missing param", "/api/v2/species/taxonomy", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Missing required parameter"},
		{"GetSpeciesThumbnail missing code", "/api/v2/species//thumbnail", []string{"code"}, []string{""},
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesThumbnail },
			http.StatusBadRequest, "Missing species code"},

		// Invalid format tests - GetSpeciesInfo
		{"GetSpeciesInfo too short", "/api/v2/species?scientific_name=Ab", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesInfo no space", "/api/v2/species?scientific_name=Turdusmigratorius", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesInfo single word", "/api/v2/species?scientific_name=Turdus", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesInfo },
			http.StatusBadRequest, "Invalid scientific name format"},

		// Invalid format tests - GetSpeciesTaxonomy
		{"GetSpeciesTaxonomy too short", "/api/v2/species/taxonomy?scientific_name=Ab", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesTaxonomy no space", "/api/v2/species/taxonomy?scientific_name=Turdusmigratorius", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},
		{"GetSpeciesTaxonomy single word", "/api/v2/species/taxonomy?scientific_name=Turdus", nil, nil,
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesTaxonomy },
			http.StatusBadRequest, "Invalid scientific name format"},

		// Error handling - nil processor
		{"GetSpeciesThumbnail nil processor", "/api/v2/species/amro/thumbnail", []string{"code"}, []string{"amro"},
			func(c *Handler) func(echo.Context) error { return c.GetSpeciesThumbnail },
			http.StatusServiceUnavailable, "BirdNET service unavailable"},
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

			c := newSpeciesHandler()
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
		Rarity: SpeciesRarityInfo{
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
		Taxonomy: TaxonomyHierarchy{
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

// TestGetAllSpecies_LocalizedSecondaryModel verifies that GetAllSpecies serves
// secondary-model species (a scientific-only bat label) with their localized
// common name, sourced from the injected scientific-to-common name map rather
// than the raw primary BirdNET.Labels.
//
// In production the name map is seeded by control_monitor (UpdateCommonNameMap
// with the orchestrator's AllLabels union, localized through the batch resolver);
// that construction lives in the facade's name-map plumbing, not the species
// domain. Here the already-localized map is injected directly so the test
// isolates the species handler's own behavior: building the list from the cached
// map, carrying the localized common name, and sorting by scientific name.
func TestGetAllSpecies_LocalizedSecondaryModel(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("feature", "localized-name-resolution")

	e := echo.New()
	handler := &Handler{
		Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")},
		commonNameMap: func() map[string]string {
			return map[string]string{
				"Barbastella barbastellus": "mopsilepakko", // localized bat name (secondary model)
				"Parus major":              "Great Tit",
			}
		},
	}
	handler.Settings.Store(&conf.Settings{})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/all", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, handler.GetAllSpecies(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AllSpeciesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

	byScientific := make(map[string]dto.RangeFilterSpecies, len(response.Species))
	for _, s := range response.Species {
		byScientific[s.ScientificName] = s
	}

	bat, ok := byScientific["Barbastella barbastellus"]
	require.True(t, ok, "secondary-model bat species must be present in /species/all")
	assert.Equal(t, "mopsilepakko", bat.CommonName, "bat must carry its localized common name")

	tit, ok := byScientific["Parus major"]
	require.True(t, ok, "primary species must still be present")
	assert.Equal(t, "Great Tit", tit.CommonName)

	// Response must be deterministically sorted by scientific name.
	require.Len(t, response.Species, 2)
	assert.Equal(t, "Barbastella barbastellus", response.Species[0].ScientificName, "response must be sorted by scientific name")
	assert.Equal(t, "Parus major", response.Species[1].ScientificName)

	assert.Equal(t, len(response.Species), response.Count)
}

// TestGetAllSpecies tests the GetAllSpecies endpoint returns all BirdNET labels
// via the fallback path (empty cached name map; labels read from settings).
func TestGetAllSpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species")
	t.Attr("type", "unit")
	t.Attr("feature", "all-species-list")

	tests := []struct {
		name           string
		labels         []string
		expectedCount  int
		expectedStatus int
	}{
		{
			name:           "returns all labels",
			labels:         []string{"Turdus migratorius_American Robin", "Cyanocitta cristata_Blue Jay", "Corvus brachyrhynchos_American Crow"},
			expectedCount:  3,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "empty labels",
			labels:         []string{},
			expectedCount:  0,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "nil labels",
			labels:         nil,
			expectedCount:  0,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			settings := &conf.Settings{}
			settings.BirdNET.Labels = tt.labels

			// Empty injected name map forces the fallback path that parses
			// allModelLabels() (which reads settings.BirdNET.Labels here, since
			// Processor is nil).
			handler := &Handler{
				Core:          &apicore.Core{Echo: e, Group: e.Group("/api/v2")},
				commonNameMap: func() map[string]string { return nil },
			}
			handler.Settings.Store(settings)

			req := httptest.NewRequest(http.MethodGet, "/api/v2/species/all", http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			err := handler.GetAllSpecies(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, rec.Code)

			var response AllSpeciesResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Equal(t, tt.expectedCount, response.Count)
			require.Len(t, response.Species, tt.expectedCount)

			if tt.expectedCount > 0 {
				// Verify first species is parsed correctly
				assert.Equal(t, "Turdus migratorius_American Robin", response.Species[0].Label)
				assert.Equal(t, "Turdus migratorius", response.Species[0].ScientificName)
				assert.Equal(t, "American Robin", response.Species[0].CommonName)

				// Verify order is preserved
				assert.Equal(t, "Cyanocitta cristata_Blue Jay", response.Species[1].Label)
				assert.Equal(t, "Cyanocitta cristata", response.Species[1].ScientificName)
				assert.Equal(t, "Blue Jay", response.Species[1].CommonName)
			}
		})
	}
}

func TestResolveSpeciesLabel(t *testing.T) {
	t.Parallel()

	allLabels := []string{
		testCanonName + "_" + testCanonCommon,
		testSciName + "_" + testCommonName,
	}

	tests := []struct {
		name       string
		targetSci  string
		wantLabel  string
		wantCommon string
	}{
		{
			name:       "exact match",
			targetSci:  testSciName,
			wantLabel:  testSciName + "_" + testCommonName,
			wantCommon: testCommonName,
		},
		{
			name:       "taxonomic alias",
			targetSci:  testAliasName,
			wantLabel:  testCanonName + "_" + testCanonCommon,
			wantCommon: testCanonCommon,
		},
		{
			name:       "case mismatch match",
			targetSci:  " tURdus MIgratoRIUS ",
			wantLabel:  testSciName + "_" + testCommonName,
			wantCommon: testCommonName,
		},
		{
			name:       "not found",
			targetSci:  "Unknown species",
			wantLabel:  "",
			wantCommon: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLabel, gotCommon := resolveSpeciesLabel(tt.targetSci, allLabels)
			assert.Equal(t, tt.wantLabel, gotLabel)
			assert.Equal(t, tt.wantCommon, gotCommon)
		})
	}
}

func TestResolveSpeciesLabel_Empty(t *testing.T) {
	t.Parallel()
	gotLabel, gotCommon := resolveSpeciesLabel("Turdus migratorius", nil)
	assert.Empty(t, gotLabel)
	assert.Empty(t, gotCommon)
}

func TestComputeRarity(t *testing.T) {
	t.Parallel()

	geomodelLabels := []string{
		testCanonName + "_" + testCanonCommon,
		testSciName + "_" + testCommonName,
		"Universal GeomodelOnly_Species",
	}
	classifierLabels := []string{
		testCanonName + "_" + testCanonCommon,
		testSciName + "_" + testCommonName,
	}

	scores := []classifier.SpeciesScore{
		{Label: testSciName + "_" + testCommonName, Score: 0.9},
		{Label: testCanonName + "_" + testCanonCommon, Score: 0.1},
	}

	tests := []struct {
		name       string
		targetSci  string
		wantScore  float64
		wantStatus RarityStatus
	}{
		{
			name:       "found in scores (very common)",
			targetSci:  testSciName,
			wantScore:  0.9,
			wantStatus: RarityVeryCommon,
		},
		{
			name:       "found in scores via alias",
			targetSci:  testAliasName,
			wantScore:  0.1,
			wantStatus: RarityRare,
		},
		{
			name:       "case mismatch in alias",
			targetSci:  " STreptoPeliA SenEgaLenSiS ",
			wantScore:  0.1,
			wantStatus: RarityRare,
		},
		{
			name:       "universal geomodel only (very rare)",
			targetSci:  "Universal GeomodelOnly",
			wantScore:  0.0,
			wantStatus: RarityVeryRare,
		},
		{
			name:       "unknown species (no coverage)",
			targetSci:  "Completely Unknown",
			wantScore:  0.0,
			wantStatus: RarityUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotScore, gotStatus := computeRarity(tt.targetSci, scores, geomodelLabels, classifierLabels)
			assert.InDelta(t, tt.wantScore, gotScore, 0.001)
			assert.Equal(t, tt.wantStatus, gotStatus)
		})
	}
}

func TestComputeRarity_Empty(t *testing.T) {
	t.Parallel()
	gotScore, gotStatus := computeRarity("Turdus migratorius", nil, nil, nil)
	assert.InDelta(t, 0.0, gotScore, 0.001)
	assert.Equal(t, RarityUnknown, gotStatus)
}

// TestComputeRarity_GeomodelLabelsTakePrecedence pins the reported bug. Coverage is
// decided by the geomodel's vocabulary, so a species the classifier can name but the
// geomodel cannot score has no occurrence probability and must report unknown rather
// than a misleading "very rare".
func TestComputeRarity_GeomodelLabelsTakePrecedence(t *testing.T) {
	t.Parallel()

	geomodelLabels := []string{testCanonName + "_" + testCanonCommon}
	classifierLabels := []string{
		testCanonName + "_" + testCanonCommon,
		testSciName + "_" + testCommonName,
	}

	_, status := computeRarity(testSciName, nil, geomodelLabels, classifierLabels)
	assert.Equal(t, RarityUnknown, status,
		"classifier-only species has no geomodel occurrence probability")

	_, status = computeRarity(testCanonName, nil, geomodelLabels, classifierLabels)
	assert.Equal(t, RarityVeryRare, status,
		"geomodel-covered species below threshold is very rare")
}

// TestComputeRarity_NoGeomodelLabels covers the range-filter backends whose vocabulary
// is the classifier's own label set: the TFLite meta model, the plain ONNX range
// filter, and an unconfigured location. GetRarityContext reports no geomodel vocabulary
// for those, so coverage must fall back to the classifier labels instead of reporting
// every species as unknown.
func TestComputeRarity_NoGeomodelLabels(t *testing.T) {
	t.Parallel()

	classifierLabels := []string{
		testCanonName + "_" + testCanonCommon,
		testSciName + "_" + testCommonName,
	}

	tests := []struct {
		name       string
		targetSci  string
		wantStatus RarityStatus
	}{
		{
			name:       "classifier species below threshold is very rare",
			targetSci:  testSciName,
			wantStatus: RarityVeryRare,
		},
		{
			name:       "legacy synonym of a classifier species is still covered",
			targetSci:  testAliasName,
			wantStatus: RarityVeryRare,
		},
		{
			name:       "species outside the classifier has no coverage",
			targetSci:  "Myotis brandtii",
			wantStatus: RarityUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotScore, gotStatus := computeRarity(tt.targetSci, nil, nil, classifierLabels)
			assert.InDelta(t, 0.0, gotScore, 0.001)
			assert.Equal(t, tt.wantStatus, gotStatus)
		})
	}
}

// TestResolveSpeciesLabel_CollidingSpecies guards a defect an earlier revision shipped:
// matching on the canonical name alone answered a request for one of these two species
// with the other's label and common name, because the alias map merges them while
// BirdNET v2.4 ships both.
func TestResolveSpeciesLabel_CollidingSpecies(t *testing.T) {
	t.Parallel()

	allLabels := []string{
		collidingSciA + "_" + collidingCommonA,
		collidingSciB + "_" + collidingCommonB,
	}

	gotLabel, gotCommon := resolveSpeciesLabel(collidingSciB, allLabels)
	assert.Equal(t, collidingSciB+"_"+collidingCommonB, gotLabel,
		"an exact scientific-name match must win over the alias collapse")
	assert.Equal(t, collidingCommonB, gotCommon)

	gotLabel, gotCommon = resolveSpeciesLabel(collidingSciA, allLabels)
	assert.Equal(t, collidingSciA+"_"+collidingCommonA, gotLabel)
	assert.Equal(t, collidingCommonA, gotCommon)
}

// TestResolveSpeciesLabel_LegacyLabelCanonicalTarget covers the production-relevant
// direction the other alias cases miss: the LABEL carries the legacy synonym (as BirdNET
// v2.4's own labels do) while the request uses the current name.
func TestResolveSpeciesLabel_LegacyLabelCanonicalTarget(t *testing.T) {
	t.Parallel()

	allLabels := []string{testAliasName + "_" + testCanonCommon}

	gotLabel, gotCommon := resolveSpeciesLabel(testCanonName, allLabels)
	assert.Equal(t, testAliasName+"_"+testCanonCommon, gotLabel,
		"canonicalization must apply to the label side, not only the request side")
	assert.Equal(t, testCanonCommon, gotCommon)
}

// TestComputeRarity_CollidingSpecies is the rarity-side counterpart to
// TestResolveSpeciesLabel_CollidingSpecies: each species must report its own score.
func TestComputeRarity_CollidingSpecies(t *testing.T) {
	t.Parallel()

	labels := []string{
		collidingSciA + "_" + collidingCommonA,
		collidingSciB + "_" + collidingCommonB,
	}
	// Descending by score, as the probable-species list arrives.
	scores := []classifier.SpeciesScore{
		{Label: collidingSciA + "_" + collidingCommonA, Score: 0.9},
		{Label: collidingSciB + "_" + collidingCommonB, Score: 0.1},
	}

	gotScore, gotStatus := computeRarity(collidingSciB, scores, labels, labels)
	assert.InDelta(t, 0.1, gotScore, 0.001, "the merged species must keep its own score")
	assert.Equal(t, RarityRare, gotStatus)

	gotScore, gotStatus = computeRarity(collidingSciA, scores, labels, labels)
	assert.InDelta(t, 0.9, gotScore, 0.001)
	assert.Equal(t, RarityVeryCommon, gotStatus)
}

// TestComputeRarity_SyntheticScoresReportUnknown pins that a score injected for a species
// OUTSIDE the coverage vocabulary is not read as a rarity, whatever its value.
// PassUnmappedSpecies injects unmapped species at 0.0, which is the case this fully
// covers: previously they read as very_rare, so the badge depended on an unrelated
// toggle.
//
// Note what this does NOT cover. addUserOverrideSpeciesScores also injects at 1.0, but
// resolveOverrideLabels resolves an override against the geomodel labels first, so a
// force-included species the geomodel knows sits INSIDE the coverage vocabulary and
// still reads as very_common. Only an override outside it, as constructed here, reaches
// the unknown path. Separating a real score from an injected one needs the range filter
// to tag synthetic entries.
func TestComputeRarity_SyntheticScoresReportUnknown(t *testing.T) {
	t.Parallel()

	const unmappedSci = "Myotis brandtii"
	geomodelLabels := []string{testCanonName + "_" + testCanonCommon}
	// A species present in neither vocabulary, standing in for one the range filter
	// cannot rank. classifierLabels is the primary model's own set in production.
	classifierLabels := []string{testSciName + "_" + testCommonName}

	tests := []struct {
		name  string
		score float64
		why   string
	}{
		{name: "unmapped species injected at 0.0", score: 0.0, why: "must not read as very_rare"},
		{name: "uncovered override injected at 1.0", score: 1.0, why: "must not read as very_common"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scores := []classifier.SpeciesScore{{Label: unmappedSci + "_Brandt's Bat", Score: tt.score}}
			gotScore, gotStatus := computeRarity(unmappedSci, scores, geomodelLabels, classifierLabels)
			assert.Equal(t, RarityUnknown, gotStatus, tt.why)
			assert.InDelta(t, 0.0, gotScore, 0.001)
		})
	}
}

// TestSpeciesInfoJSONOmitsUnsetBlocks pins the omitzero half of the wire contract. The
// populated direction is covered by TestSpeciesInfoJSONSerialization; without this,
// reverting omitzero to omitempty would go unnoticed and start emitting zero-valued
// "rarity" and "taxonomy" objects on every response where the lookup was skipped,
// breaking clients that gate on the key's presence.
func TestSpeciesInfoJSONOmitsUnsetBlocks(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(SpeciesInfo{ScientificName: testSciName, CommonName: testCommonName})
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.NotContains(t, parsed, "rarity", "an unset rarity block must be omitted, not emitted as zeros")
	assert.NotContains(t, parsed, "taxonomy", "an unset taxonomy block must be omitted, not emitted as zeros")
	assert.Contains(t, parsed, "scientific_name")
}

// TestTaxonomyInfoJSONOmitsUnsetHierarchy is the TaxonomyInfo counterpart.
func TestTaxonomyInfoJSONOmitsUnsetHierarchy(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(TaxonomyInfo{ScientificName: testSciName})
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.NotContains(t, parsed, "taxonomy", "an unset hierarchy must be omitted, not emitted as zeros")
}
