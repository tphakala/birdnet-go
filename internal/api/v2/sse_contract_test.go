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
// SSE API CONTRACT - FIELD NAMES (camelCase per REST API conventions)
// =============================================================================
//
// All field names use camelCase JSON tags. The SSEDetectionData struct no longer
// embeds datastore.Note directly, avoiding PascalCase Go default marshaling.
// =============================================================================

// sseContractFields defines the expected JSON field names for SSE detection events.
var sseContractFields = struct {
	// Detection fields (camelCase via explicit json tags)
	ID             string
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
	ClipName       string
	Verified       string
	Locked         string
	Source         string

	// SSE-specific fields (camelCase)
	BirdImage          string
	Timestamp          string
	EventType          string
	IsNewSpecies       string
	DaysSinceFirstSeen string

	// BirdImage nested fields (camelCase via explicit json tags)
	BirdImageURL            string
	BirdImageScientificName string
	BirdImageLicenseName    string
	BirdImageLicenseURL     string
	BirdImageAuthorName     string
	BirdImageAuthorURL      string
	BirdImageSourceProvider string

	// Source nested fields (camelCase)
	SourceID          string
	SourceType        string
	SourceDisplayName string
}{
	// Detection fields (camelCase)
	ID:             "id",
	Date:           "date",
	Time:           "time",
	BeginTime:      "beginTime",
	EndTime:        "endTime",
	SpeciesCode:    "speciesCode",
	ScientificName: "scientificName",
	CommonName:     "commonName",
	Confidence:     "confidence",
	Latitude:       "latitude",
	Longitude:      "longitude",
	ClipName:       "clipName",
	Verified:       "verified",
	Locked:         "locked",
	Source:         "source",

	// SSE-specific fields (camelCase)
	BirdImage:          "birdImage",
	Timestamp:          "timestamp",
	EventType:          "eventType",
	IsNewSpecies:       "isNewSpecies",
	DaysSinceFirstSeen: "daysSinceFirstSeen",

	// BirdImage nested fields (camelCase)
	BirdImageURL:            "url",
	BirdImageScientificName: "scientificName",
	BirdImageLicenseName:    "licenseName",
	BirdImageLicenseURL:     "licenseURL",
	BirdImageAuthorName:     "authorName",
	BirdImageAuthorURL:      "authorURL",
	BirdImageSourceProvider: "sourceProvider",

	// Source nested fields (camelCase)
	SourceID:          "id",
	SourceType:        "type",
	SourceDisplayName: "displayName",
}

// createTestNoteWithAllFields creates a Note with all fields populated for contract testing.
// Test coordinates are Helsinki, Finland (60.1699N, 24.9384E).
func createTestNoteWithAllFields() datastore.Note {
	return datastore.Note{
		ID: 12345,
		Source: datastore.AudioSource{
			ID:          "rtsp_test123",
			SafeString:  "rtsp://user:***@192.168.1.100:554/stream",
			DisplayName: "Garden Microphone",
		},
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
		ClipName:       "/home/user/birdnet-go/clips/clip_001.wav",
		ProcessingTime: 150 * time.Millisecond,
		Verified:       "correct",
		Locked:         true,
	}
}

// createTestBirdImage creates a BirdImage with all fields populated.
func createTestBirdImage() imageprovider.BirdImage {
	return imageprovider.BirdImage{
		URL:            "https://example.com/bird.jpg",
		ScientificName: "Parus major",
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:     "Test Author",
		AuthorURL:      "https://example.com/author",
		CachedAt:       time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		SourceProvider: "wikimedia",
	}
}

