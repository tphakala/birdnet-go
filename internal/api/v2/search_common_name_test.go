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

// TestUpdateCommonNameMap_PopulatesBothMaps verifies that UpdateCommonNameMap
// populates both the scientific-to-common map and the common-to-scientific map
// from the same label input, keeping them consistent.
func TestUpdateCommonNameMap_PopulatesBothMaps(t *testing.T) {
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
	assert.NotNil(t, c.loadCommonToScientificMap())
	assert.Empty(t, c.loadCommonNameMap())
	assert.Empty(t, c.loadCommonToScientificMap())
}

// TestHandleSearch_LocalizedCommonName_SecondaryModelSpecies is the end-to-end
// HTTP regression test for the localized common-name search fix. It verifies that when a search
// request arrives with a localized common name for a secondary-model species
// (a bat label that has no embedded common name in the label string and is
// resolved only via the batch localizer), the name is resolved to the scientific
// name before the datastore query runs. Pre-fix, the batch seam was absent so
// the bat label never entered commonToSci and the search fell back to a
// substring match on the unresolved localized string.
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

	// The localized name must have been resolved to the scientific name before
	// the datastore call. Pre-fix this would be "mopsilepakko" (unresolved).
	require.NotNil(t, captured, "SearchDetections must have been called")
	assert.Equal(t, "Barbastella barbastellus", captured.Species,
		"localized bat name must resolve to scientific name before the datastore query")

	mockDS.AssertExpectations(t)
}
