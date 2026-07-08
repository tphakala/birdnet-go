// analytics_test.go: Package api provides tests for API v2 analytics endpoints.

package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	speciestracker "github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Test date constant used across multiple test cases
const testDate = "2023-01-01"

// Scientific names reused as hourly-batch map keys across the daily-summary tests.
// The daily summary keys hourly aggregation on scientific name.
const (
	sciAmericanCrow         = "Corvus brachyrhynchos"
	sciRedBelliedWoodpecker = "Melanerpes carolinus"
	sciBarbastelleBat       = "Barbastella barbastellus"
	sciEurasianBlackbird    = "Turdus merula"
	sciAmericanRobin        = "Turdus migratorius"
	sciBlueJay              = "Cyanocitta cristata"
)

// assertAnalyticsErrorResponse validates analytics error responses.
func assertAnalyticsErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, expectedStatus int, expectedBody string) {
	t.Helper()
	assert.Equal(t, expectedStatus, rec.Code)
	if expectedStatus == http.StatusOK || expectedBody == "" {
		return
	}
	var errorResp map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &errorResp)
	require.NoError(t, err)
	errVal, ok := errorResp["error"]
	require.True(t, ok, "error response JSON should contain an 'error' field")
	assert.Contains(t, fmt.Sprint(errVal), expectedBody)
}

// TestGetSpeciesSummary tests the species summary endpoint
func TestGetSpeciesSummary(t *testing.T) {
	t.Parallel()
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "analytics")
	t.Attr("type", "integration")
	t.Attr("feature", "species-summary")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Create mock data
	firstSeen := time.Now().AddDate(0, -1, 0)
	lastSeen := time.Now().AddDate(0, 0, -1)

	mockSummaryData := []datastore.SpeciesSummaryData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Count:          42,
			FirstSeen:      firstSeen,
			LastSeen:       lastSeen,
			AvgConfidence:  0.75,
			MaxConfidence:  0.85,
		},
		{
			ScientificName: "Cyanocitta cristata",
			CommonName:     "Blue Jay",
			SpeciesCode:    "blujay",
			Count:          27,
			FirstSeen:      time.Now().AddDate(0, -2, 0),
			LastSeen:       time.Now(),
			AvgConfidence:  0.82,
			MaxConfidence:  0.92,
		},
	}

	// Setup mock expectations
	// Expect call with specific empty strings for no date filters
	mockDS.On("GetSpeciesSummaryData", mock.Anything, "", "").Return(mockSummaryData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary", http.NoBody) // Corrected path
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/summary") // Ensure path is set for context

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetSpeciesSummary(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response []map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Len(t, response, 2)
		assert.Equal(t, "Turdus migratorius", response[0]["scientific_name"])
		assert.Equal(t, "American Robin", response[0]["common_name"])
		assert.Equal(t, "amerob", response[0]["species_code"])
		assert.InDelta(t, 42, response[0]["count"], 0.01)
		assert.Equal(t, "Cyanocitta cristata", response[1]["scientific_name"])
		assert.Equal(t, "Blue Jay", response[1]["common_name"])
		assert.Equal(t, "blujay", response[1]["species_code"])
		assert.InDelta(t, 27, response[1]["count"], 0.01)

		// first_heard / last_heard must be ISO 8601 (RFC3339) with a timezone
		// offset, not the offset-less "2006-01-02 15:04:05" form. Regression
		// guard for issue #3793. A successful RFC3339 parse also proves the
		// offset is present, since RFC3339 requires one.
		firstHeard, ok := response[0]["first_heard"].(string)
		require.True(t, ok, "first_heard should be a string")
		parsedFirst, err := time.Parse(time.RFC3339, firstHeard)
		require.NoErrorf(t, err, "first_heard %q must be valid RFC3339", firstHeard)
		assert.WithinDuration(t, firstSeen, parsedFirst, time.Second,
			"first_heard should round-trip the FirstSeen instant")

		lastHeard, ok := response[0]["last_heard"].(string)
		require.True(t, ok, "last_heard should be a string")
		parsedLast, err := time.Parse(time.RFC3339, lastHeard)
		require.NoErrorf(t, err, "last_heard %q must be valid RFC3339", lastHeard)
		assert.WithinDuration(t, lastSeen, parsedLast, time.Second,
			"last_heard should round-trip the LastSeen instant")
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetSpeciesSummary_ZeroTimeOmitsHeardFields verifies that a species with no
// recorded first/last detection time (a zero time.Time) omits first_heard/last_heard
// from the JSON response entirely, rather than emitting an empty string or a
// 1970 epoch value. This guards the omitempty contract on the RFC3339-formatted
// fields. Companion to the RFC3339 format assertion in TestGetSpeciesSummary. See
// issue #3793.
func TestGetSpeciesSummary_ZeroTimeOmitsHeardFields(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "species-summary")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockSummaryData := []datastore.SpeciesSummaryData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Count:          42,
			// FirstSeen and LastSeen left as the zero time.Time on purpose.
			AvgConfidence: 0.75,
			MaxConfidence: 0.85,
		},
	}
	mockDS.On("GetSpeciesSummaryData", mock.Anything, "", "").Return(mockSummaryData, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/summary")

	require.NoError(t, controller.GetSpeciesSummary(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var response []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response, 1)

	_, hasFirst := response[0]["first_heard"]
	assert.False(t, hasFirst, "first_heard should be omitted when the detection time is zero")
	_, hasLast := response[0]["last_heard"]
	assert.False(t, hasLast, "last_heard should be omitted when the detection time is zero")

	mockDS.AssertExpectations(t)
}

// TestGetSpeciesSummaryDatabaseError tests that database errors are properly handled and return 500
func TestGetSpeciesSummaryDatabaseError(t *testing.T) {
	t.Parallel()
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "analytics")
	t.Attr("type", "error-handling")
	t.Attr("feature", "database-error")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Setup mock to return a database error (like the SQL aggregate error)
	dbError := errors.New("Error 1140 (42000): In aggregated query without GROUP BY, expression #3 of SELECT list contains nonaggregated column 'datastore.notes.species_code'")
	mockDS.On("GetSpeciesSummaryData", mock.Anything, "", "").Return([]datastore.SpeciesSummaryData{}, dbError)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/summary")

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetSpeciesSummary(c)
	}

	// Test - HandleError returns nil and writes JSON response
	err := handler(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var errorResponse map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Contains(t, errorResponse["message"], "Failed to get species summary data")

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestAnalyticsEndpointContextErrors verifies that analytics endpoints which were
// previously passing the raw request context now bound their datastore query and map
// context errors to the right HTTP status: a deadline to 408 (not 500), and a client
// cancellation to 499 (client closed request). Regression guard for the query-timeout
// consistency fix.
func TestAnalyticsEndpointContextErrors(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "error-handling")
	t.Attr("feature", "query-timeout")

	errorCases := []struct {
		name       string
		queryErr   error
		wantStatus int
		wantMsg    string
	}{
		{"deadline exceeded", context.DeadlineExceeded, http.StatusRequestTimeout, "Query timeout"},
		{"client canceled", context.Canceled, apicore.StatusClientClosedRequest, "Request canceled by client"},
	}

	endpoints := []struct {
		name      string
		path      string
		setupMock func(*mocks.MockInterface, error)
		invoke    func(*Handler, echo.Context) error
	}{
		{
			name: "species summary",
			path: "/api/v2/analytics/species/summary",
			setupMock: func(m *mocks.MockInterface, queryErr error) {
				m.On("GetSpeciesSummaryData", mock.Anything, "", "").
					Return([]datastore.SpeciesSummaryData{}, queryErr)
			},
			invoke: func(c *Handler, ctx echo.Context) error { return c.GetSpeciesSummary(ctx) },
		},
		{
			name: "time of day distribution",
			path: "/api/v2/analytics/time/distribution/hourly",
			setupMock: func(m *mocks.MockInterface, queryErr error) {
				m.On("GetHourlyDistribution", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.HourlyDistributionData{}, queryErr)
			},
			invoke: func(c *Handler, ctx echo.Context) error { return c.GetTimeOfDayDistribution(ctx) },
		},
		{
			name: "new species detections",
			path: "/api/v2/analytics/species/detections/new",
			setupMock: func(m *mocks.MockInterface, queryErr error) {
				m.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, queryErr)
			},
			invoke: func(c *Handler, ctx echo.Context) error { return c.GetNewSpeciesDetections(ctx) },
		},
		{
			// Exercises the now-context-bounded GetTopBirdsData query.
			name: "daily species summary",
			path: "/api/v2/analytics/species/daily",
			setupMock: func(m *mocks.MockInterface, queryErr error) {
				m.On("GetTopBirdsData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.Note{}, queryErr)
			},
			invoke: func(c *Handler, ctx echo.Context) error { return c.GetDailySpeciesSummary(ctx) },
		},
	}

	for _, ep := range endpoints {
		for _, ec := range errorCases {
			t.Run(ep.name+"/"+ec.name, func(t *testing.T) {
				t.Parallel()
				e, mockDS, controller := setupAnalyticsTestEnvironment(t)
				ep.setupMock(mockDS, ec.queryErr)

				req := httptest.NewRequest(http.MethodGet, ep.path, http.NoBody)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				c.SetPath(ep.path)

				require.NoError(t, ep.invoke(controller, c))
				assert.Equal(t, ec.wantStatus, rec.Code)

				var errorResponse map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errorResponse))
				assert.Contains(t, errorResponse["message"], ec.wantMsg)

				mockDS.AssertExpectations(t)
			})
		}
	}
}

