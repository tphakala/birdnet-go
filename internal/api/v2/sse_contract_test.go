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
	det.DaysSinceFirstSeen = 14
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
		assert.Contains(t, payload, sseContractFields.IsNewSpecies,
			"SSE API CONTRACT: isNewSpecies field must be present when true")
		assert.Equal(t, true, payload[sseContractFields.IsNewSpecies],
			"SSE API CONTRACT: isNewSpecies value mismatch")

		assert.Contains(t, payload, sseContractFields.DaysSinceFirstSeen,
			"SSE API CONTRACT: daysSinceFirstSeen must be present when non-zero")
		assert.InDelta(t, 14, payload[sseContractFields.DaysSinceFirstSeen], 0.001,
			"SSE API CONTRACT: daysSinceFirstSeen value mismatch")
	})

	t.Run("Verified and locked fields have correct values", func(t *testing.T) {
		assert.Equal(t, "correct", payload[sseContractFields.Verified],
			"SSE API CONTRACT: verified value mismatch")
		assert.Equal(t, true, payload[sseContractFields.Locked],
			"SSE API CONTRACT: locked value mismatch")
	})

	t.Run("Source object has correct values", func(t *testing.T) {
		sourceRaw := payload[sseContractFields.Source]
		source, ok := sourceRaw.(map[string]any)
		require.True(t, ok, "source must be an object")

		assert.Equal(t, "rtsp_test123", source[sseContractFields.SourceID],
			"SSE API CONTRACT: source.id value mismatch")
		assert.Equal(t, "Garden Microphone", source[sseContractFields.SourceDisplayName],
			"SSE API CONTRACT: source.displayName value mismatch")
	})

	t.Run("BeginTime and EndTime are RFC3339 with correct values", func(t *testing.T) {
		beginTime, ok := payload[sseContractFields.BeginTime].(string)
		require.True(t, ok, "beginTime must be a string")
		assert.Equal(t, "2024-01-15T14:30:45Z", beginTime,
			"SSE API CONTRACT: beginTime value mismatch")

		endTime, ok := payload[sseContractFields.EndTime].(string)
		require.True(t, ok, "endTime must be a string")
		assert.Equal(t, "2024-01-15T14:30:48Z", endTime,
			"SSE API CONTRACT: endTime value mismatch")
	})

	t.Run("ClipName is filename only, no path", func(t *testing.T) {
		clipName, ok := payload[sseContractFields.ClipName].(string)
		require.True(t, ok, "clipName must be a string")
		assert.Equal(t, "clip_001.wav", clipName,
			"SSE API CONTRACT: clipName must be filename only, no directory path")
	})

	t.Run("BirdImage nested fields have correct values", func(t *testing.T) {
		birdImage, ok := payload[sseContractFields.BirdImage].(map[string]any)
		require.True(t, ok, "birdImage must be an object")

		assert.Equal(t, "https://example.com/bird.jpg", birdImage[sseContractFields.BirdImageURL],
			"SSE API CONTRACT: birdImage.url value mismatch")
		assert.Equal(t, "CC BY-SA 4.0", birdImage[sseContractFields.BirdImageLicenseName],
			"SSE API CONTRACT: birdImage.licenseName value mismatch")
		assert.Equal(t, "Test Author", birdImage[sseContractFields.BirdImageAuthorName],
			"SSE API CONTRACT: birdImage.authorName value mismatch")
		assert.Equal(t, "wikimedia", birdImage[sseContractFields.BirdImageSourceProvider],
			"SSE API CONTRACT: birdImage.sourceProvider value mismatch")
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

// =============================================================================
// EXHAUSTIVE FIELD VALUE TESTS
// =============================================================================

// TestSSEContract_VerifiedAndLocked_Serialization validates that the verified
// and locked fields serialize with correct values and omitempty behavior.
func TestSSEContract_VerifiedAndLocked_Serialization(t *testing.T) {
	t.Parallel()

	t.Run("verified present when set", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.Verified = "correct"
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		val, exists := payload["verified"]
		require.True(t, exists, "SSE API CONTRACT: verified must be present when set")
		assert.Equal(t, "correct", val,
			"SSE API CONTRACT: verified value must match")
	})

	t.Run("verified omitted when empty", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.Verified = "" // empty string, omitempty should omit
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		_, exists := payload["verified"]
		assert.False(t, exists,
			"SSE API CONTRACT: verified should be omitted when empty (omitempty)")
	})

	t.Run("locked true serializes correctly", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.Locked = true
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		val, exists := payload["locked"]
		require.True(t, exists, "SSE API CONTRACT: locked must always be present")
		assert.Equal(t, true, val,
			"SSE API CONTRACT: locked=true must serialize as boolean true")
	})

	t.Run("locked false still present (no omitempty)", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.Locked = false
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		val, exists := payload["locked"]
		require.True(t, exists,
			"SSE API CONTRACT: locked must be present even when false (no omitempty)")
		assert.Equal(t, false, val,
			"SSE API CONTRACT: locked=false must serialize as boolean false")
	})
}

