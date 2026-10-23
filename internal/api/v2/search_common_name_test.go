package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// analyticsBatchFakeResolver is a test resolver that satisfies SpeciesNameResolver
// and the optional batchLocalizer interface. The batch capability lets
// scientific-only labels (no embedded common name) reach the commonToSci reverse
// map and become resolvable. Shared by the search and exclude-list
// canonicalization tests.
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

// Note: the ambiguous-common-name drop and malformed-label skipping previously
// exercised here through buildNameMaps directly are now golden-tested in
// internal/speciesindex, which owns the shared builder.

// TestLoadNameMaps_CalledBeforeInit verifies that the load helpers return
// non-nil empty maps (speciesindex.Empty semantics) when the Controller's name
// index has not been seeded yet, so callers can index without nil checks during
// the startup window.
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
	// label string) gets a Finnish localized name via the batch path. Feeding the
	// scientific-only label triggers the batchLocalizer path and populates
	// commonToSci with "mopsilepakko" -> "Barbastella barbastellus".
	seedNames(t, controller, &analyticsBatchFakeResolver{batch: map[string]string{
		"Barbastella barbastellus": "mopsilepakko",
	}}, []string{"Barbastella barbastellus"})

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
	seedNames(t, controller, nil, []string{
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