// TestGetSpeciesSummaryWithDateFilters tests the species summary endpoint with date filtering
func TestGetSpeciesSummaryWithDateFilters(t *testing.T) {
	t.Parallel()
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "analytics")
	t.Attr("type", "filtering")
	t.Attr("feature", "date-filters")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Create mock data
	mockSummaryData := []datastore.SpeciesSummaryData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Count:          10,
			FirstSeen:      time.Date(2024, 1, 15, 8, 30, 0, 0, time.UTC),
			LastSeen:       time.Date(2024, 1, 16, 14, 0, 0, 0, time.UTC),
			AvgConfidence:  0.85,
			MaxConfidence:  0.90,
		},
	}

	// Setup mock expectations with date filters
	mockDS.On("GetSpeciesSummaryData", mock.Anything, "2024-01-15", "2024-01-16").Return(mockSummaryData, nil)

	// Create a request with date parameters
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary?start_date=2024-01-15&end_date=2024-01-16", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/summary")

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetSpeciesSummary(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response []map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Len(t, response, 1)
		assert.Equal(t, "Turdus migratorius", response[0]["scientific_name"])
		assert.InDelta(t, 10, response[0]["count"], 0.01)
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetHourlyAnalytics tests the hourly analytics endpoint
func TestGetHourlyAnalytics(t *testing.T) {
	t.Parallel()
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "analytics")
	t.Attr("type", "integration")
	t.Attr("feature", "hourly-analytics")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Create mock data
	date := testDate
	species := "Turdus migratorius"

	mockHourlyData := []datastore.HourlyAnalyticsData{
		{
			Hour:  0,
			Count: 5,
		},
		{
			Hour:  1,
			Count: 3,
		},
	}

	// Setup mock expectations
	mockDS.On("GetHourlyAnalyticsData", mock.Anything, date, species).Return(mockHourlyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/time/hourly?date=2023-01-01&species=Turdus+migratorius", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/hourly")
	c.QueryParams().Set("date", date)
	c.QueryParams().Set("species", species)

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetHourlyAnalytics(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body - the actual implementation returns a single object, not an array
		var response map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, date, response["date"])
		assert.Equal(t, species, response["species"])

		// Check the counts array
		counts, ok := response["counts"].([]any)
		assert.True(t, ok, "Expected counts to be an array")
		assert.Len(t, counts, 24, "Expected 24 hours in counts array")

		// Check specific hour counts that were set in our mock
		assert.InDelta(t, 5, counts[0], 0.01, "Hour 0 should have 5 counts")
		assert.InDelta(t, 3, counts[1], 0.01, "Hour 1 should have 3 counts")

		// Check the total
		assert.InDelta(t, 8, response["total"], 0.01, "Total should be sum of all counts")
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetDailyAnalytics tests the daily analytics endpoint
func TestGetDailyAnalytics(t *testing.T) {
	t.Parallel()
	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Create mock data
	startDate := testDate
	endDate := "2023-01-07"
	species := "Turdus migratorius"

	mockDailyData := []datastore.DailyAnalyticsData{
		{
			Date:  testDate,
			Count: 12,
		},
		{
			Date:  "2023-01-02",
			Count: 8,
		},
	}

	// Setup mock expectations
	mockDS.On("GetDailyAnalyticsData", mock.Anything, startDate, endDate, species).Return(mockDailyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/analytics/time/daily?start_date=2023-01-01&end_date=2023-01-07&species=Turdus+migratorius", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/daily")
	c.QueryParams().Set("start_date", startDate)
	c.QueryParams().Set("end_date", endDate)
	c.QueryParams().Set("species", species)

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetDailyAnalytics(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body - the actual implementation returns an object with a 'data' array
		var response map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response metadata
		assert.Equal(t, startDate, response["start_date"])
		assert.Equal(t, endDate, response["end_date"])
		assert.Equal(t, species, response["species"])
		assert.InDelta(t, 20, response["total"], 0.01) // 12 + 8 = 20

		// Check data array
		data, ok := response["data"].([]any)
		assert.True(t, ok, "Expected data to be an array")
		assert.Len(t, data, 2, "Expected 2 items in data array")

		// Check first data item
		item1 := data[0].(map[string]any)
		assert.Equal(t, testDate, item1["date"])
		assert.InDelta(t, 12, item1["count"], 0.01)

		// Check second data item
		item2 := data[1].(map[string]any)
		assert.Equal(t, "2023-01-02", item2["date"])
		assert.InDelta(t, 8, item2["count"], 0.01)
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetDailyAnalyticsWithoutSpecies tests the daily analytics endpoint when no species is provided
// This tests the aggregated data behavior, which represents detection trends across all species
func TestGetDailyAnalyticsWithoutSpecies(t *testing.T) {
	t.Parallel()
	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Create mock data
	startDate := testDate
	endDate := "2023-01-07"

	mockDailyData := []datastore.DailyAnalyticsData{
		{
			Date:  "2023-01-07",
			Count: 45,
		},
		{
			Date:  "2023-01-06",
			Count: 38,
		},
		{
			Date:  "2023-01-05",
			Count: 42,
		},
	}

	// Setup mock expectations
	mockDS.On("GetDailyAnalyticsData", mock.Anything, startDate, endDate, "").Return(mockDailyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/analytics/time/daily?start_date=2023-01-01&end_date=2023-01-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/daily")
	c.QueryParams().Set("start_date", startDate)
	c.QueryParams().Set("end_date", endDate)

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetDailyAnalytics(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		data, ok := response["data"].([]any)
		assert.True(t, ok)
		assert.Len(t, data, 3)
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetInvalidAnalyticsRequests tests various invalid requests to analytics endpoints
func TestGetInvalidAnalyticsRequests(t *testing.T) {
	t.Parallel()
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "analytics")
	t.Attr("type", "validation")
	t.Attr("feature", "input-validation")

	testCases := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GetDailySpeciesSummary - Invalid Date",
			method:         http.MethodGet,
			path:           "/api/v2/analytics/species/daily?date=invalid-date",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Invalid date format. Use YYYY-MM-DD",
		},
		{
			name:           "GetSpeciesSummary - Start After End",
			method:         http.MethodGet,
			path:           "/api/v2/analytics/species/summary?start_date=2023-01-10&end_date=2023-01-01",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Invalid date parameters",
		},
		{
			name:           "GetHourlyAnalytics - Missing Date",
			method:         http.MethodGet,
			path:           "/api/v2/analytics/time/hourly?species=test",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing required parameter: date",
		},
		{
			name:           "GetHourlyAnalytics - Missing Species",
			method:         http.MethodGet,
			path:           "/api/v2/analytics/time/hourly?date=2023-01-01",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing required parameter: species",
		},
		{
			name:           "GetDailyAnalytics - Missing Start Date",
			method:         http.MethodGet,
			path:           "/api/v2/analytics/time/daily?species=test",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing required parameter: start_date",
		},
		// Add more invalid cases as needed
	}

	// Setup: Ensure settings are valid for controller creation
	appSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(),
				},
			},
		},
	}

	mockDS := mocks.NewMockInterface(t)
	// Mock expectations for image cache initialization
	mockDS.EXPECT().
		GetAllImageCaches(mock.AnythingOfType("string")).
		Return([]datastore.ImageCache{}, nil).
		Maybe()

	// Initialize a mock image cache for controller creation - ONCE for all test cases
	testMetrics, _ := observability.NewMetrics() // Create a dummy metrics instance
	// Create a stub provider to avoid nil pointer panics
	stubProvider := &apitest.TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			return imageprovider.BirdImage{}, nil
		},
	}
	mockImageCache := imageprovider.InitCache("test", stubProvider, testMetrics, mockDS)
	t.Cleanup(func() {
		assert.NoError(t, mockImageCache.Close(), "Failed to close image cache")
	})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			controller := newTestHandler(&apicore.Core{DS: mockDS, BirdImageCache: mockImageCache})
			controller.Settings.Store(appSettings)

			e := echo.New()
			// Register routes needed for this test run
			controller.Group = e.Group("/api/v2") // Assign group for proper route registration
			controller.Echo = e                   // Set Echo instance for the controller
			controller.RegisterAnalyticsRoutes(controller.Group)

			req := httptest.NewRequest(tc.method, tc.path, http.NoBody)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)
			assertAnalyticsErrorResponse(t, rec, tc.expectedStatus, tc.expectedBody)
		})
	}
}

