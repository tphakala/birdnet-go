package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// analyticsBatchFakeResolver is a test resolver that satisfies SpeciesNameResolver
// and the optional batchLocalizer interface. It is distinct from the fakeResolver in
// insights_nameresolver_test.go, which covers the forward (sci->common) path only.
// Here the batch capability lets scientific-only labels (no embedded common name)
// reach the commonToSci reverse map and become resolvable. Shared by the facade
// name-map and exclude-list canonicalization tests.
type analyticsBatchFakeResolver struct{ batch map[string]string }

func (a *analyticsBatchFakeResolver) Resolve(string, string) string      { return "" }
func (a *analyticsBatchFakeResolver) ResolveLocal(string) (string, bool) { return "", false }
func (a *analyticsBatchFakeResolver) ResolveLocalizedBatch(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v, ok := a.batch[n]; ok {
			out[n] = v
		}
	}
	return out
}

// TestUpdateCommonNameMap_PopulatesAllMaps verifies that UpdateCommonNameMap
// populates the display, folded-search, and exact-resolution maps from the same
// label input, keeping them consistent.
func TestUpdateCommonNameMap_PopulatesAllMaps(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := &Controller{Core: &apicore.Core{Group: e.Group("/api/v2")}}

	labels := []string{
		"Strix aluco_Tawny Owl",
		"Parus major_Great Tit",
	}
	c.UpdateCommonNameMap(labels)

	// Verify the scientific-to-common map (used by insights endpoints).
	sciToCommon := c.loadCommonNameMap()
	require.NotNil(t, sciToCommon)
	assert.Equal(t, "Tawny Owl", sciToCommon["Strix aluco"])
	assert.Equal(t, "Great Tit", sciToCommon["Parus major"])

	// Verify the pre-folded scientific-to-common map (used by substring search).
	folded := c.loadFoldedCommonNameMap()
	require.NotNil(t, folded)
	assert.Equal(t, "tawny owl", folded["Strix aluco"])
	assert.Equal(t, "great tit", folded["Parus major"])

	// Verify the common-to-scientific map (used by the search resolver).
	commonToSci := c.loadCommonToScientificMap()
	require.NotNil(t, commonToSci)
	assert.Equal(t, "Strix aluco", commonToSci["tawny owl"])
	assert.Equal(t, "Parus major", commonToSci["great tit"])
}

// TestBuildNameMaps_AmbiguousCommonName verifies that a common name mapped
// by two different scientific names is removed from commonToSci so the
// search resolver passes ambiguous queries through untranslated.
func TestBuildNameMaps_AmbiguousCommonName(t *testing.T) {
	t.Parallel()

	nm := buildNameMaps([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Parus major_Great Tit",
	}, nil)
	require.NotNil(t, nm)

	// sciToCommon keeps both species; scientific names are always unique.
	assert.Equal(t, "Owl", nm.sciToCommon["Strix aluco"])
	assert.Equal(t, "Owl", nm.sciToCommon["Bubo bubo"])

	// commonToSci must NOT contain the ambiguous key.
	_, ok := nm.commonToSci["owl"]
	assert.False(t, ok, "ambiguous common-name key should be removed")

	// A third label that repeats an already-ambiguous key should not
	// accidentally restore the key.
	nm = buildNameMaps([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Tyto alba_Owl",
	}, nil)
	_, ok = nm.commonToSci["owl"]
	assert.False(t, ok)

	// Non-ambiguous names remain.
	nm = buildNameMaps([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Parus major_Great Tit",
	}, nil)
	assert.Equal(t, "Parus major", nm.commonToSci["great tit"])
}

// TestBuildNameMaps_MalformedLabels verifies that labels missing a scientific
// name, a common name, or the separator are silently skipped rather than
// producing empty keys.
func TestBuildNameMaps_MalformedLabels(t *testing.T) {
	t.Parallel()

	nm := buildNameMaps([]string{
		"Strix aluco_Tawny Owl",
		"_MissingScientific",
		"MissingCommon_",
		"NoSeparatorAtAll",
		"",
		"   _   ",
	}, nil)
	require.NotNil(t, nm)
	assert.Len(t, nm.sciToCommon, 1)
	assert.Len(t, nm.commonToSci, 1)
	assert.Equal(t, "Tawny Owl", nm.sciToCommon["Strix aluco"])
	assert.Equal(t, "Strix aluco", nm.commonToSci["tawny owl"])
}

