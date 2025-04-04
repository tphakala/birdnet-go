// analytics_test.go: Package api provides tests for API v2 analytics endpoints.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"errors"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// TestGetSpeciesSummary tests the species summary endpoint
func TestGetSpeciesSummary(t *testing.T) {
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
	mockDS.On("GetSpeciesSummaryData").Return(mockSummaryData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species", http.NoBody)
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
		assert.Equal(t, "amerob", response[0]["species_code"])
		assert.Equal(t, float64(42), response[0]["count"])
		assert.Equal(t, "Cyanocitta cristata", response[1]["scientific_name"])
		assert.Equal(t, "Blue Jay", response[1]["common_name"])
		assert.Equal(t, "blujay", response[1]["species_code"])
		assert.Equal(t, float64(27), response[1]["count"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetHourlyAnalytics tests the hourly analytics endpoint
func TestGetHourlyAnalytics(t *testing.T) {
	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

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
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/hourly?date=2023-01-01&species=Turdus+migratorius", http.NoBody)
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

		// Parse response body - the actual implementation returns a single object, not an array
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Equal(t, date, response["date"])
		assert.Equal(t, species, response["species"])

		// Check the counts array
		counts, ok := response["counts"].([]interface{})
		assert.True(t, ok, "Expected counts to be an array")
		assert.Len(t, counts, 24, "Expected 24 hours in counts array")

		// Check specific hour counts that were set in our mock
		assert.Equal(t, float64(5), counts[0], "Hour 0 should have 5 counts")
		assert.Equal(t, float64(3), counts[1], "Hour 1 should have 3 counts")

		// Check the total
		assert.Equal(t, float64(8), response["total"], "Total should be sum of all counts")
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetDailyAnalytics tests the daily analytics endpoint
func TestGetDailyAnalytics(t *testing.T) {
	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

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
		"/api/v2/analytics/daily?start_date=2023-01-01&end_date=2023-01-07&species=Turdus+migratorius", http.NoBody)
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

		// Parse response body - the actual implementation returns an object with a 'data' array
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response metadata
		assert.Equal(t, startDate, response["start_date"])
		assert.Equal(t, endDate, response["end_date"])
		assert.Equal(t, species, response["species"])
		assert.Equal(t, float64(20), response["total"]) // 12 + 8 = 20

		// Check data array
		data, ok := response["data"].([]interface{})
		assert.True(t, ok, "Expected data to be an array")
		assert.Len(t, data, 2, "Expected 2 items in data array")

		// Check first data item
		item1 := data[0].(map[string]interface{})
		assert.Equal(t, "2023-01-01", item1["date"])
		assert.Equal(t, float64(12), item1["count"])

		// Check second data item
		item2 := data[1].(map[string]interface{})
		assert.Equal(t, "2023-01-02", item2["date"])
		assert.Equal(t, float64(8), item2["count"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetDailyAnalyticsWithoutSpecies tests the daily analytics endpoint when no species is provided
// This tests the aggregated data behavior, which represents detection trends across all species
func TestGetDailyAnalyticsWithoutSpecies(t *testing.T) {
	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

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
		"/api/v2/analytics/daily?start_date=2023-01-01&end_date=2023-01-07", http.NoBody)
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
	e, mockDS, controller := setupTestEnvironment(t)

	// Test cases
	testCases := []struct {
		name        string
		endpoint    string
		handler     func(echo.Context) error
		queryParams map[string]string
		expectCode  int
		expectError string
		mockSetup   func(*mock.Mock) // Add mockSetup function to configure mocks for each test case
	}{
		{
			name:     "Missing date for hourly analytics",
			endpoint: "/api/v2/analytics/hourly",
			handler:  controller.GetHourlyAnalytics,
			queryParams: map[string]string{
				"species": "Turdus migratorius",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Missing required parameter: date",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Missing species for hourly analytics",
			endpoint: "/api/v2/analytics/hourly",
			handler:  controller.GetHourlyAnalytics,
			queryParams: map[string]string{
				"date": "2023-01-01",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Missing required parameter: species",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Invalid date format for hourly analytics",
			endpoint: "/api/v2/analytics/hourly",
			handler:  controller.GetHourlyAnalytics,
			queryParams: map[string]string{
				"date":    "01-01-2023", // Wrong format
				"species": "Turdus migratorius",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Missing start_date for daily analytics",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"end_date": "2023-01-07",
				"species":  "Turdus migratorius",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Missing required parameter: start_date",
			mockSetup:   func(m *mock.Mock) {},
		},
		// Enhanced date format validation tests
		{
			name:     "Invalid month in start_date (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-13-01", // Month 13 is invalid
				"end_date":   "2023-12-31",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Invalid day in start_date (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-04-31", // April has 30 days
				"end_date":   "2023-05-01",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Invalid day in end_date (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-02-01",
				"end_date":   "2023-02-30", // February never has 30 days
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid end_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Invalid leap year date (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-02-29", // 2023 is not a leap year
				"end_date":   "2023-03-01",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Valid leap year date (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2024-02-29", // 2024 is a leap year
				"end_date":   "2024-03-01",
			},
			expectCode: http.StatusOK, // This should be valid
			mockSetup: func(m *mock.Mock) {
				// Add mock for GetDailyAnalyticsData since this test case passes validation
				m.On("GetDailyAnalyticsData", "2024-02-29", "2024-03-01", "").Return([]datastore.DailyAnalyticsData{}, nil)
			},
		},
		{
			name:     "Date with text injection (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-01-01' OR '1'='1", // SQL injection attempt
				"end_date":   "2023-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with zero month (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-00-01", // Month 0 is invalid
				"end_date":   "2023-01-01",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with zero day (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-01-00", // Day 0 is invalid
				"end_date":   "2023-01-01",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with spaces (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023 01 01", // Spaces instead of hyphens
				"end_date":   "2023-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with slashes (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023/01/01", // Slashes instead of hyphens
				"end_date":   "2023-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with dots (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023.01.01", // Dots instead of hyphens
				"end_date":   "2023-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with reversed format (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "01-01-2023", // DD-MM-YYYY format
				"end_date":   "07-01-2023",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with short year (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "23-01-01", // YY-MM-DD format
				"end_date":   "23-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with ISO 8601 format with time (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "2023-01-01T00:00:00Z", // ISO 8601 with time
				"end_date":   "2023-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with negative year (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "-2023-01-01", // Negative year
				"end_date":   "2023-01-07",
			},
			expectCode:  http.StatusBadRequest,
			expectError: "Invalid start_date format. Use YYYY-MM-DD",
			mockSetup:   func(m *mock.Mock) {},
		},
		{
			name:     "Date with very large year (daily analytics)",
			endpoint: "/api/v2/analytics/daily",
			handler:  controller.GetDailyAnalytics,
			queryParams: map[string]string{
				"start_date": "99999-01-01", // Very large year
				"end_date":   "99999-01-07",
			},
			expectCode: http.StatusBadRequest, // Go's time.Parse cannot handle years this large
			// No mock setup needed as validation will fail
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock expectations
			mockDS.ExpectedCalls = nil

			// Setup mock expectations for this test case
			if tc.mockSetup != nil {
				tc.mockSetup(&mockDS.Mock)
			}

			// Create request with query parameters
			req := httptest.NewRequest(http.MethodGet, tc.endpoint, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath(tc.endpoint)

			// Add query parameters
			q := req.URL.Query()
			for k, v := range tc.queryParams {
				q.Add(k, v)
			}
			req.URL.RawQuery = q.Encode()

			// Call handler
			err := tc.handler(c)

			// Check response
			if tc.expectCode == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectCode, rec.Code)
			} else {
				// For error cases, check the error message
				if err != nil {
					// Direct error from handler
					var httpErr *echo.HTTPError
					if errors.As(err, &httpErr) {
						assert.Equal(t, tc.expectCode, httpErr.Code)
						if tc.expectError != "" {
							assert.Contains(t, fmt.Sprintf("%v", httpErr.Message), tc.expectError)
						}
					}
				} else {
					// Error handled by controller and returned as JSON
					assert.Equal(t, tc.expectCode, rec.Code)
					if tc.expectError != "" {
						var errorResp map[string]interface{}
						err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
						assert.NoError(t, err)
						if errorResp["error"] != nil {
							assert.Contains(t, errorResp["error"].(string), tc.expectError)
						}
					}
				}
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestGetDailySpeciesSummary_MultipleDetections tests that the GetDailySpeciesSummary function
// correctly counts multiple detections of the same species
func TestGetDailySpeciesSummary_MultipleDetections(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore
	mockDS := &MockDataStoreV2{
		GetTopBirdsDataFunc: func(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
			// Return multiple notes with the same species to simulate multiple detections
			return []datastore.Note{
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
					SpeciesCode:    "AMCRO",
					ScientificName: "Corvus brachyrhynchos",
					CommonName:     "American Crow",
					Confidence:     0.85,
					Date:           "2025-03-07",
					Time:           "09:30:00",
				},
				{
					ID:             3,
					SpeciesCode:    "AMCRO",
					ScientificName: "Corvus brachyrhynchos",
					CommonName:     "American Crow",
					Confidence:     0.95,
					Date:           "2025-03-07",
					Time:           "14:45:00",
				},
				{
					ID:             4,
					SpeciesCode:    "RBWO",
					ScientificName: "Melanerpes carolinus",
					CommonName:     "Red-bellied Woodpecker",
					Confidence:     0.8,
					Date:           "2025-03-07",
					Time:           "10:20:00",
				},
				{
					ID:             5,
					SpeciesCode:    "RBWO",
					ScientificName: "Melanerpes carolinus",
					CommonName:     "Red-bellied Woodpecker",
					Confidence:     0.75,
					Date:           "2025-03-07",
					Time:           "16:05:00",
				},
			}, nil
		},
		GetHourlyOccurrencesFunc: func(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
			// Return appropriate hourly counts based on the species
			var hourlyCounts [24]int

			switch commonName {
			case "American Crow":
				// Set counts for hours 8, 9, and 14 for American Crow
				hourlyCounts[8] = 1  // 08:15:00
				hourlyCounts[9] = 1  // 09:30:00
				hourlyCounts[14] = 1 // 14:45:00
			case "Red-bellied Woodpecker":
				// Set counts for hours 10 and 16 for Red-bellied Woodpecker
				hourlyCounts[10] = 1 // 10:20:00
				hourlyCounts[16] = 1 // 16:05:00
			}

			return hourlyCounts, nil
		},
	}

	// Create a mock image provider
	mockImageProvider := &TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			return imageprovider.BirdImage{
				ScientificName: scientificName,
				URL:            "http://example.com/" + scientificName + ".jpg",
			}, nil
		},
	}

	// Create a bird image cache with our mock provider
	imageCache := imageprovider.InitCache(mockImageProvider, NewTestMetrics(t), mockDS)

	// Create a controller with our mocks
	controller := &Controller{
		DS:             mockDS,
		BirdImageCache: imageCache,
		logger:         log.New(io.Discard, "", 0), // Add logger
	}

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Verify we got the expected number of species
	assert.Equal(t, 2, len(response))

	// Find the American Crow in the response
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

	// Verify the American Crow has the correct count (3)
	assert.NotNil(t, amcro, "American Crow should be in the response")
	assert.Equal(t, 3, amcro.Count, "American Crow should have 3 detections")

	// Verify the correct hourly distribution for American Crow
	expectedAmcroHourly := make([]int, 24)
	expectedAmcroHourly[8] = 1  // 08:15:00
	expectedAmcroHourly[9] = 1  // 09:30:00
	expectedAmcroHourly[14] = 1 // 14:45:00
	assert.Equal(t, expectedAmcroHourly, amcro.HourlyCounts)

	// Verify the Red-bellied Woodpecker has the correct count (2)
	assert.NotNil(t, rbwo, "Red-bellied Woodpecker should be in the response")
	assert.Equal(t, 2, rbwo.Count, "Red-bellied Woodpecker should have 2 detections")

	// Verify the correct hourly distribution for Red-bellied Woodpecker
	expectedRbwoHourly := make([]int, 24)
	expectedRbwoHourly[10] = 1 // 10:20:00
	expectedRbwoHourly[16] = 1 // 16:05:00
	assert.Equal(t, expectedRbwoHourly, rbwo.HourlyCounts)

	// Close the image cache to clean up resources
	imageCache.Close()
}

// TestGetDailySpeciesSummary_SingleDetection tests that the GetDailySpeciesSummary function
// correctly handles the case where each species has only one detection
func TestGetDailySpeciesSummary_SingleDetection(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore
	mockDS := &MockDataStoreV2{
		GetTopBirdsDataFunc: func(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
			// Return one note per species to simulate single detections
			return []datastore.Note{
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
			}, nil
		},
		GetHourlyOccurrencesFunc: func(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
			// Return appropriate hourly counts based on the species
			var hourlyCounts [24]int

			switch commonName {
			case "American Crow":
				// Set count for hour 8 for American Crow
				hourlyCounts[8] = 1 // 08:15:00
			case "Red-bellied Woodpecker":
				// Set count for hour 10 for Red-bellied Woodpecker
				hourlyCounts[10] = 1 // 10:20:00
			}

			return hourlyCounts, nil
		},
	}

	// Create a mock image provider
	mockImageProvider := &TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			return imageprovider.BirdImage{
				ScientificName: scientificName,
				URL:            "http://example.com/" + scientificName + ".jpg",
			}, nil
		},
	}

	// Create a bird image cache with our mock provider
	imageCache := imageprovider.InitCache(mockImageProvider, NewTestMetrics(t), mockDS)

	// Create a controller with our mocks
	controller := &Controller{
		DS:             mockDS,
		BirdImageCache: imageCache,
		logger:         log.New(io.Discard, "", 0), // Add logger
	}

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Verify we got the expected number of species
	assert.Equal(t, 2, len(response))

	// Verify each species has a count of 1
	for _, species := range response {
		assert.Equal(t, 1, species.Count, "%s should have 1 detection", species.CommonName)
	}

	// Close the image cache to clean up resources
	imageCache.Close()
}

// TestGetDailySpeciesSummary_EmptyResult tests that the GetDailySpeciesSummary function
// correctly handles the case where no detections are found
func TestGetDailySpeciesSummary_EmptyResult(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore that returns no detections
	mockDS := &MockDataStoreV2{
		GetTopBirdsDataFunc: func(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
			return []datastore.Note{}, nil
		},
		GetHourlyOccurrencesFunc: func(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
			// Return empty hourly counts since there are no detections
			return [24]int{}, nil
		},
	}

	// Create a controller with our mock
	controller := &Controller{
		DS:     mockDS,
		logger: log.New(io.Discard, "", 0), // Add logger
	}

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Verify we got an empty result
	assert.Equal(t, 0, len(response))
}

// TestGetDailySpeciesSummary_TimeHandling tests that the GetDailySpeciesSummary function
// correctly handles the first and latest detection times
func TestGetDailySpeciesSummary_TimeHandling(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore
	mockDS := &MockDataStoreV2{
		GetTopBirdsDataFunc: func(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
			// Return multiple notes with the same species to test time handling
			return []datastore.Note{
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
			}, nil
		},
		GetHourlyOccurrencesFunc: func(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
			// Return hourly counts for American Crow
			var hourlyCounts [24]int

			if commonName == "American Crow" {
				hourlyCounts[6] = 1  // 06:30:00
				hourlyCounts[8] = 1  // 08:15:00
				hourlyCounts[21] = 1 // 21:45:00
			}

			return hourlyCounts, nil
		},
	}

	// Create a controller with our mock
	controller := &Controller{
		DS:     mockDS,
		logger: log.New(io.Discard, "", 0), // Add logger
	}

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Verify we got one species
	assert.Equal(t, 1, len(response))

	// Verify the first and latest times are correct
	species := response[0]
	assert.Equal(t, "06:30:00", species.First, "First detection should be 06:30:00")
	assert.Equal(t, "21:45:00", species.Latest, "Latest detection should be 21:45:00")
}

// TestGetDailySpeciesSummary_ConfidenceFilter tests that the GetDailySpeciesSummary function
// correctly filters by confidence level
func TestGetDailySpeciesSummary_ConfidenceFilter(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Track if our filter was properly applied
	var appliedMinConfidence float64

	// Create a mock datastore
	mockDS := &MockDataStoreV2{
		GetTopBirdsDataFunc: func(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
			// Save the applied confidence filter
			appliedMinConfidence = minConfidenceNormalized

			// Return notes with varying confidence levels
			return []datastore.Note{
				{
					ID:             1,
					SpeciesCode:    "AMCRO",
					ScientificName: "Corvus brachyrhynchos",
					CommonName:     "American Crow",
					Confidence:     0.9, // High confidence
					Date:           "2025-03-07",
					Time:           "08:15:00",
				},
				{
					ID:             2,
					SpeciesCode:    "RBWO",
					ScientificName: "Melanerpes carolinus",
					CommonName:     "Red-bellied Woodpecker",
					Confidence:     0.6, // Medium confidence
					Date:           "2025-03-07",
					Time:           "10:20:00",
				},
				{
					ID:             3,
					SpeciesCode:    "BCCH",
					ScientificName: "Poecile atricapillus",
					CommonName:     "Black-capped Chickadee",
					Confidence:     0.3, // Low confidence
					Date:           "2025-03-07",
					Time:           "12:45:00",
				},
			}, nil
		},
		GetHourlyOccurrencesFunc: func(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
			// Return hourly counts based on confidence filter
			var hourlyCounts [24]int

			// Only return counts for species that meet the confidence threshold
			switch commonName {
			case "American Crow":
				if minConfidenceNormalized <= 0.9 {
					hourlyCounts[8] = 1 // 08:15:00
				}
			case "Red-bellied Woodpecker":
				if minConfidenceNormalized <= 0.6 {
					hourlyCounts[10] = 1 // 10:20:00
				}
			case "Black-capped Chickadee":
				if minConfidenceNormalized <= 0.3 {
					hourlyCounts[12] = 1 // 12:45:00
				}
			}

			return hourlyCounts, nil
		},
	}

	// Create a controller with our mock
	controller := &Controller{
		DS:     mockDS,
		logger: log.New(io.Discard, "", 0), // Add logger
	}

	// Test with a confidence threshold of "70"
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07&min_confidence=70", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the threshold was correctly normalized (70% -> 0.7)
	assert.Equal(t, 0.7, appliedMinConfidence)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Verify the response only includes species with confidence >= 0.7
	for _, species := range response {
		assert.NotEqual(t, "Black-capped Chickadee", species.CommonName)
		assert.NotEqual(t, "Red-bellied Woodpecker", species.CommonName)
		assert.Equal(t, "American Crow", species.CommonName)
	}
}

// TestGetDailySpeciesSummary_LimitParameter tests that the GetDailySpeciesSummary function
// correctly applies the limit parameter
func TestGetDailySpeciesSummary_LimitParameter(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore
	mockDS := &MockDataStoreV2{
		GetTopBirdsDataFunc: func(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
			// Return multiple species to test limiting
			return []datastore.Note{
				{
					ID:             1,
					SpeciesCode:    "AMCRO",
					ScientificName: "Corvus brachyrhynchos",
					CommonName:     "American Crow",
					Confidence:     0.95,
					Date:           "2025-03-07",
					Time:           "08:15:00",
				},
				{
					ID:             2,
					SpeciesCode:    "RBWO",
					ScientificName: "Melanerpes carolinus",
					CommonName:     "Red-bellied Woodpecker",
					Confidence:     0.90,
					Date:           "2025-03-07",
					Time:           "10:20:00",
				},
				{
					ID:             3,
					SpeciesCode:    "BCCH",
					ScientificName: "Poecile atricapillus",
					CommonName:     "Black-capped Chickadee",
					Confidence:     0.85,
					Date:           "2025-03-07",
					Time:           "12:45:00",
				},
				{
					ID:             4,
					SpeciesCode:    "AMGO",
					ScientificName: "Spinus tristis",
					CommonName:     "American Goldfinch",
					Confidence:     0.80,
					Date:           "2025-03-07",
					Time:           "14:30:00",
				},
			}, nil
		},
		GetHourlyOccurrencesFunc: func(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
			// Return hourly counts for each species
			var hourlyCounts [24]int

			switch commonName {
			case "American Crow":
				hourlyCounts[8] = 1 // 08:15:00
			case "Red-bellied Woodpecker":
				hourlyCounts[10] = 1 // 10:20:00
			case "Black-capped Chickadee":
				hourlyCounts[12] = 1 // 12:45:00
			case "American Goldfinch":
				hourlyCounts[14] = 1 // 14:30:00
			}

			return hourlyCounts, nil
		},
	}

	// Create a controller with our mock
	controller := &Controller{
		DS:     mockDS,
		logger: log.New(io.Discard, "", 0), // Add logger
	}

	// Create a request with a limit of 2
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07&limit=2", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.QueryParams().Set("limit", "2")

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// Verify no error occurred
	assert.NoError(t, err)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Verify we got exactly 2 species (limited)
	assert.Equal(t, 2, len(response))
}

// TestGetDailySpeciesSummary_DatabaseError tests that the GetDailySpeciesSummary function
// correctly handles database errors
func TestGetDailySpeciesSummary_DatabaseError(t *testing.T) {
	// Setup using the proper test environment
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Override the GetTopBirdsData function to return an error
	mockDS.On("GetTopBirdsData", mock.Anything, mock.Anything).Return([]datastore.Note{}, errors.New("database connection error"))

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/daily?date=2025-03-07", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.GetDailySpeciesSummary(c)

	// The controller's HandleError method returns a JSON response, not an error
	assert.NoError(t, err)

	// Verify the response status code is 500 Internal Server Error
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse the error response
	var errorResponse map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	assert.NoError(t, err)

	// Check that the error response contains the expected fields
	assert.Contains(t, errorResponse, "error")
	assert.Contains(t, errorResponse, "message")
	assert.Contains(t, errorResponse, "code")

	// Check the error message
	assert.Contains(t, errorResponse["error"].(string), "database connection error")
	assert.Equal(t, "Failed to get daily species data", errorResponse["message"])
	assert.Equal(t, float64(http.StatusInternalServerError), errorResponse["code"])
}