// TestGetDailySpeciesSummary_MultipleDetections tests that the GetDailySpeciesSummary function
// correctly counts multiple detections of the same species
func TestGetDailySpeciesSummary_MultipleDetections(t *testing.T) {
	t.Parallel()
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "analytics")
	t.Attr("type", "integration")
	t.Attr("feature", "species-summary")

	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := mocks.NewMockInterface(t)

	testDate := "2025-03-07"
	minConfidence := 0.0

	// Expected data for GetTopBirdsData
	mockNotes := []datastore.Note{
		{
			ID:             1,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.9,
			Date:           testDate,
			Time:           "08:15:00",
		},
		{
			ID:             2,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.85,
			Date:           testDate,
			Time:           "09:30:00",
		},
		{
			ID:             3,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.95,
			Date:           testDate,
			Time:           "14:45:00",
		},
		{
			ID:             4,
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.8,
			Date:           testDate,
			Time:           "10:20:00",
		},
		{
			ID:             5,
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.75,
			Date:           testDate,
			Time:           "16:05:00",
		},
	}

	// Expected hourly counts for American Crow
	var expectedAmcroHourlyCounts [24]int
	expectedAmcroHourlyCounts[8] = 1  // From 08:15:00
	expectedAmcroHourlyCounts[9] = 1  // From 09:30:00
	expectedAmcroHourlyCounts[14] = 1 // From 14:45:00
	amcroTotal := 3

	// Expected hourly counts for Red-bellied Woodpecker
	var expectedRbwoHourlyCounts [24]int
	expectedRbwoHourlyCounts[10] = 1 // From 10:20:00
	expectedRbwoHourlyCounts[16] = 1 // From 16:05:00
	rbwoTotal := 2

	// Setup mock expectations using m.On()
	mockDS.On("GetTopBirdsData", mock.Anything, testDate, minConfidence, 0).Return(mockNotes, nil)
	// Now using batch query instead of individual calls
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, testDate, mock.MatchedBy(func(species []string) bool {
		return len(species) == 2 &&
			((species[0] == sciAmericanCrow && species[1] == sciRedBelliedWoodpecker) ||
				(species[0] == sciRedBelliedWoodpecker && species[1] == sciAmericanCrow))
	}), minConfidence).Return(map[string][24]int{
		sciAmericanCrow:         expectedAmcroHourlyCounts,
		sciRedBelliedWoodpecker: expectedRbwoHourlyCounts,
	}, nil)

	// The daily summary emits the media-proxy URL directly and no longer queries
	// the image cache for thumbnails (#3806), so no BirdImageCache is needed here.
	// The ThumbnailURLContain assertions below prove the proxy URL is emitted
	// regardless of cache state.
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v2/analytics/species/daily?date=%s", testDate), http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/daily") // Set path for context if needed by handler
	c.QueryParams().Set("date", testDate)        // Ensure query param is accessible

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	require.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify we got the expected number of species (2 in this case)
	assert.Len(t, response, 2)

	// Find the American Crow and Red-bellied Woodpecker in the response
	// Note: Response order might not be guaranteed unless sorted, check implementation or sort here
	var amcro *SpeciesDailySummary
	var rbwo *SpeciesDailySummary
	for i := range response {
		if response[i].ScientificName == "Corvus brachyrhynchos" {
			amcro = &response[i]
		}
		if response[i].ScientificName == "Melanerpes carolinus" {
			rbwo = &response[i]
		}
	}

	// Verify the American Crow details
	assert.NotNil(t, amcro, "American Crow should be in the response")
	if amcro != nil {
		assertSpeciesDailySummary(t, amcro, &SpeciesDailySummaryExpected{
			CommonName:          "American Crow",
			SpeciesCode:         "AMCRO",
			Count:               amcroTotal,
			HourlyCounts:        expectedAmcroHourlyCounts[:],
			FirstHeard:          "08:15:00",
			LatestHeard:         "14:45:00",
			HighConfidence:      true, // Based on 0.95 > 0.8
			ThumbnailURLContain: "/api/v2/media/image/Corvus%20brachyrhynchos",
		})
		// Max confidence is merged from the species summary aggregation
		assert.InDelta(t, 0.95, amcro.MaxConfidence, 0.001, "American Crow max confidence should be merged")
	}

	// Verify the Red-bellied Woodpecker details
	assert.NotNil(t, rbwo, "Red-bellied Woodpecker should be in the response")
	if rbwo != nil {
		assertSpeciesDailySummary(t, rbwo, &SpeciesDailySummaryExpected{
			CommonName:          "Red-bellied Woodpecker",
			SpeciesCode:         "RBWO",
			Count:               rbwoTotal,
			HourlyCounts:        expectedRbwoHourlyCounts[:],
			FirstHeard:          "10:20:00",
			LatestHeard:         "16:05:00",
			HighConfidence:      true, // Based on 0.8 >= 0.8
			ThumbnailURLContain: "/api/v2/media/image/Melanerpes%20carolinus",
		})
		// RBWO's max (0.8) is NOT its last note (0.75), so this pins the
		// max() aggregation rather than a "last-note-wins" implementation.
		assert.InDelta(t, 0.8, rbwo.MaxConfidence, 0.001, "Red-bellied Woodpecker max confidence should be the max across notes, not the last")
	}

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_LocalizedNonPrimarySpecies is a regression test:
// a non-primary-model species (a bat from BattyBirdNET) whose common
// name is localized by OpenFauna to a value different from its scientific name
// must still appear in the daily summary. The pre-fix pipeline keyed the hourly
// aggregation on the localized common name and reverse-mapped it through a
// label-only map that has no bat entry, so the hourly counts came back zero and
// the species was dropped. The fix keys the aggregation on scientific name end to
// end.
func TestGetDailySpeciesSummary_LocalizedNonPrimarySpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("feature", "species-summary")

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)

	const testDate = "2025-03-07"
	const minConfidence = 0.0

	// A bat localized by OpenFauna to a Finnish common name that differs from its
	// scientific name, plus a regular bird as a control.
	mockNotes := []datastore.Note{
		{
			ID:             1,
			ScientificName: sciBarbastelleBat,
			CommonName:     "mopsilepakko",
			Confidence:     0.9,
			Date:           testDate,
			Time:           "23:15:00",
		},
		{
			ID:             2,
			ScientificName: sciEurasianBlackbird,
			CommonName:     "Common Blackbird",
			Confidence:     0.8,
			Date:           testDate,
			Time:           "08:20:00",
		},
	}

	var batHourly [24]int
	batHourly[23] = 5
	var blackbirdHourly [24]int
	blackbirdHourly[8] = 2

	mockDS.On("GetTopBirdsData", mock.Anything, testDate, minConfidence, 0).Return(mockNotes, nil)

	// Post-fix contract: the controller keys the hourly aggregation on scientific
	// name end to end, so GetBatchHourlyOccurrences receives scientific names and
	// returns a map keyed by scientific name.
	var passedSpecies []string
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, testDate, mock.Anything, minConfidence).
		Run(func(args mock.Arguments) {
			// Args are (ctx, date, species, minConfidence); species is index 2.
			arg, ok := args.Get(2).([]string)
			require.True(t, ok, "species argument should be []string")
			passedSpecies = slices.Clone(arg)
		}).
		Return(map[string][24]int{
			sciBarbastelleBat:    batHourly,
			sciEurasianBlackbird: blackbirdHourly,
		}, nil)

	controller := newTestHandler(&apicore.Core{DS: mockDS})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date="+testDate, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.GetDailySpeciesSummary(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var response []SpeciesDailySummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

	// The controller must pass scientific names (not localized common names) to
	// the hourly batch fetch.
	assert.ElementsMatch(t, []string{sciBarbastelleBat, sciEurasianBlackbird}, passedSpecies,
		"hourly batch should be keyed on scientific name, not localized common name")

	bySci := make(map[string]SpeciesDailySummary, len(response))
	for i := range response {
		bySci[response[i].ScientificName] = response[i]
	}

	bat, ok := bySci[sciBarbastelleBat]
	require.True(t, ok, "bat species must appear in the daily summary (regression)")
	assert.Equal(t, 5, bat.Count, "bat hourly counts must be aggregated by scientific name")
	assert.Equal(t, "mopsilepakko", bat.CommonName, "bat keeps its localized common name for display")

	blackbird, ok := bySci[sciEurasianBlackbird]
	require.True(t, ok, "control bird must appear in the daily summary")
	assert.Equal(t, 2, blackbird.Count)

	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_ThumbnailDefersToProxy is a regression test for #3806.
