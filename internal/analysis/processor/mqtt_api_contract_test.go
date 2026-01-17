// mqtt_api_contract_test.go: Tests for MQTT JSON payload backward compatibility.
//
// IMPORTANT: These tests verify the MQTT API contract. The JSON field names tested here
// are part of the PUBLIC API used by Home Assistant and other MQTT integrations.
//
// DO NOT MODIFY these expected field names without:
// 1. Explicit approval from maintainers
// 2. A migration plan for existing integrations
// 3. Documentation in release notes
//
// Breaking changes to these field names will break user integrations.
// See: https://github.com/tphakala/birdnet-go/discussions/1759
package processor

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
// MQTT API CONTRACT - FIXED FIELD NAMES
// =============================================================================
//
// The following field names are part of the MQTT API contract and MUST NOT be
// changed without explicit maintainer approval. These are used by:
// - Home Assistant MQTT integrations
// - Custom MQTT consumers
// - Third-party automation tools
//
// Any change to these names is a BREAKING CHANGE.
// =============================================================================

// mqttAPIContractFields defines the expected JSON field names for MQTT messages.
// These are FROZEN and must not be changed without explicit approval.
//
// MODIFICATION POLICY:
// - DO NOT change existing field names
// - New fields may be added (with camelCase for new fields, but existing PascalCase must be preserved)
// - Removal of fields requires deprecation notice and migration period
var mqttAPIContractFields = struct {
	// Detection message root-level fields (from embedded datastore.Note)
	// These use PascalCase because Go's default JSON marshaling is used
	CommonName     string
	ScientificName string
	Confidence     string
	Date           string
	Time           string
	Latitude       string
	Longitude      string
	ClipName       string
	ProcessingTime string

	// Detection message root-level fields (explicit JSON tags)
	DetectionID string // camelCase - database ID for URL construction (issue #1748)
	SourceID    string // camelCase - added for Home Assistant discovery
	Occurrence  string // lowercase with omitempty
	BirdImage   string // PascalCase - DO NOT CHANGE (backward compatibility)

	// BirdImage nested fields (from imageprovider.BirdImage)
	// These use PascalCase because Go's default JSON marshaling is used
	BirdImageURL            string
	BirdImageScientificName string
	BirdImageLicenseName    string
	BirdImageLicenseURL     string
	BirdImageAuthorName     string
	BirdImageAuthorURL      string
	BirdImageCachedAt       string
	BirdImageSourceProvider string
}{
	// Root-level fields from embedded Note (PascalCase - Go default)
	CommonName:     "CommonName",
	ScientificName: "ScientificName",
	Confidence:     "Confidence",
	Date:           "Date",
	Time:           "Time",
	Latitude:       "Latitude",
	Longitude:      "Longitude",
	ClipName:       "ClipName",
	ProcessingTime: "ProcessingTime",

	// Root-level fields with explicit tags
	DetectionID: "detectionId", // camelCase - database ID for URL construction (issue #1748)
	SourceID:    "sourceId",    // camelCase - new field for HA
	Occurrence:  "occurrence",  // lowercase with omitempty
	BirdImage:   "BirdImage",   // PascalCase - FROZEN for backward compatibility

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

// TestMQTTAPIContract_NoteWithBirdImage_FieldNames verifies that the MQTT JSON
// payload uses the correct field names for backward compatibility.
//
// IMPORTANT: This test is a CONTRACT test. If it fails, you are likely breaking
// existing MQTT integrations. DO NOT modify the expected values without explicit
// maintainer approval and a migration plan.
func TestMQTTAPIContract_NoteWithBirdImage_FieldNames(t *testing.T) {
	t.Parallel()

	// Create a complete NoteWithBirdImage struct with all fields populated
	note := NoteWithBirdImage{
		Note: datastore.Note{
			ID:             12345, // Simulated database ID
			CommonName:     "American Robin",
			ScientificName: "Turdus migratorius",
			Confidence:     0.95,
			Date:           "2024-01-15",
			Time:           "12:00:00",
			Latitude:       42.3601,
			Longitude:      -71.0589,
			ClipName:       "test_clip.wav",
			ProcessingTime: 150 * time.Millisecond,
			Occurrence:     0.75,
			Source:         testAudioSource(),
		},
		DetectionID: 12345, // Should match Note.ID for URL construction
		SourceID:    "test-source-1",
		BirdImage: imageprovider.BirdImage{
			URL:            "https://example.com/bird.jpg",
			ScientificName: "Turdus migratorius",
			LicenseName:    "CC BY-SA 4.0",
			LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
			AuthorName:     "Test Author",
			AuthorURL:      "https://example.com/author",
			CachedAt:       time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			SourceProvider: "wikimedia",
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(note)
	require.NoError(t, err, "Failed to marshal NoteWithBirdImage to JSON")

	// Parse back to generic map to check field names
	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err, "Failed to unmarshal JSON to map")

	// Log the actual JSON for debugging
	t.Logf("MQTT JSON payload:\n%s", string(jsonData))

	// ==========================================================================
	// CONTRACT ASSERTIONS - DO NOT MODIFY EXPECTED VALUES
	// ==========================================================================
	// These assertions verify the API contract. Changing the expected values
	// here means you are accepting a breaking change to the MQTT API.
	// ==========================================================================

	t.Run("Root level Note fields use PascalCase", func(t *testing.T) {
		// FROZEN: These field names are part of the API contract
		assert.Contains(t, jsonMap, mqttAPIContractFields.CommonName,
			"MQTT API CONTRACT VIOLATION: CommonName field must be PascalCase")
		assert.Contains(t, jsonMap, mqttAPIContractFields.ScientificName,
			"MQTT API CONTRACT VIOLATION: ScientificName field must be PascalCase")
		assert.Contains(t, jsonMap, mqttAPIContractFields.Confidence,
			"MQTT API CONTRACT VIOLATION: Confidence field must be PascalCase")
		assert.Contains(t, jsonMap, mqttAPIContractFields.Date,
			"MQTT API CONTRACT VIOLATION: Date field must be PascalCase")
		assert.Contains(t, jsonMap, mqttAPIContractFields.Time,
			"MQTT API CONTRACT VIOLATION: Time field must be PascalCase")
		assert.Contains(t, jsonMap, mqttAPIContractFields.ClipName,
			"MQTT API CONTRACT VIOLATION: ClipName field must be PascalCase")
	})

	t.Run("DetectionID uses camelCase (for URL construction)", func(t *testing.T) {
		// This is a new field for constructing API URLs (issue #1748)
		assert.Contains(t, jsonMap, mqttAPIContractFields.DetectionID,
			"MQTT API CONTRACT: detectionId field must be present for URL construction")
		detectionID, ok := jsonMap[mqttAPIContractFields.DetectionID].(float64)
		require.True(t, ok, "detectionId must be a number")
		assert.InDelta(t, 12345, detectionID, 0.001,
			"detectionId value mismatch")
	})

	t.Run("SourceID uses camelCase (new field for HA)", func(t *testing.T) {
		// This is a new field added for Home Assistant, uses camelCase
		assert.Contains(t, jsonMap, mqttAPIContractFields.SourceID,
			"MQTT API CONTRACT: sourceId field must be present for HA filtering")
		assert.Equal(t, "test-source-1", jsonMap[mqttAPIContractFields.SourceID],
			"sourceId value mismatch")
	})

	t.Run("BirdImage field uses PascalCase for backward compatibility", func(t *testing.T) {
		// FROZEN: BirdImage must be PascalCase for backward compatibility
		// See: https://github.com/tphakala/birdnet-go/discussions/1759
		assert.Contains(t, jsonMap, mqttAPIContractFields.BirdImage,
			"MQTT API CONTRACT VIOLATION: BirdImage field must be PascalCase (was changed to camelCase in error)")

		// Verify it's NOT using camelCase
		assert.NotContains(t, jsonMap, "birdImage",
			"MQTT API CONTRACT VIOLATION: BirdImage must NOT be camelCase - this breaks existing integrations")
	})

	t.Run("BirdImage nested fields use PascalCase", func(t *testing.T) {
		// Get the BirdImage object
		birdImageRaw, ok := jsonMap[mqttAPIContractFields.BirdImage]
		require.True(t, ok, "BirdImage field must be present")

		birdImage, ok := birdImageRaw.(map[string]any)
		require.True(t, ok, "BirdImage must be an object")

		// FROZEN: These nested field names are part of the API contract
		assert.Contains(t, birdImage, mqttAPIContractFields.BirdImageURL,
			"MQTT API CONTRACT VIOLATION: BirdImage.URL field must be PascalCase")
		assert.Contains(t, birdImage, mqttAPIContractFields.BirdImageLicenseName,
			"MQTT API CONTRACT VIOLATION: BirdImage.LicenseName field must be PascalCase")
		assert.Contains(t, birdImage, mqttAPIContractFields.BirdImageAuthorName,
			"MQTT API CONTRACT VIOLATION: BirdImage.AuthorName field must be PascalCase")
		assert.Contains(t, birdImage, mqttAPIContractFields.BirdImageSourceProvider,
			"MQTT API CONTRACT VIOLATION: BirdImage.SourceProvider field must be PascalCase")

		// Verify values
		assert.Equal(t, "https://example.com/bird.jpg", birdImage[mqttAPIContractFields.BirdImageURL],
			"BirdImage.URL value mismatch")
	})

	t.Run("Occurrence uses lowercase with omitempty", func(t *testing.T) {
		// occurrence field uses lowercase (from Note struct JSON tag)
		assert.Contains(t, jsonMap, mqttAPIContractFields.Occurrence,
			"MQTT API CONTRACT: occurrence field must be present when non-zero")
		occurrence, ok := jsonMap[mqttAPIContractFields.Occurrence].(float64)
		require.True(t, ok, "occurrence must be a number")
		assert.InDelta(t, 0.75, occurrence, 0.001, "occurrence value mismatch")
	})
}

// TestMQTTAPIContract_BirdImageURL_Accessible verifies that Home Assistant
// integrations can access the bird image URL at the expected path.
//
// Home Assistant users access this via: value_json.BirdImage.URL
// This test ensures that path remains valid.
func TestMQTTAPIContract_BirdImageURL_Accessible(t *testing.T) {
	t.Parallel()

	note := NoteWithBirdImage{
		Note: datastore.Note{
			CommonName:     "American Robin",
			ScientificName: "Turdus migratorius",
			Confidence:     0.88,
			Source:         testAudioSource(),
		},
		SourceID: "backyard-mic",
		BirdImage: imageprovider.BirdImage{
			URL:            "https://upload.wikimedia.org/bird.jpg",
			ScientificName: "Turdus migratorius",
			LicenseName:    "CC BY 2.0",
			AuthorName:     "Photographer Name",
		},
	}

	jsonData, err := json.Marshal(note)
	require.NoError(t, err)

	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err)

	// ==========================================================================
	// SIMULATE HOME ASSISTANT VALUE TEMPLATE ACCESS
	// ==========================================================================
	// Home Assistant users access the image URL via: value_json.BirdImage.URL
	// This simulates that access pattern to ensure it works.
	// ==========================================================================

	// Step 1: Access BirdImage (must be PascalCase)
	birdImageRaw, exists := jsonMap["BirdImage"]
	require.True(t, exists,
		"HOME ASSISTANT INTEGRATION BROKEN: Cannot access value_json.BirdImage - field not found")

	birdImage, ok := birdImageRaw.(map[string]any)
	require.True(t, ok,
		"HOME ASSISTANT INTEGRATION BROKEN: value_json.BirdImage is not an object")

	// Step 2: Access URL within BirdImage (must be PascalCase)
	url, exists := birdImage["URL"]
	require.True(t, exists,
		"HOME ASSISTANT INTEGRATION BROKEN: Cannot access value_json.BirdImage.URL - field not found")

	assert.Equal(t, "https://upload.wikimedia.org/bird.jpg", url,
		"BirdImage.URL value incorrect")

	// Verify the WRONG paths don't work (these would be breaking changes)
	_, wrongPath1 := jsonMap["birdImage"] // camelCase - WRONG
	assert.False(t, wrongPath1,
		"API CONTRACT: 'birdImage' (camelCase) should NOT exist - use 'BirdImage' (PascalCase)")
}

// TestMQTTAPIContract_OccurrenceOmittedWhenZero verifies the omitempty behavior.
func TestMQTTAPIContract_OccurrenceOmittedWhenZero(t *testing.T) {
	t.Parallel()

	note := NoteWithBirdImage{
		Note: datastore.Note{
			CommonName:     "Blue Jay",
			ScientificName: "Cyanocitta cristata",
			Confidence:     0.92,
			Occurrence:     0.0, // Zero - should be omitted
			Source:         testAudioSource(),
		},
		SourceID:  "test-source",
		BirdImage: imageprovider.BirdImage{},
	}

	jsonData, err := json.Marshal(note)
	require.NoError(t, err)

	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err)

	// occurrence should be omitted when zero (omitempty)
	_, hasOccurrence := jsonMap["occurrence"]
	assert.False(t, hasOccurrence,
		"MQTT API CONTRACT: occurrence field should be omitted when value is zero (omitempty)")
}