// TestSSEContract_Source_NestedFields validates the source object structure,
// including the omitempty behavior of the type field.
func TestSSEContract_Source_NestedFields(t *testing.T) {
	t.Parallel()

	t.Run("source contains id and displayName", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		sourceRaw, exists := payload["source"]
		require.True(t, exists, "SSE API CONTRACT: source must be present")

		source, ok := sourceRaw.(map[string]any)
		require.True(t, ok, "SSE API CONTRACT: source must be an object")

		assert.Equal(t, "rtsp_test123", source["id"],
			"SSE API CONTRACT: source.id must match note.Source.ID")
		assert.Equal(t, "Garden Microphone", source["displayName"],
			"SSE API CONTRACT: source.displayName must match note.Source.DisplayName")
	})

	t.Run("source type omitted when empty", func(t *testing.T) {
		t.Parallel()

		// newSSEDetectionData does not set Type from the Note source,
		// so it should be omitted due to omitempty
		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		source, ok := payload["source"].(map[string]any)
		require.True(t, ok)

		_, hasType := source["type"]
		assert.False(t, hasType,
			"SSE API CONTRACT: source.type should be omitted when empty (omitempty)")
	})

	t.Run("source omitted when source ID is empty", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.Source = datastore.AudioSource{} // empty source
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		_, exists := payload["source"]
		assert.False(t, exists,
			"SSE API CONTRACT: source should be omitted when source ID is empty (omitempty)")
	})
}

// TestSSEContract_BeginTimeEndTime_RFC3339 validates that beginTime and endTime
// are formatted as RFC3339 strings when set, and omitted when zero.
func TestSSEContract_BeginTimeEndTime_RFC3339(t *testing.T) {
	t.Parallel()

	t.Run("beginTime and endTime formatted as RFC3339 when set", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		// beginTime must be present and parseable as RFC3339
		beginTimeStr, exists := payload["beginTime"]
		require.True(t, exists, "SSE API CONTRACT: beginTime must be present when non-zero")
		beginTimeVal, ok := beginTimeStr.(string)
		require.True(t, ok, "SSE API CONTRACT: beginTime must be a string")

		parsedBegin, err := time.Parse(time.RFC3339, beginTimeVal)
		require.NoError(t, err, "SSE API CONTRACT: beginTime must be valid RFC3339")
		assert.Equal(t, 2024, parsedBegin.Year())
		assert.Equal(t, time.January, parsedBegin.Month())
		assert.Equal(t, 15, parsedBegin.Day())
		assert.Equal(t, 14, parsedBegin.Hour())
		assert.Equal(t, 30, parsedBegin.Minute())
		assert.Equal(t, 45, parsedBegin.Second())

		// endTime must be present and parseable as RFC3339
		endTimeStr, exists := payload["endTime"]
		require.True(t, exists, "SSE API CONTRACT: endTime must be present when non-zero")
		endTimeVal, ok := endTimeStr.(string)
		require.True(t, ok, "SSE API CONTRACT: endTime must be a string")

		parsedEnd, err := time.Parse(time.RFC3339, endTimeVal)
		require.NoError(t, err, "SSE API CONTRACT: endTime must be valid RFC3339")
		assert.Equal(t, 2024, parsedEnd.Year())
		assert.Equal(t, time.January, parsedEnd.Month())
		assert.Equal(t, 15, parsedEnd.Day())
		assert.Equal(t, 14, parsedEnd.Hour())
		assert.Equal(t, 30, parsedEnd.Minute())
		assert.Equal(t, 48, parsedEnd.Second())
	})

	t.Run("beginTime and endTime omitted when zero", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.BeginTime = time.Time{} // zero value
		note.EndTime = time.Time{}   // zero value
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		_, hasBeginTime := payload["beginTime"]
		assert.False(t, hasBeginTime,
			"SSE API CONTRACT: beginTime should be omitted when zero (omitempty)")

		_, hasEndTime := payload["endTime"]
		assert.False(t, hasEndTime,
			"SSE API CONTRACT: endTime should be omitted when zero (omitempty)")
	})
}