// The dashboard daily summary must emit the media-proxy URL for every species with
// detections, independent of the image cache, so the proxy resolves images through
// the single-item fallback chain instead of the dashboard showing a placeholder when
// the primary provider has a negative cache entry. A nil BirdImageCache proves the
// thumbnail URL no longer depends on a cache-only lookup.
func TestGetDailySpeciesSummary_ThumbnailDefersToProxy(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("feature", "species-summary")

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)

	const testDate = "2025-03-07"
	const minConfidence = 0.0
	const sciName = sciAmericanCrow

	mockDS.On("GetTopBirdsData", mock.Anything, testDate, minConfidence, 0).Return([]datastore.Note{
		{ID: 1, ScientificName: sciName, CommonName: "American Crow", Confidence: 0.9, Date: testDate, Time: "08:15:00"},
	}, nil)

	var hourly [24]int
	hourly[8] = 1
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, testDate, mock.Anything, minConfidence).
		Return(map[string][24]int{sciName: hourly}, nil)

	// Deliberately no BirdImageCache: the thumbnail URL must not depend on it.
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date="+testDate, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.GetDailySpeciesSummary(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var response []SpeciesDailySummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response, 1)

	assert.Equal(t, imageprovider.ProxyImageURL(sciName), response[0].ThumbnailURL,
		"daily summary must emit the media-proxy URL so the proxy applies the fallback chain (#3806)")
	assert.Contains(t, response[0].ThumbnailURL, "/api/v2/media/image/Corvus%20brachyrhynchos")
	assert.NotContains(t, response[0].ThumbnailURL, "bird-placeholder",
		"dashboard thumbnail must not fall back to the static placeholder on the summary path")

	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_SingleDetection tests that the GetDailySpeciesSummary function
