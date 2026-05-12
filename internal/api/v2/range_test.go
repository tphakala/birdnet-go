// range_test.go: Package api provides tests for range filter API v2 endpoints.

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// MockBirdNET is a mock implementation of BirdNET for testing
type MockBirdNET struct {
	GetProbableSpeciesFunc func(date time.Time, week float32) ([]classifier.SpeciesScore, error)
}

func (m *MockBirdNET) GetProbableSpecies(date time.Time, week float32) ([]classifier.SpeciesScore, error) {
	if m.GetProbableSpeciesFunc != nil {
		return m.GetProbableSpeciesFunc(date, week)
	}
	// Default mock response
	return []classifier.SpeciesScore{
		{Label: "Turdus merula_Eurasian Blackbird", Score: 0.85},
		{Label: "Parus major_Great Tit", Score: 0.72},
	}, nil
}

// MockProcessor is a mock implementation of the processor for testing
type MockProcessor struct {
	BirdNETInstance *MockBirdNET
}

func (m *MockProcessor) GetBirdNET() *classifier.Orchestrator {
	// Since we can't easily mock the actual BirdNET struct, we'll return nil
	// and handle this in the test setup
	return nil
}

// setupRangeTestEnvironment creates a test environment specifically for range filter tests
func setupRangeTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()

	e, mockDS, controller := setupTestEnvironment(t)

	// Set up mock settings with range filter data
	controller.Settings.BirdNET.Latitude = 60.1699
	controller.Settings.BirdNET.Longitude = 24.9384
	controller.Settings.BirdNET.RangeFilter.Threshold = 0.01
	controller.Settings.BirdNET.RangeFilter.LastUpdated = time.Now()

	// Set the included species list directly for test setup
	controller.Settings.BirdNET.RangeFilter.Species = []string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
		"Corvus cornix_Hooded Crow",
	}

	return e, mockDS, controller
}

// TestGetRangeFilterSpeciesCount tests the species count endpoint
func TestGetRangeFilterSpeciesCount(t *testing.T) {
	// Setup
	e, _, controller := setupRangeTestEnvironment(t)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/range/species/count", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/range/species/count")

	// Test
	if assert.NoError(t, controller.GetRangeFilterSpeciesCount(c)) {
		// Check response code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response
		var response RangeFilterSpeciesCount
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, 3, response.Count)
		assert.InDelta(t, float32(0.01), response.Threshold, 0.001)
		assert.InDelta(t, 60.1699, response.Location.Latitude, 0.001)
		assert.InDelta(t, 24.9384, response.Location.Longitude, 0.001)
		assert.False(t, response.LastUpdated.IsZero())
	}
}

// TestGetRangeFilterSpeciesList tests the species list endpoint
func TestGetRangeFilterSpeciesList(t *testing.T) {
	// Setup
	e, _, controller := setupRangeTestEnvironment(t)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/range/species/list", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/range/species/list")

	// Test
	if assert.NoError(t, controller.GetRangeFilterSpeciesList(c)) {
		// Check response code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response
		var response RangeFilterSpeciesList
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, 3, response.Count)
		assert.Len(t, response.Species, 3)
		assert.InDelta(t, float32(0.01), response.Threshold, 0.001)

		// Check first species
		firstSpecies := response.Species[0]
		assert.Equal(t, "Turdus merula_Eurasian Blackbird", firstSpecies.Label)
		assert.Equal(t, "Turdus merula", firstSpecies.ScientificName)
		assert.Equal(t, "Eurasian Blackbird", firstSpecies.CommonName)
		// Score should be nil for current range filter species since individual scores are not available
		assert.Nil(t, firstSpecies.Score)
	}
}

// TestTestRangeFilterWithoutProcessor tests the test endpoint when processor is not available
func TestTestRangeFilterWithoutProcessor(t *testing.T) {
	// Setup
	e, _, controller := setupRangeTestEnvironment(t)

	// Ensure processor is nil
	controller.Processor = nil

	// Create test request
	testRequest := RangeFilterTestRequest{
		Latitude:  60.1699,
		Longitude: 24.9384,
		Threshold: 0.01,
		Date:      "2024-06-15",
	}

	requestBody, _ := json.Marshal(testRequest)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/range/species/test", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/range/species/test")

	// Test
	err := controller.TestRangeFilter(c)
	require.NoError(t, err)

	// Check response code (should be 500 due to missing processor)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var response ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response.Message, "BirdNET service not available")
}

// TestTestRangeFilterValidation tests input validation for the test endpoint
func TestTestRangeFilterValidation(t *testing.T) {
	// Setup
	e, _, controller := setupRangeTestEnvironment(t)

	tests := []struct {
		name           string
		request        RangeFilterTestRequest
		expectedStatus int
		expectedError  string
	}{
		{
			name: "Invalid latitude too low",
			request: RangeFilterTestRequest{
				Latitude:  -91.0,
				Longitude: 24.9384,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid latitude too high",
			request: RangeFilterTestRequest{
				Latitude:  91.0,
				Longitude: 24.9384,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid longitude too low",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: -181.0,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid longitude too high",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 181.0,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid threshold too low",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: -0.1,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid threshold too high",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: 1.1,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid week too low",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: 0.01,
				Week:      -1,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid week too high",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: 0.01,
				Week:      49,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid range filter parameters",
		},
		{
			name: "Invalid date format",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: 0.01,
				Date:      "invalid-date",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Date must be in YYYY-MM-DD format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestBody, _ := json.Marshal(tt.request)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/range/species/test", bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/range/species/test")

			// Test
			err := controller.TestRangeFilter(c)
			require.NoError(t, err)

			// Check response code
			assert.Equal(t, tt.expectedStatus, rec.Code)

			// Parse error response
			var response ErrorResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Contains(t, response.Message, tt.expectedError)
		})
	}
}

