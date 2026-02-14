// analytics_false_positive_test.go: Tests for false positive filtering in analytics endpoints.
// These tests verify that analytics queries correctly exclude false_positive detections.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestGetSpeciesSummary_ExcludesFalsePositives verifies species summary excludes false positives.
// This test validates that the datastore is called and returns data without false positives.
func TestGetSpeciesSummary_ExcludesFalsePositives(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "unit")
	t.Attr("feature", "false-positive-filtering")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Create mock data representing what the datastore would return AFTER filtering false positives
	// In a real scenario with 3 detections (1 unreviewed, 1 correct, 1 false_positive),
	// the datastore should only return 2 in the count
	mockSummaryData := []datastore.SpeciesSummaryData{
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Count:          2, // This is the key test - count excludes false_positive
			FirstSeen:      time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC),
			LastSeen:       time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC),
			AvgConfidence:  0.875, // Average of 0.85 and 0.90 (not including false_positive's 0.88)
			MaxConfidence:  0.90,
		},
		{
			ScientificName: "Cyanocitta cristata",
			CommonName:     "Blue Jay",
			SpeciesCode:    "blujay",
			Count:          2,
			FirstSeen:      time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
			LastSeen:       time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			AvgConfidence:  0.895,
			MaxConfidence:  0.92,
		},
		// Note: Northern Cardinal is NOT in results because all its detections are false_positive
	}

	// Setup mock expectation - datastore should be called and return filtered data
	mockDS.On("GetSpeciesSummaryData", mock.Anything, "2024-01-15", "2024-01-15").Return(mockSummaryData, nil)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary?start_date=2024-01-15&end_date=2024-01-15", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/summary")

	// Execute
	handler := func(c echo.Context) error {
		return controller.GetSpeciesSummary(c)
	}
	err := handler(c)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response
	var response []map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify results - should only have 2 species (excluding species with only false_positives)
	assert.Len(t, response, 2, "Should return 2 species (false_positives excluded)")

	// Verify American Robin count
	robinData := findSpeciesInResponse(response, "Turdus migratorius")
	require.NotNil(t, robinData, "American Robin should be in results")
	assert.InDelta(t, 2.0, robinData["count"].(float64), 0.01,
		"American Robin count should be 2 (excluding false_positive detection)")

	// Verify Blue Jay count
	jayData := findSpeciesInResponse(response, "Cyanocitta cristata")
	require.NotNil(t, jayData, "Blue Jay should be in results")
	assert.InDelta(t, 2.0, jayData["count"].(float64), 0.01,
		"Blue Jay count should be 2")

	// Verify Cardinal is NOT in results
	// nolint:misspell // Cardinalis is a scientific name, not a misspelling
	cardinalData := findSpeciesInResponse(response, "Cardinalis cardinalis")
	assert.Nil(t, cardinalData,
		"Northern Cardinal should NOT be in results (all detections are false_positives)")

	// Verify mock expectations were met
	mockDS.AssertExpectations(t)
}

// TestGetDailyAnalytics_ExcludesFalsePositives verifies daily analytics excludes false positives.
func TestGetDailyAnalytics_ExcludesFalsePositives(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "unit")
	t.Attr("feature", "false-positive-filtering")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Mock data representing filtered results
	// With 6 total detections and 2 false_positives, we should see 4
	mockDailyData := []datastore.DailyAnalyticsData{
		{
			Date:  "2024-01-15",
			Count: 4, // Excludes 2 false_positives
		},
	}

	// Setup mock expectation
	mockDS.On("GetDailyAnalyticsData", mock.Anything, "2024-01-15", "2024-01-15", "").
		Return(mockDailyData, nil)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/time/daily?start_date=2024-01-15&end_date=2024-01-15", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/daily")

	// Execute
	handler := func(c echo.Context) error {
		return controller.GetDailyAnalytics(c)
	}
	err := handler(c)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response - GetDailyAnalytics returns a wrapped response
	var response struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Species   string `json:"species,omitempty"`
		Data      []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"data"`
		Total int `json:"total"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify results
	require.Len(t, response.Data, 1, "Should have 1 day entry in data array")
	assert.Equal(t, 4, response.Data[0].Count,
		"Count should be 4 (excludes 2 false_positives)")
	assert.Equal(t, 4, response.Total,
		"Total count should be 4 (excludes 2 false_positives)")

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetSpeciesDiversity_ExcludesFalsePositives verifies species diversity excludes false positives.
func TestGetSpeciesDiversity_ExcludesFalsePositives(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "unit")
	t.Attr("feature", "false-positive-filtering")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Mock data: Only 2 species should be counted (Cardinal excluded as all detections are false_positive)
	mockDiversityData := []datastore.DailyAnalyticsData{
		{
			Date:  "2024-01-15",
			Count: 2, // Robin and Jay, not Cardinal
		},
	}

	// Setup mock expectation
	mockDS.On("GetSpeciesDiversityData", mock.Anything, "2024-01-15", "2024-01-15").
		Return(mockDiversityData, nil)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/diversity?start_date=2024-01-15&end_date=2024-01-15", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/diversity")

	// Execute
	handler := func(c echo.Context) error {
		return controller.GetSpeciesDiversity(c)
	}
	err := handler(c)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response - GetSpeciesDiversity returns a wrapped response
	var response struct {
		StartDate    string `json:"start_date"`
		EndDate      string `json:"end_date"`
		Data         []struct {
			Date          string `json:"date"`
			UniqueSpecies int    `json:"unique_species"`
		} `json:"data"`
		MaxDiversity int `json:"max_diversity"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify results
	require.Len(t, response.Data, 1, "Should have 1 day entry in data array")
	assert.Equal(t, 2, response.Data[0].UniqueSpecies,
		"Unique species count should be 2 (excludes species with only false_positives)")
	assert.Equal(t, 2, response.MaxDiversity,
		"Max diversity should be 2")

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestAllDetectionsFalsePositive_ReturnsEmpty tests edge case where all detections are false positives.
func TestAllDetectionsFalsePositive_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "unit")
	t.Attr("feature", "edge-case")

	// Setup
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Mock empty data - datastore returns nothing when all detections are false_positive
	mockDS.On("GetSpeciesSummaryData", mock.Anything, "2024-01-20", "2024-01-20").
		Return([]datastore.SpeciesSummaryData{}, nil)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary?start_date=2024-01-20&end_date=2024-01-20", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/summary")

	// Execute
	handler := func(c echo.Context) error {
		return controller.GetSpeciesSummary(c)
	}
	err := handler(c)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response
	var response []map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify empty results
	assert.Empty(t, response, "Should return empty array when all detections are false_positives")

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// Helper function to find a species in the response by scientific name
func findSpeciesInResponse(response []map[string]any, scientificName string) map[string]any {
	for _, item := range response {
		if name, ok := item["scientific_name"].(string); ok && name == scientificName {
			return item
		}
	}
	return nil
}