// correctly handles the case where each species has only one detection
func TestGetDailySpeciesSummary_SingleDetection(t *testing.T) {
	t.Parallel()
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := mocks.NewMockInterface(t)

	// Expected data for GetTopBirdsData
	mockNotesSingle := []datastore.Note{
		{
			ID:             1,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.9,
			Date:           "2025-03-07",
			Time:           "08:15:00",
		},
		{
			ID:             2,
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.8,
			Date:           "2025-03-07",
			Time:           "10:20:00",
		},
	}

	// Expected hourly counts for American Crow (single detection)
	var expectedAmcroSingleHourly [24]int
	expectedAmcroSingleHourly[8] = 1

	// Expected hourly counts for Red-bellied Woodpecker (single detection)
	var expectedRbwoSingleHourly [24]int
	expectedRbwoSingleHourly[10] = 1

	// Setup mock expectations using m.On()
	mockDS.On("GetTopBirdsData", mock.Anything, "2025-03-07", 0.0, 0).Return(mockNotesSingle, nil)
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2025-03-07", mock.Anything, 0.0).Return(map[string][24]int{
		sciAmericanCrow:         expectedAmcroSingleHourly,
		sciRedBelliedWoodpecker: expectedRbwoSingleHourly,
	}, nil)

	// The daily summary emits proxy URLs directly and no longer queries the image
	// cache for thumbnails (#3806), so no BirdImageCache is needed here.
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	require.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify we got the expected number of species
	assert.Len(t, response, 2)

	// Verify each species has a count of 1
	for _, species := range response {
		assert.Equal(t, 1, species.Count, "%s should have 1 detection", species.CommonName)
	}

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_EmptyResult tests that the GetDailySpeciesSummary function
// correctly handles the case where no detections are found
func TestGetDailySpeciesSummary_EmptyResult(t *testing.T) {
	t.Parallel()
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore that returns no detections
	mockDS := mocks.NewMockInterface(t)

	// Setup mock expectations using m.On()
	// Expect GetTopBirdsData to be called and return empty slice
	mockDS.On("GetTopBirdsData", mock.Anything, "2025-03-07", 0.0, 0).Return([]datastore.Note{}, nil)
	// Expect GetBatchHourlyOccurrences not to be called since there are no birds

	// Create a controller with our mock
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	require.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify we got an empty result
	assert.Empty(t, response)

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_TimeHandling tests that the GetDailySpeciesSummary function
// correctly handles the first and latest detection times
func TestGetDailySpeciesSummary_TimeHandling(t *testing.T) {
	t.Parallel()
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := mocks.NewMockInterface(t)

	// Expected data for GetTopBirdsData
	mockNotesTime := []datastore.Note{
		{
			ID:             2,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.85,
			Date:           "2025-03-07",
			Time:           "06:30:00", // Earlier time - put this first
		},
		{
			ID:             1,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.9,
			Date:           "2025-03-07",
			Time:           "08:15:00",
		},
		{
			ID:             3,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.95,
			Date:           "2025-03-07",
			Time:           "21:45:00", // Later time
		},
	}

	// Expected hourly counts for American Crow
	var expectedAmcroTimeHourly [24]int
	expectedAmcroTimeHourly[6] = 1
	expectedAmcroTimeHourly[8] = 1
	expectedAmcroTimeHourly[21] = 1

	// Setup mock expectations using m.On()
	mockDS.On("GetTopBirdsData", mock.Anything, "2025-03-07", 0.0, 0).Return(mockNotesTime, nil)
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2025-03-07", mock.Anything, 0.0).Return(map[string][24]int{
		sciAmericanCrow: expectedAmcroTimeHourly,
	}, nil)

	// Create a controller with our mock
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	require.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify we got one species
	assert.Len(t, response, 1)

	// Verify the first and latest times are correct
	species := response[0]
	assert.Equal(t, "06:30:00", species.FirstHeard, "First detection should be 06:30:00")
	assert.Equal(t, "21:45:00", species.LatestHeard, "Latest detection should be 21:45:00")

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_ConfidenceFilter tests that the GetDailySpeciesSummary function
// correctly filters by confidence level
func TestGetDailySpeciesSummary_ConfidenceFilter(t *testing.T) {
	t.Parallel()
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := mocks.NewMockInterface(t)

	// Expected confidence filter value (passed as %) -> converted to decimal
	expectedMinConfidence := 0.7 // 70%

	// Expected data for GetTopBirdsData (includes species below the threshold)
	mockNotesConfidence := []datastore.Note{
		{
			ID:             1,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.9, // Above threshold
			Date:           "2025-03-07",
			Time:           "08:15:00",
		},
		{
			ID:             2,
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.6, // Below threshold
			Date:           "2025-03-07",
			Time:           "10:20:00",
		},
	}

	// Expected hourly counts (only for species meeting confidence threshold)
	var expectedAmcroConfidenceHourly [24]int
	expectedAmcroConfidenceHourly[8] = 1

	// Setup mock expectations
	// GetTopBirdsData is called with the normalized confidence
	mockDS.On("GetTopBirdsData", mock.Anything, "2025-03-07", expectedMinConfidence, 0).Return(mockNotesConfidence, nil)
	// GetBatchHourlyOccurrences is called for all species returned by GetTopBirdsData
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2025-03-07", mock.Anything, expectedMinConfidence).Return(map[string][24]int{
		sciAmericanCrow:         expectedAmcroConfidenceHourly,
		sciRedBelliedWoodpecker: {}, // Filtered out later
	}, nil)

	// Create a controller with our mock
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	// Test with a confidence threshold of "70"
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07&min_confidence=70", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	require.NoError(t, err)

	// Verify the threshold was correctly normalized (70% -> 0.7)
	assert.InDelta(t, 0.7, expectedMinConfidence, 0.01)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify the response only includes species with confidence >= 0.7
	for _, species := range response {
		assert.NotEqual(t, "Red-bellied Woodpecker", species.CommonName)
		assert.Equal(t, "American Crow", species.CommonName)
	}

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_LimitParameter tests that the GetDailySpeciesSummary function
// correctly applies the limit parameter
func TestGetDailySpeciesSummary_LimitParameter(t *testing.T) {
	t.Parallel()
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := mocks.NewMockInterface(t)

	// Expected data for GetTopBirdsData (more than the limit)
	// Mock returns only 2 notes since limit=2 is passed to GetTopBirdsData
	mockNotesLimit := []datastore.Note{
		{
			ID:             1,
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.9,
			Date:           "2025-03-07",
			Time:           "08:15:00",
		},
		{
			ID:             2,
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.8,
			Date:           "2025-03-07",
			Time:           "10:20:00",
		},
	}

	// Expected hourly counts for each species
	var expectedAmcroLimitHourly [24]int
	expectedAmcroLimitHourly[8] = 1
	var expectedRbwoLimitHourly [24]int
	expectedRbwoLimitHourly[10] = 1
	var expectedBcchLimitHourly [24]int
	expectedBcchLimitHourly[12] = 1

	// Setup mock expectations
	mockDS.On("GetTopBirdsData", mock.Anything, "2025-03-07", 0.0, 2).Return(mockNotesLimit, nil)
	// Expect GetBatchHourlyOccurrences to be called for the 2 species returned
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2025-03-07", mock.Anything, 0.0).Return(map[string][24]int{
		sciAmericanCrow:         expectedAmcroLimitHourly,
		sciRedBelliedWoodpecker: expectedRbwoLimitHourly,
	}, nil)

	// Create a controller with our mock
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	// Create a request with a limit of 2
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07&limit=2", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.QueryParams().Set("limit", "2")

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	require.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify we got exactly 2 species (limited)
	assert.Len(t, response, 2)

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_DatabaseError tests that the GetDailySpeciesSummary function
// correctly handles database errors
func TestGetDailySpeciesSummary_DatabaseError(t *testing.T) {
	t.Parallel()
	// Setup using the proper test environment
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Override the GetTopBirdsData function to return an error
	mockDS.On("GetTopBirdsData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, errors.New("database connection error"))

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Verify the response status code is 500 Internal Server Error
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse the error response
	var errorResponse map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check that the error response contains the expected fields
	assert.Contains(t, errorResponse, "error")
	assert.Contains(t, errorResponse, "message")
	assert.Contains(t, errorResponse, "code")

	// Check the error message: in non-debug mode, Error field uses sanitized message
	assert.Equal(t, "Failed to get daily species data", errorResponse["error"])
	assert.Equal(t, "Failed to get daily species data", errorResponse["message"])
	assert.InDelta(t, http.StatusInternalServerError, errorResponse["code"], 0.01)
}

// TestGetDailySpeciesSummary_BatchQueryError tests that batch query errors are properly propagated
func TestGetDailySpeciesSummary_BatchQueryError(t *testing.T) {
	t.Parallel()
	// Setup using the proper test environment
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	testDate := "2025-03-07"
	mockNotes := []datastore.Note{
		{CommonName: "American Crow", ScientificName: "Corvus brachyrhynchos", Confidence: 0.85},
	}

	// Mock successful GetTopBirdsData call
	mockDS.On("GetTopBirdsData", mock.Anything, testDate, 0.0, 0).Return(mockNotes, nil)

	// Mock GetBatchHourlyOccurrences to return an error
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, testDate, mock.Anything, 0.0).Return(
		map[string][24]int{}, errors.New("batch query failed: connection timeout"))

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date="+testDate, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)
	require.NoError(t, err)

	// Verify error response
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var errorResponse map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check that the error response contains expected fields - in non-debug mode,
	// Error field uses sanitized message instead of raw err.Error()
	assert.Contains(t, errorResponse, "error")
	assert.Equal(t, "Failed to process daily species data", errorResponse["error"])

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestAnalytics_ResolvesLocalizedCommonNameToScientific verifies that after wiring a
// batch-capable resolver and calling UpdateCommonNameMap, the reverse lookup for a
// localized bat name (which has no embedded common name in the label) returns the
// correct scientific name. This guards the map-builder wiring for the analytics path.
func TestAnalytics_ResolvesLocalizedCommonNameToScientific(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "localized-name-resolution")

	_, _, c := setupAnalyticsTestEnvironmentWithBatName(t)

	got, hit := c.resolveSpeciesToScientific(batCommonName)
	require.True(t, hit, "expected a reverse-map hit for the Finnish bat name")
	assert.Equal(t, batScientificName, got)
}

