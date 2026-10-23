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
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

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
		Unlikely:       true,
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
	det := apicore.NewSSEDetectionData(&note, &birdImage)
	det.IsNewSpecies = true
	det.DaysSinceFirstSeen = 14
	return det
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
	detection := apicore.NewSSEDetectionData(&note, &birdImage)

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

// =============================================================================
// EXHAUSTIVE FIELD VALUE TESTS
// =============================================================================

// TestSSEContract_AllFieldValues_RoundTrip performs an exhaustive round-trip
// verification of every field value through JSON serialization.
func TestSSEContract_AllFieldValues_RoundTrip(t *testing.T) {
	t.Parallel()

	note := createTestNoteWithAllFields()
	birdImage := createTestBirdImage()
	detection := apicore.NewSSEDetectionData(&note, &birdImage)
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
		assert.Equal(t, true, payload["unlikely"],
			"unlikely value mismatch")
		assert.Equal(t, "new_detection", payload["eventType"],
			"eventType value mismatch")
		assert.Equal(t, true, payload["isNewSpecies"],
			"isNewSpecies value mismatch")
		assert.InDelta(t, float64(5), payload["daysSinceFirstSeen"], 0.001,
			"daysSinceFirstSeen value mismatch")
	})

	// The fixture sets locked and unlikely to the same value, so a constructor
	// that mixed the two up would pass the checks above.
	t.Run("locked and unlikely follow their own note fields", func(t *testing.T) {
		t.Parallel()

		independentNote := createTestNoteWithAllFields()
		independentNote.Locked = false
		independentJSON, err := json.Marshal(apicore.NewSSEDetectionData(&independentNote, nil))
		require.NoError(t, err)

		var independentPayload map[string]any
		require.NoError(t, json.Unmarshal(independentJSON, &independentPayload))

		assert.Equal(t, false, independentPayload["locked"],
			"locked must follow note.Locked, not note.Unlikely")
		assert.Equal(t, true, independentPayload["unlikely"],
			"unlikely must follow note.Unlikely, not note.Locked")
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

		assert.Equal(t, imageprovider.ProxyImageURL("Parus major"), bi["url"])
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
		// Unlikely: false     (omitempty)
		// Locked: false       (no omitempty — always present)
		// BeginTime: zero     (omitempty via constructor logic)
		// EndTime: zero       (omitempty via constructor logic)
		// Source.ID: ""       (omitempty on Source pointer)
	}

	detection := apicore.NewSSEDetectionData(&note, nil)
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
			"unlikely",           // bool, omitempty
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

	// The constructor copies only the source ID and display name, so a source
	// that is present still has no type.
	t.Run("source type omitted when source is present", func(t *testing.T) {
		t.Parallel()

		withSource := createTestNoteWithAllFields()
		sourceJSON, err := json.Marshal(apicore.NewSSEDetectionData(&withSource, nil))
		require.NoError(t, err)

		var sourcePayload map[string]any
		require.NoError(t, json.Unmarshal(sourceJSON, &sourcePayload))

		source, ok := sourcePayload["source"].(map[string]any)
		require.True(t, ok, "source must be an object when the note has a source ID")
		assert.NotContains(t, source, "type",
			"SSE API CONTRACT: source.type should be omitted when empty (omitempty)")
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
		"unlikely":           true,
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

// TestSSEContract_NewSSEDetectionData_TimestampIsConstructionTime verifies that
// the constructor stamps the event with the time it ran.
func TestSSEContract_NewSSEDetectionData_TimestampIsConstructionTime(t *testing.T) {
	t.Parallel()

	before := time.Now()
	note := createTestNoteWithAllFields()
	birdImage := createTestBirdImage()
	detection := apicore.NewSSEDetectionData(&note, &birdImage)
	after := time.Now()

	assert.False(t, detection.Timestamp.Before(before),
		"Timestamp must not be before construction time")
	assert.False(t, detection.Timestamp.After(after),
		"Timestamp must not be after construction time")
}
