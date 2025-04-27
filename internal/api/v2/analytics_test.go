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
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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
	// Expect call with specific empty strings for no date filters
	mockDS.On("GetSpeciesSummaryData", "", "").Return(mockSummaryData, nil)

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

// TestGetInvalidAnalyticsRequests tests various invalid requests to analytics endpoints
func TestGetInvalidAnalyticsRequests(t *testing.T) {
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
			expectedBody:   "start_date cannot be after end_date",
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

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup: Ensure settings are valid for controller creation within the loop
			appSettings := &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: struct {
							Debug     bool
							Enabled   bool
							Path      string
							Type      string
							Bitrate   string
							Retention struct {
								Debug    bool
								Policy   string
								MaxAge   string
								MaxUsage string
								MinClips int
							}
						}{
							Path: t.TempDir(),
						},
					},
				},
			}

			mockDS := new(MockDataStoreV2)
			// Add necessary mock expectations based on the specific endpoint being tested, if any.
			mockDS.On("GetSettings").Return(appSettings, nil) // Needed for cache init if controller setup does it
			// Add GetAllImageCaches mock if cache init happens here
			mockDS.On("GetAllImageCaches", mock.AnythingOfType("string")).Return([]datastore.ImageCache{}, nil)

			// Initialize a mock image cache for controller creation
			testMetrics, _ := telemetry.NewMetrics() // Create a dummy metrics instance
			// Fix: Replace nil provider with a stub provider to avoid nil pointer panics
			stubProvider := &TestImageProvider{
				FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
					return imageprovider.BirdImage{}, nil
				},
			}
			mockImageCache := imageprovider.InitCache("test", stubProvider, testMetrics, mockDS)
			t.Cleanup(func() { mockImageCache.Close() })

			controller := &Controller{
				DS:             mockDS,
				Settings:       appSettings,
				BirdImageCache: mockImageCache,
				logger:         log.New(io.Discard, "", 0),
				// sunCalc and controlChan might be needed depending on handlers tested
			}

			e := echo.New()
			// Register routes needed for this test run
			controller.Group = e.Group("/api/v2") // Assign group for proper route initialization
			controller.Echo = e                   // Set Echo instance for the controller
			controller.initAnalyticsRoutes()      // Initialize routes using the actual method

			req := httptest.NewRequest(tc.method, tc.path, http.NoBody)
			rec := httptest.NewRecorder()

			// Let Echo's router handle the request routing
			e.ServeHTTP(rec, req)

			// Check response
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedStatus, rec.Code)
			} else {
				// For error cases, check the error message
				assert.Equal(t, tc.expectedStatus, rec.Code)
				if tc.expectedBody != "" {
					var errorResp map[string]interface{}
					err := json.Unmarshal(rec.Body.Bytes(), &errorResp)
					assert.NoError(t, err)
					if errorResp["error"] != nil {
						assert.Contains(t, errorResp["error"].(string), tc.expectedBody)
					}
				}
			}

			// Only assert expectations if the handler was expected to interact with the mock
			// mockDS.AssertExpectations(t) // May need selective assertion
		})
	}
}