// TestAnalytics_HourlyHandlerPassesResolvedSpeciesToDatastore verifies that when
// GetHourlyAnalytics receives a localized common name, the resolved scientific name
// is what actually reaches the datastore. The API response keeps the user-facing
// string; only the datastore call uses the resolved value.
func TestAnalytics_HourlyHandlerPassesResolvedSpeciesToDatastore(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "localized-name-resolution")

	e, mockDS, controller := setupAnalyticsTestEnvironmentWithBatName(t)

	const (
		localizedName  = "mopsilepakko"
		scientificName = "Barbastella barbastellus"
		date           = testDate
	)

	// Capture the species argument that actually reaches the datastore.
	var capturedSpecies string
	mockDS.EXPECT().
		GetHourlyAnalyticsData(mock.Anything, date, mock.AnythingOfType("string")).
		RunAndReturn(func(_ context.Context, _ string, species string) ([]datastore.HourlyAnalyticsData, error) {
			capturedSpecies = species
			return []datastore.HourlyAnalyticsData{}, nil
		}).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/time/hourly", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/analytics/time/hourly")
	ctx.QueryParams().Set("date", date)
	ctx.QueryParams().Set("species", localizedName)

	err := controller.GetHourlyAnalytics(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	// The resolved scientific name must reach the datastore, not the localized name.
	assert.Equal(t, scientificName, capturedSpecies,
		"datastore must receive the scientific name, not the localized common name")

	mockDS.AssertExpectations(t)
}