// createTestSSEDetectionData creates an SSEDetectionData with all fields populated
// using the newSSEDetectionData constructor (same code path as production).
func createTestSSEDetectionData() SSEDetectionData {
	note := createTestNoteWithAllFields()
	birdImage := createTestBirdImage()
	det := newSSEDetectionData(&note, &birdImage)
	det.IsNewSpecies = true
	det.DaysSinceFirstSeen = 0
	return det
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

	t.Run("Detection fields use camelCase", func(t *testing.T) {
		detectionFields := []string{
			sseContractFields.ID,
			sseContractFields.Date,
			sseContractFields.Time,
			sseContractFields.SpeciesCode,
			sseContractFields.ScientificName,
			sseContractFields.CommonName,
			sseContractFields.Confidence,
			sseContractFields.Latitude,
			sseContractFields.Longitude,
			sseContractFields.ClipName,
			sseContractFields.Locked,
		}

		for _, field := range detectionFields {
			assert.Contains(t, payload, field,
				"SSE API CONTRACT VIOLATION: Detection field '%s' must be present", field)
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

	t.Run("Source nested structure is correct", func(t *testing.T) {
		sourceRaw, exists := payload[sseContractFields.Source]
		require.True(t, exists, "source field must be present")

		source, ok := sourceRaw.(map[string]any)
		require.True(t, ok, "source must be an object")

		expectedSourceFields := []string{
			sseContractFields.SourceID,
			sseContractFields.SourceDisplayName,
		}

		for _, field := range expectedSourceFields {
			assert.Contains(t, source, field,
				"SSE API CONTRACT VIOLATION: Source.%s field must be present", field)
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

	detection := createTestSSEDetectionData()

	// Date should be "YYYY-MM-DD"
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, detection.Date,
		"SSE API CONTRACT: Date must be in YYYY-MM-DD format")

	// Time should be "HH:MM:SS"
	assert.Regexp(t, `^\d{2}:\d{2}:\d{2}$`, detection.Time,
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
	// SIMULATE FRONTEND ACCESS PATTERNS (camelCase)
	// ==========================================================================

	t.Run("Frontend can access detection species info", func(t *testing.T) {
		// Frontend accesses: data.commonName, data.scientificName
		commonName, exists := payload["commonName"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.commonName")
		assert.Equal(t, "Great Tit", commonName)

		sciName, exists := payload["scientificName"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.scientificName")
		assert.Equal(t, "Parus major", sciName)
	})

	t.Run("Frontend can access detection confidence", func(t *testing.T) {
		// Frontend accesses: data.confidence
		confidence, exists := payload["confidence"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.confidence")
		assert.InDelta(t, 0.85, confidence, 0.001)
	})

	t.Run("Frontend can access bird image URL", func(t *testing.T) {
		// Frontend accesses: data.birdImage.url
		birdImageRaw, exists := payload["birdImage"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.birdImage")

		birdImage, ok := birdImageRaw.(map[string]any)
		require.True(t, ok, "FRONTEND BROKEN: data.birdImage is not an object")

		url, exists := birdImage["url"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.birdImage.url")
		assert.Equal(t, "https://example.com/bird.jpg", url)
	})

	t.Run("Frontend can access detection ID", func(t *testing.T) {
		// Frontend accesses: data.id for navigation/links
		id, exists := payload["id"]
		require.True(t, exists, "FRONTEND BROKEN: Cannot access data.id")
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

	note := createTestNoteWithAllFields()
	birdImage := createTestBirdImage()
	detection := newSSEDetectionData(&note, &birdImage)
	detection.IsNewSpecies = false   // Should be omitted
	detection.DaysSinceFirstSeen = 0 // Should be omitted

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
	// EXPECTED ROOT-LEVEL FIELDS (all camelCase)
	// ==========================================================================
	expectedRootFields := []string{
		// Detection fields (camelCase)
		"id", "date", "time",
		"speciesCode", "scientificName", "commonName", "confidence",
		"latitude", "longitude",
		"clipName",
		// SSE-specific (camelCase)
		"birdImage", "timestamp", "eventType",
	}

	for _, field := range expectedRootFields {
		assert.Contains(t, payload, field,
			"SSE API CONTRACT: Expected field '%s' not found in JSON payload", field)
	}
}

// TestSSEContract_NoPascalCaseFields verifies that all fields use camelCase
// naming and no PascalCase Go defaults leak through.
func TestSSEContract_NoPascalCaseFields(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	jsonStr := string(jsonBytes)

	// These PascalCase fields should NOT appear in the JSON output
	forbiddenFields := []string{
		"CommonName",
		"ScientificName",
		"ClipName",
		"SourceNode",
		"SpeciesCode",
		"ProcessingTime",
		"BeginTime",
		"EndTime",
		"Confidence",
		"Latitude",
		"Longitude",
		"Threshold",
		"Sensitivity",
	}

	for _, field := range forbiddenFields {
		assert.NotContains(t, jsonStr, `"`+field+`":`,
			"SSE API CONTRACT VIOLATION: PascalCase field '%s' must not appear in SSE payload", field)
	}
}

// TestSSEContract_SensitiveDataExcluded verifies that sensitive internal data
// is not exposed in SSE payloads.
func TestSSEContract_SensitiveDataExcluded(t *testing.T) {
	t.Parallel()

	note := createTestNoteWithAllFields()
	// Set a full filesystem path to verify it gets stripped to filename
	note.ClipName = "/var/lib/birdnet-go/clips/2024/01/15/clip_001.wav"
	// Set source with credentials in SafeString
	note.Source = datastore.AudioSource{
		ID:          "rtsp_abc123",
		SafeString:  "rtsp://admin:secretpass@192.168.1.100:554/stream1",
		DisplayName: "Backyard Camera",
	}

	birdImage := createTestBirdImage()
	detection := newSSEDetectionData(&note, &birdImage)

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	jsonStr := string(jsonBytes)

	// ClipName should be just the filename, not the full path
	assert.NotContains(t, jsonStr, "/var/lib/birdnet-go",
		"SSE payload must not contain filesystem paths")
	assert.Contains(t, jsonStr, "clip_001.wav",
		"SSE payload should contain the clip filename")

	// SafeString (which could contain sanitized RTSP URLs) should not appear
	assert.NotContains(t, jsonStr, "rtsp://",
		"SSE payload must not contain RTSP URLs")
	assert.NotContains(t, jsonStr, "safeString",
		"SSE payload must not contain safeString field")
	assert.NotContains(t, jsonStr, "SafeString",
		"SSE payload must not contain SafeString field")
	assert.NotContains(t, jsonStr, "secretpass",
		"SSE payload must not contain credentials")

	// Internal Note fields should not be present
	assert.NotContains(t, jsonStr, "Threshold",
		"SSE payload must not contain internal Threshold field")
	assert.NotContains(t, jsonStr, "Sensitivity",
		"SSE payload must not contain internal Sensitivity field")
	assert.NotContains(t, jsonStr, "ProcessingTime",
		"SSE payload must not contain internal ProcessingTime field")
	assert.NotContains(t, jsonStr, "SourceNode",
		"SSE payload must not contain internal SourceNode field")

	// Verify source only has safe fields
	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	sourceRaw, exists := payload["source"]
	require.True(t, exists, "source field must be present")

	source, ok := sourceRaw.(map[string]any)
	require.True(t, ok, "source must be an object")

	assert.Equal(t, "rtsp_abc123", source["id"])
	assert.Equal(t, "Backyard Camera", source["displayName"])
	// SafeString must not appear as a field
	_, hasSafeString := source["safeString"]
	assert.False(t, hasSafeString, "source must not contain safeString field")
}

// TestSSEContract_NullInternalFieldsExcluded verifies that null internal fields
// like Results, Review, Comments, and Lock are not present in the SSE payload.
func TestSSEContract_NullInternalFieldsExcluded(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	jsonStr := string(jsonBytes)

	// These internal Note fields should never appear in SSE output
	internalFields := []string{
		"Results",
		"Review",
		"Comments",
		"Lock",
		"Occurrence",
	}

	for _, field := range internalFields {
		assert.NotContains(t, jsonStr, `"`+field+`"`,
			"SSE payload must not contain internal field '%s'", field)
	}
}
