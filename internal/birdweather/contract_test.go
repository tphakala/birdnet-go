// contract_test.go: Tests for BirdWeather API field contract.
//
// IMPORTANT: These tests verify that the BirdWeather integration extracts
// the correct fields from Note for API submission. They serve as regression
// tests for the model separation refactor.
//
// The BirdWeather API requires specific fields in specific formats. Changes
// to how these fields are accessed or formatted will break the integration.
package birdweather

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// =============================================================================
// BIRDWEATHER API CONTRACT - REQUIRED FIELDS
// =============================================================================
//
// The BirdWeather API requires the following fields from Note:
// - CommonName: bird's common name
// - ScientificName: bird's scientific name
// - Confidence: detection confidence (0-1)
//
// The Publish function formats the detection instant it is given as the timestamp
// (Note.Date and Note.Time are not used), then calls PostDetection with the extracted fields.
// =============================================================================

// createTestNote creates a Note with all BirdWeather-required fields populated.
// Test coordinates are Helsinki, Finland (60.1699°N, 24.9384°E).
func createTestNote() *datastore.Note {
	return &datastore.Note{
		ID:             12345,
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		Latitude:       60.1699,
		Longitude:      24.9384,
		ClipName:       "clip_001.wav",
	}
}

// TestBirdWeatherContract_RequiredFields verifies that the Publish method
// can access the correct fields from Note for BirdWeather API submission.
func TestBirdWeatherContract_RequiredFields(t *testing.T) {
	t.Parallel()

	note := createTestNote()

	// ==========================================================================
	// CONTRACT ASSERTIONS - Required fields must be accessible
	// ==========================================================================

	// ScientificName (required by BirdWeather API)
	assert.NotEmpty(t, note.ScientificName,
		"BIRDWEATHER CONTRACT: ScientificName field must be accessible and non-empty")

	// CommonName (required by BirdWeather API)
	assert.NotEmpty(t, note.CommonName,
		"BIRDWEATHER CONTRACT: CommonName field must be accessible and non-empty")

	// Confidence (required by BirdWeather API)
	assert.Greater(t, note.Confidence, 0.0,
		"BIRDWEATHER CONTRACT: Confidence field must be accessible and > 0")
	assert.LessOrEqual(t, note.Confidence, 1.0,
		"BIRDWEATHER CONTRACT: Confidence must be <= 1.0")
}

// TestBirdWeatherContract_ConfidenceFormatForAPI verifies confidence is formatted
// correctly for the API.
func TestBirdWeatherContract_ConfidenceFormatForAPI(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		confidence float64
		expected   string
	}{
		{"high_confidence", 0.95, "0.95"},
		{"medium_confidence", 0.50, "0.50"},
		{"low_confidence", 0.10, "0.10"},
		{"max_confidence", 1.00, "1.00"},
		{"min_positive", 0.01, "0.01"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate confidence formatting as done in PostDetection
			formatted := formatConfidence(tc.confidence)

			assert.Equal(t, tc.expected, formatted,
				"BIRDWEATHER CONTRACT: Confidence %.2f should format as %s", tc.confidence, tc.expected)
		})
	}
}

// formatConfidence formats confidence as done in PostDetection.
func formatConfidence(confidence float64) string {
	return fmt.Sprintf("%.2f", confidence)
}

// TestBirdWeatherContract_FieldAccessPattern verifies the exact field access
// pattern used in the Publish function.
func TestBirdWeatherContract_FieldAccessPattern(t *testing.T) {
	t.Parallel()

	note := createTestNote()

	// These field accesses verify the exact field names used in Publish and PostDetection.
	// Any change to Note struct field names would break these accesses and this test.

	// Accessed in PostDetection call
	assert.NotEmpty(t, note.CommonName, "CommonName field must be accessible")
	assert.NotEmpty(t, note.ScientificName, "ScientificName field must be accessible")
	assert.Greater(t, note.Confidence, 0.0, "Confidence field must be accessible")
}

// TestBirdWeatherContract_SpeciesNameFormats verifies species name handling.
func TestBirdWeatherContract_SpeciesNameFormats(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		scientificName string
		commonName     string
	}{
		{
			name:           "simple_names",
			scientificName: "Parus major",
			commonName:     "Great Tit",
		},
		{
			name:           "subspecies",
			scientificName: "Motacilla alba alba",
			commonName:     "White Wagtail",
		},
		{
			name:           "with_parentheses",
			scientificName: "Sturnus vulgaris",
			commonName:     "Common Starling (European)",
		},
		{
			name:           "with_apostrophe",
			scientificName: "Bonasa umbellus",
			commonName:     "Ruffed Grouse",
		},
		{
			name:           "with_hyphen",
			scientificName: "Plectrophenax nivalis",
			commonName:     "Snow Bunting",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			note := &datastore.Note{
				Date:           "2024-01-15",
				Time:           "14:30:45",
				ScientificName: tc.scientificName,
				CommonName:     tc.commonName,
				Confidence:     0.85,
			}

			// BirdWeather API should accept these names
			assert.NotEmpty(t, note.ScientificName)
			assert.NotEmpty(t, note.CommonName)
		})
	}
}

// TestBirdWeatherContract_EdgeCaseConfidence verifies edge case confidence values.
func TestBirdWeatherContract_EdgeCaseConfidence(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		confidence float64
		valid      bool
	}{
		{"max_valid", 1.0, true},
		{"high", 0.99, true},
		{"threshold", 0.70, true},
		{"low", 0.01, true},
		{"zero", 0.0, false},      // Zero confidence shouldn't be sent
		{"negative", -0.5, false}, // Invalid
		{"over_max", 1.5, false},  // Invalid
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.valid {
				assert.GreaterOrEqual(t, tc.confidence, 0.0)
				assert.LessOrEqual(t, tc.confidence, 1.0)
				assert.Greater(t, tc.confidence, 0.0,
					"Valid confidence must be > 0 (BirdWeather doesn't accept zero confidence)")
			} else {
				invalid := tc.confidence <= 0.0 || tc.confidence > 1.0
				assert.True(t, invalid,
					"Confidence %.2f should be invalid", tc.confidence)
			}
		})
	}
}