// TestAnalytics_TimeOfDayDistributionResolvesLocalizedSpecies verifies that
// GetTimeOfDayDistribution passes the scientific name to the datastore when given
// a localized common name (Finnish bat name "mopsilepakko"). Before the fix the
// resolver was not wired and the localized string reached the datastore unchanged.
func TestAnalytics_TimeOfDayDistributionResolvesLocalizedSpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "localized-name-resolution")

	e, mockDS, controller := setupAnalyticsTestEnvironmentWithBatName(t)

	const (
		startDate      = "2023-01-01"
		endDate        = "2023-01-31"
		localizedName  = "mopsilepakko"
		scientificName = "Barbastella barbastellus"
	)

	var capturedSpecies string
	mockDS.EXPECT().
		GetHourlyDistribution(mock.Anything, startDate, endDate, mock.AnythingOfType("string")).
		RunAndReturn(func(_ context.Context, _, _ string, species string) ([]datastore.HourlyDistributionData, error) {
			capturedSpecies = species
			return []datastore.HourlyDistributionData{}, nil
		}).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/time/distribution/hourly", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/analytics/time/distribution/hourly")
	ctx.QueryParams().Set("start_date", startDate)
	ctx.QueryParams().Set("end_date", endDate)
	ctx.QueryParams().Set("species", localizedName)

	err := controller.GetTimeOfDayDistribution(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, scientificName, capturedSpecies,
		"datastore must receive the scientific name, not the localized common name")

	mockDS.AssertExpectations(t)
}