// TestValidWeekBoundaries verifies that valid week boundary values pass
// validation and reach the processor (which returns 500 since it's nil).
func TestValidWeekBoundaries(t *testing.T) {
	e, _, controller := setupRangeTestEnvironment(t)
	controller.Processor = nil

	for _, week := range []float32{1, 48} {
		t.Run(fmt.Sprintf("week=%v", week), func(t *testing.T) {
			testRequest := RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: 0.01,
				Week:      week,
			}

			requestBody, _ := json.Marshal(testRequest)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/range/species/test", bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/range/species/test")

			err := controller.TestRangeFilter(c)
			require.NoError(t, err)

			// Valid week passes validation and reaches processor (500, not 400)
			assert.Equal(t, http.StatusInternalServerError, rec.Code)

			var response ErrorResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Contains(t, response.Message, "BirdNET service not available")
		})
	}
}

// TestRebuildRangeFilterWithoutProcessor tests the rebuild endpoint when processor is not available
func TestRebuildRangeFilterWithoutProcessor(t *testing.T) {
	// Setup
	e, _, controller := setupRangeTestEnvironment(t)

	// Ensure processor is nil
	controller.Processor = nil

	// Create request
	req := httptest.NewRequest(http.MethodPost, "/api/v2/range/rebuild", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/range/rebuild")

	// Test
	err := controller.RebuildRangeFilter(c)
	require.NoError(t, err)

	// Check response code (should be 500 due to missing processor)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var response ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response.Message, "BirdNET service not available")
}

// TestBuildTestSettingsDoesNotMutateGlobal verifies that buildTestSettings
// creates an isolated snapshot: the controller's cached settings and the
// global published snapshot remain unchanged.
func TestBuildTestSettingsDoesNotMutateGlobal(t *testing.T) {
	_, _, controller := setupRangeTestEnvironment(t)

	originalLat := controller.Settings.BirdNET.Latitude
	originalLon := controller.Settings.BirdNET.Longitude
	originalThreshold := controller.Settings.BirdNET.RangeFilter.Threshold

	testSettings := controller.buildTestSettings(40.7128, -74.0060, 0.05)

	// Test snapshot has the overridden values
	assert.InDelta(t, 40.7128, testSettings.BirdNET.Latitude, 0.0001)
	assert.InDelta(t, -74.0060, testSettings.BirdNET.Longitude, 0.0001)
	assert.InDelta(t, float32(0.05), testSettings.BirdNET.RangeFilter.Threshold, 0.001)
	assert.True(t, testSettings.BirdNET.LocationConfigured)

	// Controller's settings are unchanged
	assert.InDelta(t, originalLat, controller.Settings.BirdNET.Latitude, 0.0001)
	assert.InDelta(t, originalLon, controller.Settings.BirdNET.Longitude, 0.0001)
	assert.InDelta(t, originalThreshold, controller.Settings.BirdNET.RangeFilter.Threshold, 0.001)

	// Published snapshot (what handlers read via CurrentOrFallback) is also unchanged
	published := conf.CurrentOrFallback(controller.Settings)
	assert.InDelta(t, originalLat, published.BirdNET.Latitude, 0.0001)
	assert.InDelta(t, originalLon, published.BirdNET.Longitude, 0.0001)
	assert.InDelta(t, originalThreshold, published.BirdNET.RangeFilter.Threshold, 0.001)
}

// TestBuildTestSettingsConcurrentWithCountEndpoint verifies that the count
// endpoint always returns the real coordinates even while buildTestSettings
// is called concurrently. Because buildTestSettings never modifies global
// state, this is trivially safe (regression test for #1940).
func TestBuildTestSettingsConcurrentWithCountEndpoint(t *testing.T) {
	e, _, controller := setupRangeTestEnvironment(t)

	originalLat := controller.Settings.BirdNET.Latitude
	originalLon := controller.Settings.BirdNET.Longitude

	const iterations = 50
	var wg sync.WaitGroup

	for range iterations {
		wg.Add(2)

		go func() {
			defer wg.Done()
			_ = controller.buildTestSettings(-33.8688, 151.2093, 0.05)
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/range/species/count", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/range/species/count")

			err := controller.GetRangeFilterSpeciesCount(c)
			if err != nil {
				t.Errorf("GetRangeFilterSpeciesCount returned error: %v", err)
				return
			}

			var response RangeFilterSpeciesCount
			if unmarshalErr := json.Unmarshal(rec.Body.Bytes(), &response); unmarshalErr != nil {
				t.Errorf("Failed to unmarshal response: %v", unmarshalErr)
				return
			}

			assert.InDelta(t, originalLat, response.Location.Latitude, 0.0001)
			assert.InDelta(t, originalLon, response.Location.Longitude, 0.0001)
		}()
	}

	wg.Wait()
}