// TestMQTTAPIContract_AllExpectedFieldsPresent is a comprehensive check that all
// expected fields are present in the MQTT payload.
func TestMQTTAPIContract_AllExpectedFieldsPresent(t *testing.T) {
	t.Parallel()

	note := NoteWithBirdImage{
		Note: datastore.Note{
			ID:             67890, // Database primary key
			CommonName:     "House Sparrow",
			ScientificName: "Passer domesticus",
			Confidence:     0.87,
			Date:           "2024-06-15",
			Time:           "08:30:00",
			Latitude:       34.0522,
			Longitude:      -118.2437,
			ClipName:       "detection_001.wav",
			ProcessingTime: 125 * time.Millisecond,
			Occurrence:     0.65,
			Source:         testAudioSource(),
		},
		DetectionID: 67890, // Should match Note.ID for URL construction
		SourceID:    "garden-mic",
		BirdImage: imageprovider.BirdImage{
			URL:            "https://example.com/sparrow.jpg",
			ScientificName: "Passer domesticus",
			LicenseName:    "CC BY-SA 4.0",
			LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
			AuthorName:     "Bird Photographer",
			AuthorURL:      "https://example.com/photographer",
			CachedAt:       time.Now(),
			SourceProvider: "flickr",
		},
	}

	jsonData, err := json.Marshal(note)
	require.NoError(t, err)

	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err)

	// ==========================================================================
	// EXPECTED ROOT-LEVEL FIELDS (FROZEN CONTRACT)
	// ==========================================================================
	expectedRootFields := []string{
		"CommonName",     // PascalCase - from embedded Note
		"ScientificName", // PascalCase - from embedded Note
		"Confidence",     // PascalCase - from embedded Note
		"Date",           // PascalCase - from embedded Note
		"Time",           // PascalCase - from embedded Note
		"Latitude",       // PascalCase - from embedded Note
		"Longitude",      // PascalCase - from embedded Note
		"ClipName",       // PascalCase - from embedded Note
		"ProcessingTime", // PascalCase - from embedded Note
		"occurrence",     // lowercase - from embedded Note (explicit tag)
		"detectionId",    // camelCase - database ID for URL construction (issue #1748)
		"sourceId",       // camelCase - new field for HA discovery
		"BirdImage",      // PascalCase - FROZEN for backward compatibility
	}

	for _, field := range expectedRootFields {
		assert.Contains(t, jsonMap, field,
			"MQTT API CONTRACT: Expected field '%s' not found in JSON payload", field)
	}

	// ==========================================================================
	// EXPECTED BIRDIMAGE NESTED FIELDS (FROZEN CONTRACT)
	// ==========================================================================
	birdImage := jsonMap["BirdImage"].(map[string]any)
	expectedBirdImageFields := []string{
		"URL",            // PascalCase - Go default
		"ScientificName", // PascalCase - Go default
		"LicenseName",    // PascalCase - Go default
		"LicenseURL",     // PascalCase - Go default
		"AuthorName",     // PascalCase - Go default
		"AuthorURL",      // PascalCase - Go default
		"CachedAt",       // PascalCase - Go default
		"SourceProvider", // PascalCase - Go default
	}

	for _, field := range expectedBirdImageFields {
		assert.Contains(t, birdImage, field,
			"MQTT API CONTRACT: Expected BirdImage.%s field not found", field)
	}
}