// TestAnalytics_BatchDailySpeciesResolvesLocalizedSpecies verifies that
// GetBatchDailySpeciesData resolves a localized common name to its scientific name
// before querying the datastore, while keeping the user-facing name as the response key.
func TestAnalytics_BatchDailySpeciesResolvesLocalizedSpecies(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "localized-name-resolution")

	e, mockDS, controller := setupAnalyticsTestEnvironmentWithBatName(t)

	const (
		startDate      = "2023-01-01"
		endDate        = "2023-01-31"
		localizedName  = "mopsilepakko"
		scientificName = "Barbastella barbastellus"
	)

	var capturedSpecies string
	mockDS.EXPECT().
		GetDailyAnalyticsData(mock.Anything, startDate, endDate, mock.AnythingOfType("string")).
		RunAndReturn(func(_ context.Context, _, _, species string) ([]datastore.DailyAnalyticsData, error) {
			capturedSpecies = species
			return []datastore.DailyAnalyticsData{}, nil
		}).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/time/daily/batch", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/analytics/time/daily/batch")
	ctx.QueryParams().Set("start_date", startDate)
	ctx.QueryParams().Set("end_date", endDate)
	ctx.QueryParams()["species"] = []string{localizedName}

	err := controller.GetBatchDailySpeciesData(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// The datastore must have received the scientific name, not the localized name.
	assert.Equal(t, scientificName, capturedSpecies,
		"datastore must receive the scientific name, not the localized common name")

	// The response is a map[string]SpeciesDailyData keyed by user-facing species name.
	// The localized name must be the key, not the scientific name.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	_, hasLocalizedKey := resp[localizedName]
	assert.True(t, hasLocalizedKey,
		"response must be keyed by the user-facing localized name %q, not the scientific name", localizedName)

	mockDS.AssertExpectations(t)
}

func TestApplySpeciesStatusToSummary_FlagPassThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		status            speciestracker.SpeciesStatus
		expectNewSpecies  bool
		expectNewThisYear bool
		expectNewSeason   bool
	}{
		{
			name: "all flags true when tracker reports new",
			status: speciestracker.SpeciesStatus{
				IsNew:           true,
				DaysSinceFirst:  0,
				IsNewThisYear:   true,
				IsNewThisSeason: true,
				CurrentSeason:   "spring",
			},
			expectNewSpecies:  true,
			expectNewThisYear: true,
			expectNewSeason:   true,
		},
		{
			name: "all flags true when tracker reports in-window",
			status: speciestracker.SpeciesStatus{
				IsNew:           true,
				DaysSinceFirst:  3,
				IsNewThisYear:   true,
				IsNewThisSeason: true,
				CurrentSeason:   "spring",
			},
			expectNewSpecies:  true,
			expectNewThisYear: true,
			expectNewSeason:   true,
		},
		{
			name: "all flags false when tracker reports out-of-window",
			status: speciestracker.SpeciesStatus{
				IsNew:           false,
				DaysSinceFirst:  8,
				IsNewThisYear:   false,
				IsNewThisSeason: false,
				CurrentSeason:   "summer",
			},
			expectNewSpecies:  false,
			expectNewThisYear: false,
			expectNewSeason:   false,
		},
		{
			name: "mixed flags passed through independently",
			status: speciestracker.SpeciesStatus{
				IsNew:           false,
				DaysSinceFirst:  30,
				IsNewThisYear:   true,
				IsNewThisSeason: true,
				CurrentSeason:   "winter",
			},
			expectNewSpecies:  false,
			expectNewThisYear: true,
			expectNewSeason:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var summary SpeciesDailySummary
			applySpeciesStatusToSummary(&summary, &tt.status)
			assert.Equal(t, tt.expectNewSpecies, summary.IsNewSpecies, "IsNewSpecies")
			assert.Equal(t, tt.expectNewThisYear, summary.IsNewThisYear, "IsNewThisYear")
			assert.Equal(t, tt.expectNewSeason, summary.IsNewThisSeason, "IsNewThisSeason")
			assert.Equal(t, tt.status.DaysSinceFirst, summary.DaysSinceFirstSeen, "DaysSinceFirstSeen")
			assert.Equal(t, tt.status.CurrentSeason, summary.CurrentSeason, "CurrentSeason")
		})
	}
}
