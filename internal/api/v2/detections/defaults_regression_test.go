// defaults_regression_test.go: regression tests for the detection/search endpoints
// called with no query parameters. Catches config-migration breakage like #2352
// where MigrateDashboardLayout zeroed Dashboard.SummaryLimit, causing GORM Limit(0)
// to return 0 rows.
//
// Pattern: call endpoint with no/minimal query params -> assert datastore receives
// valid fallback values -> assert non-empty response. The analytics counterparts of
// these tests stay in package api (api_defaults_regression_test.go); these moved
// here with the detection/search handlers and the default constants they pin.
package detections

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Scientific names for the reusable mock notes (local copies of the package-api
// analytics test constants).
const (
	sciAmericanRobin = "Turdus migratorius"
	sciBlueJay       = "Cyanocitta cristata"
)

// testNotes is reusable mock data for endpoints returning []datastore.Note.
// ScientificName must be set to prevent aggregation collapse downstream.
func testNotes() []datastore.Note {
	today := time.Now().Format(time.DateOnly)
	return []datastore.Note{
		{
			ID:             1,
			CommonName:     "American Robin",
			ScientificName: sciAmericanRobin,
			Confidence:     0.85,
			Date:           today,
			Time:           "08:30:00",
		},
		{
			ID:             2,
			CommonName:     "Blue Jay",
			ScientificName: sciBlueJay,
			Confidence:     0.72,
			Date:           today,
			Time:           "09:15:00",
		},
	}
}

// executeRequest creates an HTTP request with no query parameters and returns the recorder.
func executeRequest(t *testing.T, e *echo.Echo, method, path string, handler echo.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath(path)

	err := handler(c)
	if err != nil {
		// Handler returned an error - record the HTTP status when it is an echo error.
		var httpErr *echo.HTTPError
		if errors.As(err, &httpErr) {
			rec.Code = httpErr.Code
		} else {
			require.NoError(t, err, "handler returned an unexpected error")
		}
	}
	return rec
}

// setupPostMigrationTestEnvironment creates a test environment where
// MigrateDashboardLayout() has been called, zeroing Dashboard.SummaryLimit.
// This reproduces the exact scenario from #2352.
func setupPostMigrationTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e, mockDS, controller := setupTestEnvironment(t)

	// Run migration - moves SummaryLimit into layout element, zeros deprecated field
	migrated := controller.Settings.Load().MigrateDashboardLayout()
	require.True(t, migrated, "migration should have occurred (no pre-existing layout)")

	// Verify the deprecated field is actually zeroed
	assert.Equal(t, 0, controller.Settings.Load().Realtime.Dashboard.SummaryLimit,
		"deprecated SummaryLimit should be zeroed after migration")

	// Verify GetEffectiveSummaryLimit still returns a valid value
	effectiveLimit := controller.Settings.Load().GetEffectiveSummaryLimit()
	assert.Positive(t, effectiveLimit,
		"GetEffectiveSummaryLimit should return positive value after migration")

	return e, mockDS, controller
}

// verifyGetDetectionsDefaults is a shared helper for detection default-parameter regression tests.
func verifyGetDetectionsDefaults(t *testing.T, e *echo.Echo, mockDS *mocks.MockInterface, controller *Handler) {
	t.Helper()

	notes := testNotes()
	mockDS.On("SearchNotes", "", false, defaultNumResults, 0).Return(notes, int64(len(notes)), nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/detections", controller.GetDetections)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))

	detections, ok := result["data"].([]any)
	require.True(t, ok, "response should contain 'data' array")
	assert.NotEmpty(t, detections, "should return detections")

	mockDS.AssertExpectations(t)
}

func TestGetDetections_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupTestEnvironment(t)
	verifyGetDetectionsDefaults(t, e, mockDS, controller)
}

func TestGetRecentDetections_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupTestEnvironment(t)

	notes := testNotes()

	// Default limit is 10
	mockDS.On("GetLastDetections", 10).Return(notes, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/detections/recent", controller.GetRecentDetections)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return recent detections with default limit")

	mockDS.AssertExpectations(t)
}

func TestSearchDetections_EmptyBody(t *testing.T) {
	t.Parallel()
	t.Attr("component", "search")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	// Must use setupTestEnvironment because HandleSearch calls c.Debug() which
	// dereferences c.Settings.WebServer.Debug.
	e, mockDS, controller := setupTestEnvironment(t)

	mockResults := []datastore.DetectionRecord{
		{
			ID:         "1",
			CommonName: "American Robin",
			Confidence: 0.85,
		},
	}

	// Empty body defaults: page=1, confidenceMin=0, confidenceMax=1 (normalized),
	// all status filters="any", perPage=20
	mockDS.On("SearchDetections", mock.MatchedBy(func(f *datastore.SearchFilters) bool {
		return f.Page == 1 &&
			f.PerPage == defaultPerPage &&
			f.ConfidenceMin == 0.0 &&
			f.ConfidenceMax == 1.0 // Normalized from 0 -> 1
	})).Return(mockResults, 1, nil).Once()

	// Build request with empty JSON body
	body := strings.NewReader("{}")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/search", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/search")

	err := controller.HandleSearch(c)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	results, ok := result["results"].([]any)
	require.True(t, ok, "response should contain 'results' array")
	assert.NotEmpty(t, results, "should return results with default search params")

	mockDS.AssertExpectations(t)
}

func TestGetDetections_DefaultParams_AfterMigration(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupPostMigrationTestEnvironment(t)
	verifyGetDetectionsDefaults(t, e, mockDS, controller)
}