// TestSSEContract_IsNewSpecies_DaysSinceFirstSeen validates the species tracking
// metadata fields when set to non-zero values.
func TestSSEContract_IsNewSpecies_DaysSinceFirstSeen(t *testing.T) {
	t.Parallel()

	t.Run("isNewSpecies true and daysSinceFirstSeen zero", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)
		detection.IsNewSpecies = true
		detection.DaysSinceFirstSeen = 0

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		val, exists := payload["isNewSpecies"]
		require.True(t, exists, "SSE API CONTRACT: isNewSpecies must be present when true")
		assert.Equal(t, true, val,
			"SSE API CONTRACT: isNewSpecies must serialize as boolean true")

		// daysSinceFirstSeen=0 should be omitted (omitempty on int)
		_, hasDays := payload["daysSinceFirstSeen"]
		assert.False(t, hasDays,
			"SSE API CONTRACT: daysSinceFirstSeen should be omitted when zero (omitempty)")
	})

	t.Run("daysSinceFirstSeen present when non-zero", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)
		detection.IsNewSpecies = false
		detection.DaysSinceFirstSeen = 42

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		val, exists := payload["daysSinceFirstSeen"]
		require.True(t, exists,
			"SSE API CONTRACT: daysSinceFirstSeen must be present when non-zero")
		assert.InDelta(t, float64(42), val, 0.001,
			"SSE API CONTRACT: daysSinceFirstSeen value must match")

		// isNewSpecies=false should be omitted (omitempty)
		_, hasNew := payload["isNewSpecies"]
		assert.False(t, hasNew,
			"SSE API CONTRACT: isNewSpecies should be omitted when false (omitempty)")
	})

	t.Run("both present when both non-zero", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)
		detection.IsNewSpecies = true
		detection.DaysSinceFirstSeen = 7

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		isNew, exists := payload["isNewSpecies"]
		require.True(t, exists, "SSE API CONTRACT: isNewSpecies must be present when true")
		assert.Equal(t, true, isNew)

		days, exists := payload["daysSinceFirstSeen"]
		require.True(t, exists,
			"SSE API CONTRACT: daysSinceFirstSeen must be present when non-zero")
		assert.InDelta(t, float64(7), days, 0.001)
	})
}

// TestSSEContract_BirdImage_AllSubFields validates that all birdImage sub-fields
// are present with correct camelCase names and expected values.
func TestSSEContract_BirdImage_AllSubFields(t *testing.T) {
	t.Parallel()

	t.Run("all birdImage fields present with values", func(t *testing.T) {
		t.Parallel()

		detection := createTestSSEDetectionData()

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		birdImageRaw, exists := payload["birdImage"]
		require.True(t, exists, "SSE API CONTRACT: birdImage must be present")

		birdImage, ok := birdImageRaw.(map[string]any)
		require.True(t, ok, "SSE API CONTRACT: birdImage must be an object")

		// Verify all fields are present and have correct values
		assert.Equal(t, "https://example.com/bird.jpg", birdImage["url"],
			"SSE API CONTRACT: birdImage.url value must match")
		assert.Equal(t, "Parus major", birdImage["scientificName"],
			"SSE API CONTRACT: birdImage.scientificName value must match")
		assert.Equal(t, "CC BY-SA 4.0", birdImage["licenseName"],
			"SSE API CONTRACT: birdImage.licenseName value must match")
		assert.Equal(t, "https://creativecommons.org/licenses/by-sa/4.0/", birdImage["licenseURL"],
			"SSE API CONTRACT: birdImage.licenseURL value must match")
		assert.Equal(t, "Test Author", birdImage["authorName"],
			"SSE API CONTRACT: birdImage.authorName value must match")
		assert.Equal(t, "https://example.com/author", birdImage["authorURL"],
			"SSE API CONTRACT: birdImage.authorURL value must match")
		assert.Equal(t, "wikimedia", birdImage["sourceProvider"],
			"SSE API CONTRACT: birdImage.sourceProvider value must match")
	})

	t.Run("birdImage fields use camelCase not PascalCase", func(t *testing.T) {
		t.Parallel()

		detection := createTestSSEDetectionData()

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		jsonStr := string(jsonBytes)

		// These PascalCase versions must never appear inside the birdImage object
		forbiddenBirdImageFields := []string{
			`"URL"`,
			`"ScientificName"`,
			`"LicenseName"`,
			`"LicenseURL"`,
			`"AuthorName"`,
			`"AuthorURL"`,
			`"SourceProvider"`,
			`"CachedAt"`,
		}

		for _, field := range forbiddenBirdImageFields {
			assert.NotContains(t, jsonStr, field+`:`,
				"SSE API CONTRACT VIOLATION: PascalCase %s must not appear in birdImage", field)
		}
	})

	t.Run("birdImage present as empty object when nil image provided", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		detection := newSSEDetectionData(&note, nil)

		jsonBytes, err := json.Marshal(detection)
		require.NoError(t, err)

		var payload map[string]any
		err = json.Unmarshal(jsonBytes, &payload)
		require.NoError(t, err)

		// birdImage is not a pointer and has no omitempty, so it must always be present
		birdImageRaw, exists := payload["birdImage"]
		require.True(t, exists,
			"SSE API CONTRACT: birdImage must always be present (no omitempty)")

		birdImage, ok := birdImageRaw.(map[string]any)
		require.True(t, ok, "SSE API CONTRACT: birdImage must be an object")

		// url field should be present but empty (no omitempty on url)
		urlVal, urlExists := birdImage["url"]
		assert.True(t, urlExists, "SSE API CONTRACT: birdImage.url key must exist even when empty")
		assert.Empty(t, urlVal,
			"SSE API CONTRACT: birdImage.url must be empty when no image provided")
	})
}

