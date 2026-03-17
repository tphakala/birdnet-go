// api_defaults_regression_test.go: Regression tests for API endpoints called with
// no query parameters. Catches config migration breakage like #2352 where
// MigrateDashboardLayout zeroed Dashboard.SummaryLimit, causing GORM Limit(0) → 0 rows.
//
// Pattern: call endpoint with no/minimal query params → assert datastore receives
// valid fallback values → assert non-empty response.

package api

import (
	"encoding/json"
	"errors"
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
)

// testNotes is reusable mock data for endpoints returning []datastore.Note.
// ScientificName must be set to prevent aggregation collapse in daily species summary.
func testNotes() []datastore.Note {
	return []datastore.Note{
		{
			ID:             1,
			CommonName:     "American Robin",
			ScientificName: "Turdus migratorius",
			Confidence:     0.85,
			Date:           time.Now().Format(time.DateOnly),
			Time:           "08:30:00",
		},
		{
			ID:             2,
			CommonName:     "Blue Jay",
			ScientificName: "Cyanocitta cristata",
			Confidence:     0.72,
			Date:           time.Now().Format(time.DateOnly),
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
	// Some handlers return ErrResponseHandled when they write the response directly
	if err != nil && !errors.Is(err, ErrResponseHandled) {
		// Handler returned an error — check if it wrote an HTTP error response
		if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
			rec.Code = httpErr.Code
		}
	}
	return rec
}

func TestDailySpeciesSummary_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupTestEnvironment(t)

	today := time.Now().Format(time.DateOnly)
	notes := testNotes()

	// Key assertion: GetTopBirdsData must be called with today's date and the raw limit (0 = no limit)
	mockDS.On("GetTopBirdsData", today, 0.0, 0).Return(notes, nil).Once()

	// aggregateDailySpeciesData calls GetBatchHourlyOccurrences for hourly counts
	mockDS.On("GetBatchHourlyOccurrences", today, mock.Anything, 0.0).
		Return(map[string][24]int{
			"American Robin": {0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Blue Jay":       {0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		}, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/species/daily", controller.GetDailySpeciesSummary)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := strings.TrimSpace(rec.Body.String())
	assert.NotEqual(t, "[]", body, "response should not be empty when data exists")
	assert.NotEqual(t, "null", body, "response should not be null when data exists")

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return species data with default params")

	mockDS.AssertExpectations(t)
}

func TestSpeciesSummary_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockData := []datastore.SpeciesSummaryData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Count:          42,
			AvgConfidence:  0.75,
			MaxConfidence:  0.85,
		},
	}

	mockDS.On("GetSpeciesSummaryData", mock.Anything, "", "").Return(mockData, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/species/summary", controller.GetSpeciesSummary)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return summary data with default params")

	mockDS.AssertExpectations(t)
}

func TestNewSpeciesDetections_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockData := []datastore.NewSpeciesData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			FirstSeenDate:  "2025-01-15",
			CountInPeriod:  5,
		},
	}

	mockDS.On("GetNewSpeciesDetections",
		mock.Anything,          // context
		mock.Anything,          // startDate (30 days ago)
		mock.Anything,          // endDate (today)
		defaultNewSpeciesLimit, // 100
		0,                      // offset
	).Return(mockData, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/species/detections/new", controller.GetNewSpeciesDetections)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return new species with default params")

	mockDS.AssertExpectations(t)
}

func TestTimeOfDayDistribution_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockData := []datastore.HourlyDistributionData{
		{Hour: 8, Count: 5},
		{Hour: 9, Count: 3},
	}

	mockDS.On("GetHourlyDistribution",
		mock.Anything, // context
		mock.Anything, // startDate
		mock.Anything, // endDate
		"",            // species (empty = all)
	).Return(mockData, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/time/distribution/hourly", controller.GetTimeOfDayDistribution)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Len(t, result, 24, "should return 24-hour distribution array")

	mockDS.AssertExpectations(t)
}

func TestGetDetections_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupTestEnvironment(t)

	notes := testNotes()

	// Default: queryType="all" → SearchNotes("", false, 100, 0)
	mockDS.On("SearchNotes", "", false, defaultNumResults, 0).Return(notes, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/detections", controller.GetDetections)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))

	// Paginated response has a "data" array (PaginatedResponse.Data)
	detections, ok := result["data"].([]any)
	require.True(t, ok, "response should contain 'data' array")
	assert.NotEmpty(t, detections, "should return detections with default params")

	mockDS.AssertExpectations(t)
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

	// Must use setupTestEnvironment (not setupAnalyticsTestEnvironment) because
	// HandleSearch calls c.Debug() which dereferences c.Settings.WebServer.Debug.
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
			f.ConfidenceMax == 1.0 // Normalized from 0 → 1
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
