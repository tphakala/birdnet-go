// sse_contract_test.go: Tests for SSE payload backward compatibility.
//
// IMPORTANT: These tests verify the SSE API contract. The JSON field names tested here
// are part of the PUBLIC API consumed by the frontend.
//
// DO NOT MODIFY these expected field names without:
// 1. Updating the frontend to handle the change
// 2. Explicit approval from maintainers
// 3. Documentation in release notes
//
// Breaking changes to these field names will break the frontend silently.
package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// =============================================================================
// SSE API CONTRACT - FIELD NAMES
// =============================================================================
//
// The following field names are part of the SSE API contract consumed by the
// frontend. Changes to these names will break the UI.
// =============================================================================

// sseContractFields defines the expected JSON field names for SSE detection events.
var sseContractFields = struct {
	// Note fields (embedded, PascalCase from Go default marshaling)
	ID             string
	SourceNode     string
	Date           string
	Time           string
	BeginTime      string
	EndTime        string
	SpeciesCode    string
	ScientificName string
	CommonName     string
	Confidence     string
	Latitude       string
	Longitude      string
	Threshold      string
	Sensitivity    string
	ClipName       string
	ProcessingTime string

	// SSE-specific fields (explicit JSON tags, camelCase)
	BirdImage          string
	Timestamp          string
	EventType          string
	IsNewSpecies       string
	DaysSinceFirstSeen string

	// BirdImage nested fields (PascalCase from Go default)
	BirdImageURL            string
	BirdImageScientificName string
	BirdImageLicenseName    string
	BirdImageLicenseURL     string
	BirdImageAuthorName     string
	BirdImageAuthorURL      string
	BirdImageCachedAt       string
	BirdImageSourceProvider string
}{
	// Note fields (PascalCase - Go default marshaling)
	ID:             "ID",
	SourceNode:     "SourceNode",
	Date:           "Date",
	Time:           "Time",
	BeginTime:      "BeginTime",
	EndTime:        "EndTime",
	SpeciesCode:    "SpeciesCode",
	ScientificName: "ScientificName",
	CommonName:     "CommonName",
	Confidence:     "Confidence",
	Latitude:       "Latitude",
	Longitude:      "Longitude",
	Threshold:      "Threshold",
	Sensitivity:    "Sensitivity",
	ClipName:       "ClipName",
	ProcessingTime: "ProcessingTime",

	// SSE-specific fields (camelCase via json tags)
	BirdImage:          "birdImage",
	Timestamp:          "timestamp",
	EventType:          "eventType",
	IsNewSpecies:       "isNewSpecies",
	DaysSinceFirstSeen: "daysSinceFirstSeen",

	// BirdImage nested fields (PascalCase - Go default)
	BirdImageURL:            "URL",
	BirdImageScientificName: "ScientificName",
	BirdImageLicenseName:    "LicenseName",
	BirdImageLicenseURL:     "LicenseURL",
	BirdImageAuthorName:     "AuthorName",
	BirdImageAuthorURL:      "AuthorURL",
	BirdImageCachedAt:       "CachedAt",
	BirdImageSourceProvider: "SourceProvider",
}

// createTestNoteWithAllFields creates a Note with all fields populated for contract testing.
// Test coordinates are Helsinki, Finland (60.1699°N, 24.9384°E).
func createTestNoteWithAllFields() datastore.Note {
	return datastore.Note{
		ID:             12345,
		SourceNode:     "test-node",
		Date:           "2024-01-15",
		Time:           "14:30:45",
		BeginTime:      time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
		EndTime:        time.Date(2024, 1, 15, 14, 30, 48, 0, time.UTC),
		SpeciesCode:    "gretit1",
		ScientificName: "Parus major",
		CommonName:     "Great Tit",
		Confidence:     0.85,
		Latitude:       60.1699,
		Longitude:      24.9384,
		Threshold:      0.7,
		Sensitivity:    1.0,
		ClipName:       "clip_001.wav",
		ProcessingTime: 150 * time.Millisecond,
	}
}