// TestSSEContract_ZeroConfidence_NotOmitted verifies that a confidence value of 0.0
// is still present in the JSON payload (not omitted by omitempty).
func TestSSEContract_ZeroConfidence_NotOmitted(t *testing.T) {
	t.Parallel()

	note := createTestNoteWithAllFields()
	note.Confidence = 0.0 // Zero confidence is valid — must not be omitted
	birdImage := createTestBirdImage()
	detection := newSSEDetectionData(&note, &birdImage)

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	val, exists := payload["confidence"]
	require.True(t, exists,
		"SSE API CONTRACT: confidence must be present even when 0.0 (no omitempty)")
	assert.InDelta(t, 0.0, val, 0.001,
		"SSE API CONTRACT: confidence=0.0 must serialize as 0")
}

// TestSSEContract_AllFieldValues_RoundTrip performs an exhaustive round-trip
// verification of every field value through JSON serialization.
func TestSSEContract_AllFieldValues_RoundTrip(t *testing.T) {
	t.Parallel()

	note := createTestNoteWithAllFields()
	birdImage := createTestBirdImage()
	detection := newSSEDetectionData(&note, &birdImage)
	detection.IsNewSpecies = true
	detection.DaysSinceFirstSeen = 5

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	// ==========================================================================
	// ROOT-LEVEL FIELD VALUES
	// ==========================================================================

	t.Run("root field values", func(t *testing.T) {
		t.Parallel()

		assert.InDelta(t, float64(12345), payload["id"], 0.001,
			"id value mismatch")
		assert.Equal(t, "2024-01-15", payload["date"],
			"date value mismatch")
		assert.Equal(t, "14:30:45", payload["time"],
			"time value mismatch")
		assert.Equal(t, "gretit1", payload["speciesCode"],
			"speciesCode value mismatch")
		assert.Equal(t, "Parus major", payload["scientificName"],
			"scientificName value mismatch")
		assert.Equal(t, "Great Tit", payload["commonName"],
			"commonName value mismatch")
		assert.InDelta(t, 0.85, payload["confidence"], 0.001,
			"confidence value mismatch")
		assert.InDelta(t, 60.1699, payload["latitude"], 0.0001,
			"latitude value mismatch")
		assert.InDelta(t, 24.9384, payload["longitude"], 0.0001,
			"longitude value mismatch")
		assert.Equal(t, "clip_001.wav", payload["clipName"],
			"clipName should be filename only, not full path")
		assert.Equal(t, "correct", payload["verified"],
			"verified value mismatch")
		assert.Equal(t, true, payload["locked"],
			"locked value mismatch")
		assert.Equal(t, "new_detection", payload["eventType"],
			"eventType value mismatch")
		assert.Equal(t, true, payload["isNewSpecies"],
			"isNewSpecies value mismatch")
		assert.InDelta(t, float64(5), payload["daysSinceFirstSeen"], 0.001,
			"daysSinceFirstSeen value mismatch")
	})

	// ==========================================================================
	// TIMESTAMP FIELD
	// ==========================================================================

	t.Run("timestamp is valid RFC3339", func(t *testing.T) {
		t.Parallel()

		tsStr, ok := payload["timestamp"].(string)
		require.True(t, ok, "timestamp must be a string")

		_, err := time.Parse(time.RFC3339Nano, tsStr)
		require.NoError(t, err, "timestamp must be valid RFC3339")
	})

	// ==========================================================================
	// SOURCE FIELD VALUES
	// ==========================================================================

	t.Run("source field values", func(t *testing.T) {
		t.Parallel()

		source, ok := payload["source"].(map[string]any)
		require.True(t, ok, "source must be an object")

		assert.Equal(t, "rtsp_test123", source["id"],
			"source.id value mismatch")
		assert.Equal(t, "Garden Microphone", source["displayName"],
			"source.displayName value mismatch")
	})

	// ==========================================================================
	// BEGIN/END TIME FIELD VALUES
	// ==========================================================================

	t.Run("beginTime and endTime values", func(t *testing.T) {
		t.Parallel()

		beginTimeStr, ok := payload["beginTime"].(string)
		require.True(t, ok, "beginTime must be a string")
		assert.Equal(t, "2024-01-15T14:30:45Z", beginTimeStr,
			"beginTime RFC3339 value mismatch")

		endTimeStr, ok := payload["endTime"].(string)
		require.True(t, ok, "endTime must be a string")
		assert.Equal(t, "2024-01-15T14:30:48Z", endTimeStr,
			"endTime RFC3339 value mismatch")
	})

	// ==========================================================================
	// BIRDIMAGE FIELD VALUES
	// ==========================================================================

	t.Run("birdImage field values", func(t *testing.T) {
		t.Parallel()

		bi, ok := payload["birdImage"].(map[string]any)
		require.True(t, ok, "birdImage must be an object")

		assert.Equal(t, "https://example.com/bird.jpg", bi["url"])
		assert.Equal(t, "Parus major", bi["scientificName"])
		assert.Equal(t, "CC BY-SA 4.0", bi["licenseName"])
		assert.Equal(t, "https://creativecommons.org/licenses/by-sa/4.0/", bi["licenseURL"])
		assert.Equal(t, "Test Author", bi["authorName"])
		assert.Equal(t, "https://example.com/author", bi["authorURL"])
		assert.Equal(t, "wikimedia", bi["sourceProvider"])
	})
}

