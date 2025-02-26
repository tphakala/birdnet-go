// analytics_test.go: Package api provides tests for API v2 analytics endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestGetSpeciesSummary tests the species summary endpoint
func TestGetSpeciesSummary(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment()

	// Create mock data
	firstSeen := time.Now().AddDate(0, -1, 0)
	lastSeen := time.Now().AddDate(0, 0, -1)

	mockSummaryData := []datastore.SpeciesSummaryData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			Count:          42,
			FirstSeen:      firstSeen,
			LastSeen:       lastSeen,
			AvgConfidence:  0.75,
			MaxConfidence:  0.85,
		},
		{
			ScientificName: "Cyanocitta cristata",
			CommonName:     "Blue Jay",
			Count:          27,
			FirstSeen:      time.Now().AddDate(0, -2, 0),
			LastSeen:       time.Now(),
			AvgConfidence:  0.82,
			MaxConfidence:  0.92,
		},
	}

	// Setup mock expectations
	mockDS.On("GetSpeciesSummaryData").Return(mockSummaryData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.GetSpeciesSummary(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response []map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Len(t, response, 2)
		assert.Equal(t, "Turdus migratorius", response[0]["scientific_name"])
		assert.Equal(t, "American Robin", response[0]["common_name"])
		assert.Equal(t, float64(42), response[0]["count"])
		assert.Equal(t, "Cyanocitta cristata", response[1]["scientific_name"])
		assert.Equal(t, "Blue Jay", response[1]["common_name"])
		assert.Equal(t, float64(27), response[1]["count"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetHourlyAnalytics tests the hourly analytics endpoint
func TestGetHourlyAnalytics(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment()

	// Create mock data
	date := "2023-01-01"
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
	mockDS.On("GetHourlyAnalyticsData", date, species).Return(mockHourlyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/hourly?date=2023-01-01&species=Turdus+migratorius", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/hourly")
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

		// Parse response body
		var response []map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Len(t, response, 2)
		assert.Equal(t, float64(0), response[0]["hour"])
		assert.Equal(t, float64(5), response[0]["count"])

		assert.Equal(t, float64(1), response[1]["hour"])
		assert.Equal(t, float64(3), response[1]["count"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetDailyAnalytics tests the daily analytics endpoint
func TestGetDailyAnalytics(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment()

	// Create mock data
	startDate := "2023-01-01"
	endDate := "2023-01-07"
	species := "Turdus migratorius"

	mockDailyData := []datastore.DailyAnalyticsData{
		{
			Date:  "2023-01-01",
			Count: 12,
		},
		{
			Date:  "2023-01-02",
			Count: 8,
		},
	}

	// Setup mock expectations
	mockDS.On("GetDailyAnalyticsData", startDate, endDate, species).Return(mockDailyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/analytics/daily?start_date=2023-01-01&end_date=2023-01-07&species=Turdus+migratorius", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/daily")
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

		// Parse response body
		var response []map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Len(t, response, 2)
		assert.Equal(t, "2023-01-01", response[0]["date"])
		assert.Equal(t, float64(12), response[0]["count"])

		assert.Equal(t, "2023-01-02", response[1]["date"])
		assert.Equal(t, float64(8), response[1]["count"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetTrends tests the detection trends functionality
func TestGetTrends(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment()

	// Create mock data
	startDate := "2023-01-01"
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
	mockDS.On("GetDailyAnalyticsData", startDate, endDate, "").Return(mockDailyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/analytics/daily?start_date=2023-01-01&end_date=2023-01-07", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/daily")
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
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		data, ok := response["data"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, data, 3)
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetInvalidAnalyticsRequests tests analytics endpoints with invalid parameters
func TestGetInvalidAnalyticsRequests(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment()

	// Test cases
	testCases := []struct {
		name        string
		endpoint    string
		handler     func(echo.Context) error
		queryParams map[string]string
		expectCode  int
	}{
		{
			name:     "Missing date for hourly analytics",
			endpoint: "/api/v2/analytics/hourly",
			handler:  controller.GetHourlyAnalytics,
			queryParams: map[string]string{
				"species": "Turdus migratorius",
			},
			expectCode: http.StatusBadRequest,
		},
		{
			name:     "Missing species for hourly analytics",
			endpoint: "/api/v2/analytics/hourly",
			handler:  controller.GetHourlyAnalytics,
			queryParams: map[string]string{
				"date": "2023-01-01",
			},
			expectCode: http.StatusBadRequest,
		},
		{
			name:     "Invalid date format for hourly analytics",
			endpoint: "/api/v2/analytics/hourly",
			handler:  controller.GetHourlyAnalytics,
			queryParams: map[string]string{
				"date":    "01-01-2023", // Wrong format
				"species": "Turdus migratorius",
			},
			expectCode: http.StatusBadRequest,
		},
		{
			name:     "Missing start_date for daily analytics",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"end_date": "2023-01-07",
				"species":  "Turdus migratorius",
			},
			expectCode: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a request
			req := httptest.NewRequest(http.MethodGet, tc.endpoint, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath(tc.endpoint)

			// Set query parameters
			for key, value := range tc.queryParams {
				c.QueryParams().Set(key, value)
			}

			// Call handler
			err := tc.handler(c)

			// Check if error handling works as expected
			if httpErr, ok := err.(*echo.HTTPError); ok {
				assert.Equal(t, tc.expectCode, httpErr.Code)
			} else {
				assert.Equal(t, tc.expectCode, rec.Code)
			}
		})
	}
}