// createTestSSEDetectionData creates an SSEDetectionData with all fields populated.
func createTestSSEDetectionData() SSEDetectionData {
	return SSEDetectionData{
		Note: createTestNoteWithAllFields(),
		BirdImage: imageprovider.BirdImage{
			URL:            "https://example.com/bird.jpg",
			ScientificName: "Parus major",
			LicenseName:    "CC BY-SA 4.0",
			LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
			AuthorName:     "Test Author",
			AuthorURL:      "https://example.com/author",
			CachedAt:       time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			SourceProvider: "wikimedia",
		},
		Timestamp:          time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC),
		EventType:          "new_detection",
		IsNewSpecies:       true,
		DaysSinceFirstSeen: 0,
	}
}

// TestSSEContract_DetectionPayload_FieldNames validates that SSE detection
// events contain the expected JSON field names that the frontend depends on.
func TestSSEContract_DetectionPayload_FieldNames(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	// Serialize to JSON (same as SSE does)
	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err, "Failed to marshal SSEDetectionData to JSON")

	// Parse back to map to check field names
	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err, "Failed to unmarshal JSON to map")

	// Log the actual JSON for debugging
	t.Logf("SSE JSON payload:\n%s", string(jsonBytes))

	// ==========================================================================
	// CONTRACT ASSERTIONS - DO NOT MODIFY EXPECTED VALUES
	// ==========================================================================

	t.Run("Note fields use PascalCase", func(t *testing.T) {
		noteFields := []string{
			sseContractFields.ID,
			sseContractFields.SourceNode,
			sseContractFields.Date,
			sseContractFields.Time,
			sseContractFields.SpeciesCode,
			sseContractFields.ScientificName,
			sseContractFields.CommonName,
			sseContractFields.Confidence,
			sseContractFields.Latitude,
			sseContractFields.Longitude,
			sseContractFields.ClipName,
		}

		for _, field := range noteFields {
			assert.Contains(t, payload, field,
				"SSE API CONTRACT VIOLATION: Note field '%s' must be present", field)
		}
	})

	t.Run("SSE-specific fields use camelCase", func(t *testing.T) {
		assert.Contains(t, payload, sseContractFields.BirdImage,
			"SSE API CONTRACT VIOLATION: birdImage field must be present")
		assert.Contains(t, payload, sseContractFields.Timestamp,
			"SSE API CONTRACT VIOLATION: timestamp field must be present")
		assert.Contains(t, payload, sseContractFields.EventType,
			"SSE API CONTRACT VIOLATION: eventType field must be present")
	})

	t.Run("BirdImage nested structure is correct", func(t *testing.T) {
		birdImageRaw, exists := payload[sseContractFields.BirdImage]
		require.True(t, exists, "birdImage field must be present")

		birdImage, ok := birdImageRaw.(map[string]any)
		require.True(t, ok, "birdImage must be an object")

		expectedBirdImageFields := []string{
			sseContractFields.BirdImageURL,
			sseContractFields.BirdImageScientificName,
			sseContractFields.BirdImageLicenseName,
			sseContractFields.BirdImageAuthorName,
			sseContractFields.BirdImageSourceProvider,
		}

		for _, field := range expectedBirdImageFields {
			assert.Contains(t, birdImage, field,
				"SSE API CONTRACT VIOLATION: BirdImage.%s field must be present", field)
		}
	})

	t.Run("New species tracking fields are present when populated", func(t *testing.T) {
		// isNewSpecies should be present (true in our test data)
		assert.Contains(t, payload, sseContractFields.IsNewSpecies,
			"SSE API CONTRACT: isNewSpecies field must be present when true")
	})
}

// TestSSEContract_DetectionPayload_DateTimeFormat validates date/time string formats.
func TestSSEContract_DetectionPayload_DateTimeFormat(t *testing.T) {
	t.Parallel()

	note := createTestNoteWithAllFields()

	// Date should be "YYYY-MM-DD"
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, note.Date,
		"SSE API CONTRACT: Date must be in YYYY-MM-DD format")

	// Time should be "HH:MM:SS"
	assert.Regexp(t, `^\d{2}:\d{2}:\d{2}$`, note.Time,
		"SSE API CONTRACT: Time must be in HH:MM:SS format")
}