// TestSSEContract_OmitemptyFields_Comprehensive tests the omitempty behavior of
// every field that has the omitempty JSON tag.
func TestSSEContract_OmitemptyFields_Comprehensive(t *testing.T) {
	t.Parallel()

	// Create a detection with all omitempty fields at their zero values
	note := datastore.Note{
		ID:             1,
		Date:           "2024-01-01",
		Time:           "00:00:00",
		ScientificName: "Test species",
		CommonName:     "Test Bird",
		// All other fields at zero values:
		// SpeciesCode: ""     (omitempty)
		// Latitude: 0.0       (omitempty)
		// Longitude: 0.0      (omitempty)
		// ClipName: ""        (omitempty)
		// Verified: ""        (omitempty)
		// Locked: false       (no omitempty — always present)
		// BeginTime: zero     (omitempty via constructor logic)
		// EndTime: zero       (omitempty via constructor logic)
		// Source.ID: ""       (omitempty on Source pointer)
	}

	detection := newSSEDetectionData(&note, nil)
	// IsNewSpecies: false    (omitempty)
	// DaysSinceFirstSeen: 0  (omitempty)

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	// Fields that MUST be present even at zero values (no omitempty)
	t.Run("always-present fields", func(t *testing.T) {
		t.Parallel()

		alwaysPresentFields := []string{
			"id",             // uint, no omitempty
			"date",           // string, no omitempty
			"time",           // string, no omitempty
			"scientificName", // string, no omitempty
			"commonName",     // string, no omitempty
			"confidence",     // float64, no omitempty — 0.0 is valid
			"locked",         // bool, no omitempty
			"birdImage",      // struct, no omitempty
			"timestamp",      // time.Time, no omitempty
			"eventType",      // string, no omitempty
		}

		for _, field := range alwaysPresentFields {
			assert.Contains(t, payload, field,
				"SSE API CONTRACT: field '%s' must always be present (no omitempty)", field)
		}
	})

	// Fields that MUST be omitted at zero values (with omitempty)
	t.Run("omitted-when-zero fields", func(t *testing.T) {
		t.Parallel()

		omittedFields := []string{
			"speciesCode",        // string, omitempty
			"latitude",           // float64, omitempty
			"longitude",          // float64, omitempty
			"clipName",           // string, omitempty
			"verified",           // string, omitempty
			"source",             // *SSESourceInfo, omitempty (nil pointer)
			"beginTime",          // string, omitempty (not set by constructor when zero)
			"endTime",            // string, omitempty (not set by constructor when zero)
			"isNewSpecies",       // bool, omitempty
			"daysSinceFirstSeen", // int, omitempty
		}

		for _, field := range omittedFields {
			_, exists := payload[field]
			assert.False(t, exists,
				"SSE API CONTRACT: field '%s' should be omitted at zero value (omitempty)", field)
		}
	})
}

