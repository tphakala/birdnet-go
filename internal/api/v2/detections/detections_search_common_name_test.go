package detections

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestGetDetections_CommonNameSubstringUnion exercises the GET endpoint used by
// DetectionsPage. "Barn Owl" is both an exact common name and a substring of
// "American Barn Owl", so resolving it by replacement would hide Tyto furcata.
func TestGetDetections_CommonNameSubstringUnion(t *testing.T) {
	t.Attr("component", "detections")
	t.Attr("feature", "common-name-substring-union")

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
	commonToSci := map[string]string{
		apicore.NormalizeForLookup("Barn Owl"):          "Tyto alba",
		apicore.NormalizeForLookup("American Barn Owl"): "Tyto furcata",
	}
	sciToCommon := map[string]string{
		"Tyto alba":    "Barn Owl",
		"Tyto furcata": "American Barn Owl",
	}
	handler := buildTestHandler(t, core, commonToSci, sciToCommon)

	var captured *datastore.AdvancedSearchFilters
	mockDS.EXPECT().
		SearchNotesAdvanced(mock.AnythingOfType("*datastore.AdvancedSearchFilters")).
		RunAndReturn(func(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
			captured = filters
			return []datastore.Note{
				{ID: 1, ScientificName: "Tyto alba", CommonName: "Barn Owl"},
				{ID: 2, ScientificName: "Tyto furcata", CommonName: "American Barn Owl"},
			}, 2, nil
		}).Once()

	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/detections?queryType=search&search=barn%20owl&numResults=25&offset=0",
		http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, handler.GetDetections(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "barn owl", captured.TextQuery,
		"raw text must remain available for substring matching")
	assert.Equal(t, []string{"Tyto alba", "Tyto furcata"}, captured.SpeciesScientific,
		"all active-locale substring matches must be OR-ed with the raw query")
	assert.Equal(t, datastore.SortBySearchDefault, captured.SortBy,
		"expanded simple search must preserve the datastore's historical ordering")

	mockDS.AssertExpectations(t)
}

// TestGetDetections_CommonNameSubstringUnionAdvanced verifies that additional
// filters cannot drop the expanded scientific-name alternatives when the HTTP
// request routes directly through advanced search.
func TestGetDetections_CommonNameSubstringUnionAdvanced(t *testing.T) {
	t.Attr("component", "detections")
	t.Attr("feature", "common-name-substring-union")

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
	handler := buildTestHandler(t, core,
		map[string]string{
			apicore.NormalizeForLookup("Barn Owl"):          "Tyto alba",
			apicore.NormalizeForLookup("American Barn Owl"): "Tyto furcata",
		},
		map[string]string{
			"Tyto alba":    "Barn Owl",
			"Tyto furcata": "American Barn Owl",
		})

	var captured *datastore.AdvancedSearchFilters
	mockDS.EXPECT().
		SearchNotesAdvanced(mock.AnythingOfType("*datastore.AdvancedSearchFilters")).
		RunAndReturn(func(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
			captured = filters
			return nil, 0, nil
		}).Once()

	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/detections?queryType=search&search=barn%20owl&confidence=%3E%3D50&numResults=25&offset=0",
		http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, handler.GetDetections(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "barn owl", captured.TextQuery)
	assert.Equal(t, []string{"Tyto alba", "Tyto furcata"}, captured.SpeciesScientific)
	require.NotNil(t, captured.Confidence)
	assert.Equal(t, ">=", captured.Confidence.Operator)
	assert.InDelta(t, 0.5, captured.Confidence.Value, 1e-9)

	mockDS.AssertExpectations(t)
}