// TestSSEContract_FrontendAccessPaths verifies the field access paths used by the frontend.
func TestSSEContract_FrontendAccessPaths(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	// ==========================================================================
	// SIMULATE FRONTEND ACCESS PATTERNS
	// ==========================================================================

	t.Run("Frontend can access detection species info", func(t *testing.T) {
		// Frontend accesses: data.CommonName, data.ScientificName
		commonName, exists := payload["CommonName"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.CommonName")
		assert.Equal(t, "Great Tit", commonName)

		sciName, exists := payload["ScientificName"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.ScientificName")
		assert.Equal(t, "Parus major", sciName)
	})

	t.Run("Frontend can access detection confidence", func(t *testing.T) {
		// Frontend accesses: data.Confidence
		confidence, exists := payload["Confidence"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.Confidence")
		assert.InDelta(t, 0.85, confidence, 0.001)
	})

	t.Run("Frontend can access bird image URL", func(t *testing.T) {
		// Frontend accesses: data.birdImage.URL
		birdImageRaw, exists := payload["birdImage"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.birdImage")

		birdImage, ok := birdImageRaw.(map[string]any)
		require.True(t, ok, "FRONTEND BROKEN: data.birdImage is not an object")

		url, exists := birdImage["URL"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.birdImage.URL")
		assert.Equal(t, "https://example.com/bird.jpg", url)
	})

	t.Run("Frontend can access detection ID", func(t *testing.T) {
		// Frontend accesses: data.ID for navigation/links
		id, exists := payload["ID"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.ID")
		assert.InDelta(t, float64(12345), id, 0.001)
	})

	t.Run("Frontend can access event type", func(t *testing.T) {
		// Frontend accesses: data.eventType for event handling
		eventType, exists := payload["eventType"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.eventType")
		assert.Equal(t, "new_detection", eventType)
	})
}

// TestSSEContract_IsNewSpeciesOmittedWhenFalse verifies omitempty behavior.
func TestSSEContract_IsNewSpeciesOmittedWhenFalse(t *testing.T) {
	t.Parallel()

	detection := SSEDetectionData{
		Note:               createTestNoteWithAllFields(),
		BirdImage:          imageprovider.BirdImage{},
		Timestamp:          time.Now(),
		EventType:          "new_detection",
		IsNewSpecies:       false, // Should be omitted
		DaysSinceFirstSeen: 0,     // Should be omitted
	}

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	// isNewSpecies and daysSinceFirstSeen should be omitted when zero/false (omitempty)
	_, hasIsNewSpecies := payload["isNewSpecies"]
	assert.False(t, hasIsNewSpecies,
		"SSE API CONTRACT: isNewSpecies should be omitted when false (omitempty)")

	_, hasDaysSinceFirstSeen := payload["daysSinceFirstSeen"]
	assert.False(t, hasDaysSinceFirstSeen,
		"SSE API CONTRACT: daysSinceFirstSeen should be omitted when zero (omitempty)")
}

// TestSSEContract_AllExpectedFieldsPresent is a comprehensive check that all
// expected fields are present in the SSE payload.
func TestSSEContract_AllExpectedFieldsPresent(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	// ==========================================================================
	// EXPECTED ROOT-LEVEL FIELDS
	// ==========================================================================
	expectedRootFields := []string{
		// From embedded Note (PascalCase)
		"ID", "SourceNode", "Date", "Time",
		"SpeciesCode", "ScientificName", "CommonName", "Confidence",
		"Latitude", "Longitude", "Threshold", "Sensitivity",
		"ClipName", "ProcessingTime",
		// SSE-specific (camelCase)
		"birdImage", "timestamp", "eventType",
	}

	for _, field := range expectedRootFields {
		assert.Contains(t, payload, field,
			"SSE API CONTRACT: Expected field '%s' not found in JSON payload", field)
	}
}

// TestSSEContract_NoCamelCaseConversionForNoteFields verifies that Note fields
// retain their PascalCase naming (Go default) and haven't been accidentally
// converted to camelCase.
func TestSSEContract_NoCamelCaseConversionForNoteFields(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	jsonStr := string(jsonBytes)

	// These would be wrong if someone added json tags with camelCase
	forbiddenFields := []struct {
		wrong   string
		correct string
	}{
		{"commonName", "CommonName"},
		{"scientificName", "ScientificName"},
		{"clipName", "ClipName"},
		{"sourceNode", "SourceNode"},
		{"speciesCode", "SpeciesCode"},
		{"processingTime", "ProcessingTime"},
	}

	for _, f := range forbiddenFields {
		assert.NotContains(t, jsonStr, `"`+f.wrong+`":`,
			"SSE API CONTRACT VIOLATION: Found '%s' but should be '%s'", f.wrong, f.correct)
	}
}