// TestLoadNameMaps_CalledBeforeInit verifies that the load helpers return
// non-nil empty maps when the Controller has not yet seeded nameMaps, so
// callers can index without nil checks during the startup window.
func TestLoadNameMaps_CalledBeforeInit(t *testing.T) {
	t.Parallel()

	c := &Controller{Core: &apicore.Core{}}
	assert.NotNil(t, c.loadCommonNameMap())
	assert.NotNil(t, c.loadFoldedCommonNameMap())
	assert.NotNil(t, c.loadCommonToScientificMap())
	assert.Empty(t, c.loadCommonNameMap())
	assert.Empty(t, c.loadFoldedCommonNameMap())
	assert.Empty(t, c.loadCommonToScientificMap())
}

// TestHandleSearch_LocalizedCommonName_SecondaryModelSpecies is the end-to-end
// HTTP regression test for the localized common-name search fix. It verifies that when a search
// request arrives with a localized common name for a secondary-model species
// (a bat label that has no embedded common name in the label string and is
// resolved only via the batch localizer), the raw term and resolved scientific
// name both reach the datastore. Pre-fix, the batch seam was absent so the bat
// label never entered commonToSci and the search fell back to a substring match
// on the unresolved localized string.
func TestHandleSearch_LocalizedCommonName_SecondaryModelSpecies(t *testing.T) {
	t.Attr("component", "search")
	t.Attr("feature", "localized-name-resolution")

	// Build the full facade so the detections domain handler is wired with the real
	// loadCommonToScientificMap accessor over this controller's name maps. The search
	// handler moved to the detections package; the facade exposes it via
	// controller.detections. setupTestEnvironment publishes the test settings to the
	// process-global snapshot, so this test must not call t.Parallel().
	e, mockDS, controller := setupTestEnvironment(t)

	// Wire a batch-capable resolver so the scientific-only bat label
	// "Barbastella barbastellus" (no underscore-separated common name in the
	// label string) gets a Finnish localized name via the batch path.
	controller.SetNameResolver(&analyticsBatchFakeResolver{batch: map[string]string{
		"Barbastella barbastellus": "mopsilepakko",
	}})
	// Feed the scientific-only label so UpdateCommonNameMap triggers the
	// batchLocalizer path and populates commonToSci with
	// "mopsilepakko" -> "Barbastella barbastellus".
	controller.UpdateCommonNameMap([]string{"Barbastella barbastellus"})

	// Capture the SearchFilters that reach the datastore.
	var captured *datastore.SearchFilters
	mockDS.EXPECT().
		SearchDetections(mock.Anything).
		RunAndReturn(func(f *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
			captured = f
			return nil, 0, nil
		}).Once()

	// Drive a POST /search request with the localized Finnish bat name.
	body := strings.NewReader(`{"species":"mopsilepakko"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/search", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/search")

	err := controller.detections.HandleSearch(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Keep the localized text for raw substring matching and add every matching
	// scientific name from the active-locale common-name map.
	require.NotNil(t, captured, "SearchDetections must have been called")
	assert.Equal(t, "mopsilepakko", captured.Species)
	assert.Equal(t, []string{"Barbastella barbastellus"}, captured.SpeciesScientific)

	mockDS.AssertExpectations(t)
}

// TestHandleSearch_ExactCommonNamePreservesSubstringUnion covers a taxonomic
// split where "Barn Owl" is an exact common name for Tyto alba but is also a
// substring of "American Barn Owl" (Tyto furcata). The exact resolution must be
// additive; replacing the raw term with Tyto alba hides Tyto furcata detections.
func TestHandleSearch_ExactCommonNamePreservesSubstringUnion(t *testing.T) {
	t.Attr("component", "search")
	t.Attr("feature", "common-name-substring-union")

	e, mockDS, controller := setupTestEnvironment(t)
	controller.UpdateCommonNameMap([]string{
		"Tyto alba_Barn Owl",
		"Tyto furcata_American Barn Owl",
	})

	var captured *datastore.SearchFilters
	mockDS.EXPECT().
		SearchDetections(mock.Anything).
		RunAndReturn(func(f *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
			captured = f
			return nil, 0, nil
		}).Once()

	body := strings.NewReader(`{"species":"barn owl"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/search", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/search")

	err := controller.detections.HandleSearch(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured, "SearchDetections must have been called")
	assert.Equal(t, "barn owl", captured.Species,
		"raw common name must remain available for substring matching")
	assert.Equal(t, []string{"Tyto alba", "Tyto furcata"}, captured.SpeciesScientific,
		"all active-locale common-name substring matches must be added")

	mockDS.AssertExpectations(t)
}
