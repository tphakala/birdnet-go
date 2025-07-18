// range_test.go: Package api provides tests for range filter API v2 endpoints.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/birdnet"
)

// MockBirdNET is a mock implementation of BirdNET for testing
type MockBirdNET struct {
	GetProbableSpeciesFunc func(date time.Time, week float32) ([]birdnet.SpeciesScore, error)
}

func (m *MockBirdNET) GetProbableSpecies(date time.Time, week float32) ([]birdnet.SpeciesScore, error) {
	if m.GetProbableSpeciesFunc != nil {
		return m.GetProbableSpeciesFunc(date, week)
	}
	// Default mock response
	return []birdnet.SpeciesScore{
		{Label: "Turdus merula_Eurasian Blackbird", Score: 0.85},
		{Label: "Parus major_Great Tit", Score: 0.72},
	}, nil
}

// MockProcessor is a mock implementation of the processor for testing
type MockProcessor struct {
	BirdNETInstance *MockBirdNET
}

func (m *MockProcessor) GetBirdNET() *birdnet.BirdNET {
	// Since we can't easily mock the actual BirdNET struct, we'll return nil
	// and handle this in the test setup
	return nil
}

// setupRangeTestEnvironment creates a test environment specifically for range filter tests
func setupRangeTestEnvironment(t *testing.T) (*echo.Echo, *MockDataStore, *Controller) {
	t.Helper()

	e, mockDS, controller := setupTestEnvironment(t)

	// Set up mock settings with range filter data
	controller.Settings.BirdNET.Latitude = 60.1699
	controller.Settings.BirdNET.Longitude = 24.9384
	controller.Settings.BirdNET.RangeFilter.Threshold = 0.01
	controller.Settings.BirdNET.RangeFilter.LastUpdated = time.Now()

	// Mock the included species list using the proper API
	controller.Settings.UpdateIncludedSpecies([]string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
		"Corvus cornix_Hooded Crow",
	})

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
	assert.Contains(t, response.Message, "BirdNET processor not available")
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
			expectedError:  "Latitude must be between -90 and 90",
		},
		{
			name: "Invalid latitude too high",
			request: RangeFilterTestRequest{
				Latitude:  91.0,
				Longitude: 24.9384,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Latitude must be between -90 and 90",
		},
		{
			name: "Invalid longitude too low",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: -181.0,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Longitude must be between -180 and 180",
		},
		{
			name: "Invalid longitude too high",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 181.0,
				Threshold: 0.01,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Longitude must be between -180 and 180",
		},
		{
			name: "Invalid threshold too low",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: -0.1,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Threshold must be between 0 and 1",
		},
		{
			name: "Invalid threshold too high",
			request: RangeFilterTestRequest{
				Latitude:  60.1699,
				Longitude: 24.9384,
				Threshold: 1.1,
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Threshold must be between 0 and 1",
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
	assert.Contains(t, response.Message, "BirdNET processor not available")
}