// TestMQTTAPIContract_NoUnexpectedCamelCaseConversions verifies that fields that
// should be PascalCase have not been accidentally converted to camelCase.
//
// This test catches the exact bug that was introduced in PR #1749.
func TestMQTTAPIContract_NoUnexpectedCamelCaseConversions(t *testing.T) {
	t.Parallel()

	note := NoteWithBirdImage{
		Note: datastore.Note{
			CommonName:     "Test Bird",
			ScientificName: "Testus birdus",
			Confidence:     0.9,
			Source:         testAudioSource(),
		},
		SourceID: "test",
		BirdImage: imageprovider.BirdImage{
			URL: "https://example.com/test.jpg",
		},
	}

	jsonData, err := json.Marshal(note)
	require.NoError(t, err)

	jsonStr := string(jsonData)

	// ==========================================================================
	// FORBIDDEN FIELD NAMES - These indicate breaking changes
	// ==========================================================================
	// If any of these appear in the JSON, it means a field was incorrectly
	// changed from PascalCase to camelCase, breaking the API contract.
	// ==========================================================================

	forbiddenFields := []struct {
		wrong   string
		correct string
		reason  string
	}{
		{"birdImage", "BirdImage", "PR #1749 regression - breaks Home Assistant integrations"},
		{"commonName", "CommonName", "Would break existing MQTT consumers"},
		{"scientificName", "ScientificName", "Would break existing MQTT consumers"},
		{"confidence", "Confidence", "Would break existing MQTT consumers"},
		{"clipName", "ClipName", "Would break existing MQTT consumers"},
		{"processingTime", "ProcessingTime", "Would break existing MQTT consumers"},
	}

	for _, f := range forbiddenFields {
		// Check that the wrong (camelCase) version does NOT appear as a key
		// We need to check for it as a JSON key, not just substring
		assert.NotContains(t, jsonStr, `"`+f.wrong+`":`,
			"MQTT API CONTRACT VIOLATION: Found '%s' but should be '%s'. Reason: %s",
			f.wrong, f.correct, f.reason)
	}
}