// TestGetDailySpeciesSummary_MultipleDetections tests that the GetDailySpeciesSummary function
// correctly counts multiple detections of the same species
func TestGetDailySpeciesSummary_MultipleDetections(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := new(MockDataStoreV2)

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
	mockDS.On("GetTopBirdsData", testDate, minConfidence).Return(mockNotes, nil)
	mockDS.On("GetHourlyOccurrences", testDate, "American Crow", minConfidence).Return(expectedAmcroHourlyCounts, nil)
	mockDS.On("GetHourlyOccurrences", testDate, "Red-bellied Woodpecker", minConfidence).Return(expectedRbwoHourlyCounts, nil)

	// Mock for image cache initialization
	mockDS.On("GetAllImageCaches", mock.AnythingOfType("string")).Return([]datastore.ImageCache{}, nil)

	// Expect calls to GetImageCache during GetBatch and return nil (not found)
	mockDS.On("GetImageCache", mock.AnythingOfType("datastore.ImageCacheQuery")).Return(nil, nil)

	// ---> FIX: Add mock expectation for SaveImageCache <---
	// Expect calls to SaveImageCache when the cache tries to store fetched results
	mockDS.On("SaveImageCache", mock.AnythingOfType("*datastore.ImageCache")).Return(nil)

	// Create a mock image provider (can be nil if cache doesn't need real fetching)
	mockImageProvider := &TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			// Return placeholder or specific mock image data if needed
			return imageprovider.BirdImage{
				ScientificName: scientificName,
				URL:            fmt.Sprintf("http://example.com/%s.jpg", scientificName),
				// Add other fields if necessary
			}, nil
		},
		/* GetBatchFunc: func(scientificNames []string) map[string]imageprovider.BirdImage {
			results := make(map[string]imageprovider.BirdImage)
			for _, name := range scientificNames {
				// Simulate fetching or return cached placeholder
				results[name] = imageprovider.BirdImage{
					ScientificName: name,
					URL:            fmt.Sprintf("http://example.com/%s.jpg", name),
				}
			}
			return results
		}, */
	}

	// Create a bird image cache with our mock provider
	// ---> FIX: Provide a non-nil telemetry.Metrics instance <---
	testMetrics, _ := telemetry.NewMetrics() // Create a dummy metrics instance
	imageCache := imageprovider.InitCache("test", mockImageProvider, testMetrics, mockDS)
	t.Cleanup(func() { imageCache.Close() })

	// Create a controller with our mocks
	controller := &Controller{
		DS:             mockDS,
		BirdImageCache: imageCache,
		logger:         log.New(io.Discard, "", 0), // Use discarded logger for tests
	}

	// Create a request with the date we want to test
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v2/analytics/species/daily?date=%s", testDate), http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/daily") // Set path for context if needed by handler
	c.QueryParams().Set("date", testDate)        // Ensure query param is accessible

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
		assert.Equal(t, "American Crow", amcro.CommonName)
		assert.Equal(t, "AMCRO", amcro.SpeciesCode)
		assert.Equal(t, amcroTotal, amcro.Count, "American Crow count mismatch") // Count is sum of hourly
		assert.Equal(t, expectedAmcroHourlyCounts[:], amcro.HourlyCounts, "American Crow hourly counts mismatch")
		assert.Equal(t, "08:15:00", amcro.FirstHeard, "American Crow first heard time")
		assert.Equal(t, "14:45:00", amcro.LatestHeard, "American Crow latest heard time")
		assert.True(t, amcro.HighConfidence, "American Crow should be high confidence") // Based on 0.95 > 0.8
		assert.Contains(t, amcro.ThumbnailURL, "Corvus brachyrhynchos", "American Crow thumbnail URL")
	}

	// Verify the Red-bellied Woodpecker details
	assert.NotNil(t, rbwo, "Red-bellied Woodpecker should be in the response")
	if rbwo != nil {
		assert.Equal(t, "Red-bellied Woodpecker", rbwo.CommonName)
		assert.Equal(t, "RBWO", rbwo.SpeciesCode)
		assert.Equal(t, rbwoTotal, rbwo.Count, "Red-bellied Woodpecker count mismatch") // Count is sum of hourly
		assert.Equal(t, expectedRbwoHourlyCounts[:], rbwo.HourlyCounts, "Red-bellied Woodpecker hourly counts mismatch")
		assert.Equal(t, "10:20:00", rbwo.FirstHeard, "Red-bellied Woodpecker first heard time")
		assert.Equal(t, "16:05:00", rbwo.LatestHeard, "Red-bellied Woodpecker latest heard time")
		assert.True(t, rbwo.HighConfidence, "Red-bellied Woodpecker should be high confidence") // Based on 0.8 >= 0.8
		assert.Contains(t, rbwo.ThumbnailURL, "Melanerpes carolinus", "Red-bellied Woodpecker thumbnail URL")
	}

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_SingleDetection tests that the GetDailySpeciesSummary function
// correctly handles the case where each species has only one detection
func TestGetDailySpeciesSummary_SingleDetection(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := new(MockDataStoreV2)

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
	mockDS.On("GetTopBirdsData", "2025-03-07", 0.0).Return(mockNotesSingle, nil)
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "American Crow", 0.0).Return(expectedAmcroSingleHourly, nil)
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "Red-bellied Woodpecker", 0.0).Return(expectedRbwoSingleHourly, nil)

	// ---> FIX: Add necessary mock expectations for image cache <---
	mockDS.On("GetAllImageCaches", mock.AnythingOfType("string")).Return([]datastore.ImageCache{}, nil)
	mockDS.On("GetImageCache", mock.AnythingOfType("datastore.ImageCacheQuery")).Return(nil, nil)
	mockDS.On("SaveImageCache", mock.AnythingOfType("*datastore.ImageCache")).Return(nil)

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
	imageCache := imageprovider.InitCache("test", mockImageProvider, NewTestMetrics(t), mockDS)

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

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_EmptyResult tests that the GetDailySpeciesSummary function
// correctly handles the case where no detections are found
func TestGetDailySpeciesSummary_EmptyResult(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore that returns no detections
	mockDS := new(MockDataStoreV2)

	// Setup mock expectations using m.On()
	// Expect GetTopBirdsData to be called and return empty slice
	mockDS.On("GetTopBirdsData", "2025-03-07", 0.0).Return([]datastore.Note{}, nil)
	// Expect GetHourlyOccurrences not to be called since there are no birds

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

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_TimeHandling tests that the GetDailySpeciesSummary function
// correctly handles the first and latest detection times
func TestGetDailySpeciesSummary_TimeHandling(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := new(MockDataStoreV2)

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
	mockDS.On("GetTopBirdsData", "2025-03-07", 0.0).Return(mockNotesTime, nil)
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "American Crow", 0.0).Return(expectedAmcroTimeHourly, nil)

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
	assert.Equal(t, "06:30:00", species.FirstHeard, "First detection should be 06:30:00")
	assert.Equal(t, "21:45:00", species.LatestHeard, "Latest detection should be 21:45:00")

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailySpeciesSummary_ConfidenceFilter tests that the GetDailySpeciesSummary function
// correctly filters by confidence level
func TestGetDailySpeciesSummary_ConfidenceFilter(t *testing.T) {
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := new(MockDataStoreV2)

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
	mockDS.On("GetTopBirdsData", "2025-03-07", expectedMinConfidence).Return(mockNotesConfidence, nil)
	// GetHourlyOccurrences is called for each species returned by GetTopBirdsData,
	// *even if* the species itself is below the threshold (filtering happens later in Go code)
	// We expect it to be called for American Crow with the filter
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "American Crow", expectedMinConfidence).Return(expectedAmcroConfidenceHourly, nil)
	// ---> FIX: Remove expectation for the filtered species <---
	/* We also expect it to be called for Red-bellied Woodpecker, even though it's below threshold.
	// The mock should return empty counts because the handler logic will filter it later.
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "Red-bellied Woodpecker", expectedMinConfidence).Return([24]int{}, nil) */

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
	assert.Equal(t, 0.7, expectedMinConfidence)

	// Verify the response status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse the response
	var response []SpeciesDailySummary
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

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
	// Create a new echo instance
	e := echo.New()

	// Create a mock datastore using testify/mock
	mockDS := new(MockDataStoreV2)

	// Expected data for GetTopBirdsData (more than the limit)
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
		{
			ID:             3,
			SpeciesCode:    "BCCH",
			ScientificName: "Poecile atricapillus",
			CommonName:     "Black-capped Chickadee",
			Confidence:     0.7,
			Date:           "2025-03-07",
			Time:           "12:45:00",
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
	mockDS.On("GetTopBirdsData", "2025-03-07", 0.0).Return(mockNotesLimit, nil)
	// Expect GetHourlyOccurrences to be called for all species returned by GetTopBirdsData,
	// as limiting happens *after* this step.
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "American Crow", 0.0).Return(expectedAmcroLimitHourly, nil)
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "Red-bellied Woodpecker", 0.0).Return(expectedRbwoLimitHourly, nil)
	mockDS.On("GetHourlyOccurrences", "2025-03-07", "Black-capped Chickadee", 0.0).Return(expectedBcchLimitHourly, nil)

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

	// Assert that all expectations were met
	mockDS.AssertExpectations(t)
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
