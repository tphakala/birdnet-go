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
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// testNotes is reusable mock data for endpoints returning []datastore.Note.
// ScientificName must be set to prevent aggregation collapse in daily species summary.
func testNotes() []datastore.Note {
	today := time.Now().Format(time.DateOnly)
	return []datastore.Note{
		{
			ID:             1,
			CommonName:     "American Robin",
			ScientificName: "Turdus migratorius",
			Confidence:     0.85,
			Date:           today,
			Time:           "08:30:00",
		},
		{
			ID:             2,
			CommonName:     "Blue Jay",
			ScientificName: "Cyanocitta cristata",
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
	// Some handlers return ErrResponseHandled when they write the response directly
	if err != nil && !errors.Is(err, ErrResponseHandled) {
		// Handler returned an error — check if it wrote an HTTP error response
		if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
			rec.Code = httpErr.Code
		} else {
			// Unexpected non-HTTP error — fail the test immediately
			require.NoError(t, err, "handler returned an unexpected error")
		}
	}
	return rec
}

//nolint:dupl // intentional duplicate: same endpoint called via different setup (pre- vs post-migration)
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
			f.ConfidenceMin == 0.0 &&
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

func TestRequiredParams_ReturnBadRequest(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, _, controller := setupAnalyticsTestEnvironment(t)

	tests := []struct {
		name    string
		method  string
		path    string
		handler echo.HandlerFunc
	}{
		{
			name:    "GET /analytics/time/hourly requires date and species",
			method:  http.MethodGet,
			path:    "/api/v2/analytics/time/hourly",
			handler: controller.GetHourlyAnalytics,
		},
		{
			name:    "GET /analytics/time/daily requires start_date",
			method:  http.MethodGet,
			path:    "/api/v2/analytics/time/daily",
			handler: controller.GetDailyAnalytics,
		},
		{
			name:    "GET /analytics/species/daily/batch requires dates",
			method:  http.MethodGet,
			path:    "/api/v2/analytics/species/daily/batch",
			handler: controller.GetBatchDailySpeciesSummary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := executeRequest(t, e, tt.method, tt.path, tt.handler)

			// Must return 400, not 500 or panic
			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"endpoint should return 400 when called without required params")
		})
	}
}

// setupPostMigrationTestEnvironment creates a test environment where
// MigrateDashboardLayout() has been called, zeroing Dashboard.SummaryLimit.
// This reproduces the exact scenario from #2352.
func setupPostMigrationTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()
	e, mockDS, controller := setupTestEnvironment(t)

	// Run migration — moves SummaryLimit into layout element, zeros deprecated field
	migrated := controller.Settings.MigrateDashboardLayout()
	require.True(t, migrated, "migration should have occurred (no pre-existing layout)")

	// Verify the deprecated field is actually zeroed
	assert.Equal(t, 0, controller.Settings.Realtime.Dashboard.SummaryLimit,
		"deprecated SummaryLimit should be zeroed after migration")

	// Verify GetEffectiveSummaryLimit still returns a valid value
	effectiveLimit := controller.Settings.GetEffectiveSummaryLimit()
	assert.Positive(t, effectiveLimit,
		"GetEffectiveSummaryLimit should return positive value after migration")

	return e, mockDS, controller
}

//nolint:dupl // intentional duplicate: same endpoint called via different setup (pre- vs post-migration)
func TestDailySpeciesSummary_DefaultParams_AfterMigration(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2352")

	e, mockDS, controller := setupPostMigrationTestEnvironment(t)

	today := time.Now().Format(time.DateOnly)
	notes := testNotes()

	// After migration, the API handler still passes limit=0 (its default for "no limit param").
	mockDS.On("GetTopBirdsData", today, 0.0, 0).Return(notes, nil).Once()
	mockDS.On("GetBatchHourlyOccurrences", today, mock.Anything, 0.0).
		Return(map[string][24]int{
			"American Robin": {0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"Blue Jay":       {0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		}, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/species/daily", controller.GetDailySpeciesSummary)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := strings.TrimSpace(rec.Body.String())
	assert.NotEqual(t, "[]", body, "response must not be empty after migration")
	assert.NotEqual(t, "null", body, "response must not be null after migration")

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return species data after config migration")

	mockDS.AssertExpectations(t)
}

func TestGetDetections_DefaultParams_AfterMigration(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupPostMigrationTestEnvironment(t)

	notes := testNotes()

	// Detection defaults (numResults=100) are not config-driven, so should be unaffected
	mockDS.On("SearchNotes", "", false, defaultNumResults, 0).Return(notes, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/detections", controller.GetDetections)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))

	detections, ok := result["data"].([]any)
	require.True(t, ok, "response should contain 'data' array")
	assert.NotEmpty(t, detections, "should return detections after config migration")

	mockDS.AssertExpectations(t)
}

func TestGetEffectiveSummaryLimit_AfterMigration(t *testing.T) {
	t.Parallel()
	t.Attr("component", "config")
	t.Attr("type", "regression")
	t.Attr("issue", "2352")

	settings := newValidTestSettings()

	// Pre-migration baseline: capture effective limit before migration
	expectedLimit := settings.GetEffectiveSummaryLimit()
	assert.Positive(t, expectedLimit)
	assert.Equal(t, expectedLimit, settings.Realtime.Dashboard.SummaryLimit)

	// Run migration
	migrated := settings.MigrateDashboardLayout()
	require.True(t, migrated)

	// Post-migration: deprecated field zeroed, but effective limit preserved
	assert.Equal(t, 0, settings.Realtime.Dashboard.SummaryLimit,
		"deprecated field should be zeroed")
	assert.Equal(t, expectedLimit, settings.GetEffectiveSummaryLimit(),
		"effective limit should come from layout element after migration")

	// Second migration is a no-op
	assert.False(t, settings.MigrateDashboardLayout(),
		"second migration should be skipped")
}