// TestSSEContract_NoUnexpectedFields verifies that the JSON payload contains only
// the documented contract fields and no undocumented additions.
func TestSSEContract_NoUnexpectedFields(t *testing.T) {
	t.Parallel()

	detection := createTestSSEDetectionData()

	jsonBytes, err := json.Marshal(detection)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(jsonBytes, &payload)
	require.NoError(t, err)

	// All allowed root-level field names
	allowedRootFields := map[string]bool{
		"id":                 true,
		"date":               true,
		"time":               true,
		"scientificName":     true,
		"commonName":         true,
		"speciesCode":        true,
		"confidence":         true,
		"latitude":           true,
		"longitude":          true,
		"clipName":           true,
		"source":             true,
		"beginTime":          true,
		"endTime":            true,
		"verified":           true,
		"locked":             true,
		"birdImage":          true,
		"timestamp":          true,
		"eventType":          true,
		"isNewSpecies":       true,
		"daysSinceFirstSeen": true,
	}

	for field := range payload {
		assert.True(t, allowedRootFields[field],
			"SSE API CONTRACT: unexpected field '%s' found in payload — add to allowlist or remove from struct", field)
	}

	// Check birdImage sub-fields
	allowedBirdImageFields := map[string]bool{
		"url":            true,
		"scientificName": true,
		"licenseName":    true,
		"licenseURL":     true,
		"authorName":     true,
		"authorURL":      true,
		"sourceProvider": true,
	}

	birdImage, ok := payload["birdImage"].(map[string]any)
	require.True(t, ok, "birdImage must be an object")

	for field := range birdImage {
		assert.True(t, allowedBirdImageFields[field],
			"SSE API CONTRACT: unexpected birdImage field '%s' found — add to allowlist or remove from struct", field)
	}

	// Check source sub-fields
	allowedSourceFields := map[string]bool{
		"id":          true,
		"type":        true,
		"displayName": true,
	}

	if sourceRaw, exists := payload["source"]; exists {
		source, ok := sourceRaw.(map[string]any)
		require.True(t, ok, "source must be an object")

		for field := range source {
			assert.True(t, allowedSourceFields[field],
				"SSE API CONTRACT: unexpected source field '%s' found — add to allowlist or remove from struct", field)
		}
	}
}

// TestSSEContract_NewSSEDetectionData_Constructor verifies that the constructor
// correctly maps all Note fields and sanitizes sensitive data.
func TestSSEContract_NewSSEDetectionData_Constructor(t *testing.T) {
	t.Parallel()

	t.Run("clipName stripped to basename", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.ClipName = "/deeply/nested/path/to/recordings/2024/clip.wav"
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		assert.Equal(t, "clip.wav", detection.ClipName,
			"ClipName must be stripped to filename only")
	})

	t.Run("empty clipName remains empty", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		note.ClipName = ""
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		assert.Empty(t, detection.ClipName,
			"Empty ClipName must remain empty, not '.'")
	})

	t.Run("eventType always set to new_detection", func(t *testing.T) {
		t.Parallel()

		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)

		assert.Equal(t, "new_detection", detection.EventType,
			"EventType must always be 'new_detection'")
	})

	t.Run("timestamp set to approximately now", func(t *testing.T) {
		t.Parallel()

		before := time.Now()
		note := createTestNoteWithAllFields()
		birdImage := createTestBirdImage()
		detection := newSSEDetectionData(&note, &birdImage)
		after := time.Now()

		assert.False(t, detection.Timestamp.Before(before),
			"Timestamp must not be before construction time")
		assert.False(t, detection.Timestamp.After(after),
			"Timestamp must not be after construction time")
	})
}
