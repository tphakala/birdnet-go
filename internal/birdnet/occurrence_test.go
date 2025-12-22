package birdnet

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observation"
	"go.uber.org/goleak"
)

// Test constants for observation testing.
const (
	testNodeName        = "TestNode"
	testSpecies         = "Turdus merula_blackbird" // Format: "Scientific Name_Common Name"
	testAudioSource     = "test_audio"
	testClipName        = "test_clip.wav"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestGetSpeciesOccurrence(t *testing.T) {
	tests := []struct {
		name        string
		latitude    float64
		longitude   float64
		species     string
		expected    float64
		description string
	}{
		{
			name:        "location_not_set",
			latitude:    0,
			longitude:   0,
			species:     "Turdus_merula",
			expected:    0.0,
			description: "Should return 0 when location is not set",
		},
		{
			name:        "no_interpreter",
			latitude:    52.5200,
			longitude:   13.4050,
			species:     "Turdus_merula",
			expected:    0.0,
			description: "Should return 0 when range filter model is not loaded",
		},
		{
			name:        "unknown_species",
			latitude:    52.5200,
			longitude:   13.4050,
			species:     "Unknown_species",
			expected:    0.0,
			description: "Should return 0 for unknown species",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a fresh BirdNET instance for each subtest
			settings := &conf.Settings{
				BirdNET: conf.BirdNETConfig{
					Latitude:  tt.latitude,
					Longitude: tt.longitude,
					RangeFilter: conf.RangeFilterSettings{
						Threshold: 0.01,
					},
				},
			}

			bn := &BirdNET{
				Settings: settings,
			}

			occurrence := bn.GetSpeciesOccurrence(tt.species)
			assert.InDelta(t, tt.expected, occurrence, 0.001, tt.description)
		})
	}
}

func TestObservationWithOccurrence(t *testing.T) {
	t.Parallel()

	// Create test settings
	settings := &conf.Settings{}
	settings.Main.Name = testNodeName
	settings.BirdNET = conf.BirdNETConfig{
		Latitude:    52.5200,
		Longitude:   13.4050,
		Threshold:   0.5,
		Sensitivity: 1.0,
	}

	// Create a test observation with occurrence value
	beginTime := time.Now()
	endTime := beginTime.Add(3 * time.Second)
	species := testSpecies
	confidence := 0.85
	source := testAudioSource
	clipName := testClipName
	elapsedTime := 100 * time.Millisecond
	occurrence := 0.75 // Test occurrence value

	// Create the note
	note := observation.New(settings, beginTime, endTime, species, confidence, source, clipName, elapsedTime, occurrence)

	// Verify all fields are set correctly
	assert.Equal(t, testNodeName, note.SourceNode)
	assert.Equal(t, "Turdus merula", note.ScientificName)
	assert.Equal(t, "blackbird", note.CommonName)
	assert.InEpsilon(t, 0.85, note.Confidence, 0.001)
	assert.InEpsilon(t, 0.75, note.Occurrence, 0.001, "Occurrence should be set to the provided value")
	assert.InEpsilon(t, 52.5200, note.Latitude, 0.001)
	assert.InEpsilon(t, 13.4050, note.Longitude, 0.001)
	assert.InEpsilon(t, 0.5, note.Threshold, 0.001)
	assert.InEpsilon(t, 1.0, note.Sensitivity, 0.001)
	assert.Equal(t, testClipName, note.ClipName)
	assert.Equal(t, elapsedTime, note.ProcessingTime)
}

func TestObservationWithOccurrence_Rounding(t *testing.T) {
	t.Parallel()

	// Create test settings
	settings := &conf.Settings{}
	settings.Main.Name = testNodeName
	settings.BirdNET = conf.BirdNETConfig{
		Latitude:    52.5200,
		Longitude:   13.4050,
		Threshold:   0.5,
		Sensitivity: 1.0,
	}

	// Create a test observation with occurrence value that needs rounding
	beginTime := time.Now()
	endTime := beginTime.Add(3 * time.Second)
	species := testSpecies
	confidence := 0.853 // Value that gets rounded to two decimals
	source := testAudioSource
	clipName := testClipName
	elapsedTime := 100 * time.Millisecond
	occurrence := 0.853 // Test occurrence value that should be rounded

	// Create the note
	note := observation.New(settings, beginTime, endTime, species, confidence, source, clipName, elapsedTime, occurrence)

	// Verify rounding behavior - confidence gets rounded to two decimals
	assert.InDelta(t, 0.85, note.Confidence, 0.01, "Confidence should be rounded to two decimals")
	assert.InEpsilon(t, 0.853, note.Occurrence, 0.001, "Occurrence should preserve original precision")
}

func TestNoteJSONIncludesOccurrence(t *testing.T) {
	t.Parallel()

	// Build Settings with Main.Name and BirdNET config
	settings := &conf.Settings{}
	settings.Main.Name = testNodeName
	settings.BirdNET = conf.BirdNETConfig{
		Latitude:    52.5200,
		Longitude:   13.4050,
		Threshold:   0.5,
		Sensitivity: 1.0,
	}

	// Create test observation with occurrence 0.23
	beginTime := time.Now()
	endTime := beginTime.Add(3 * time.Second)
	species := testSpecies
	confidence := 0.85
	source := testAudioSource
	clipName := testClipName
	elapsedTime := 100 * time.Millisecond
	occurrence := 0.23

	// Construct the note via observation.New
	note := observation.New(settings, beginTime, endTime, species, confidence, source, clipName, elapsedTime, occurrence)

	// Marshal the note to JSON
	jsonData, err := json.Marshal(note)
	require.NoError(t, err, "JSON marshaling should not error")

	// Unmarshal into map to properly test the occurrence field
	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err, "JSON unmarshaling should not error")

	// Assert that the occurrence key exists and has the correct numeric value
	occurrenceValue, exists := jsonMap["occurrence"]
	require.True(t, exists, "JSON should contain occurrence key")

	// Cast to float64 and assert the value with epsilon for floating-point comparison
	occurrenceFloat, ok := occurrenceValue.(float64)
	require.True(t, ok, "occurrence value should be a float64")
	assert.InEpsilon(t, 0.23, occurrenceFloat, 0.001, "occurrence value should equal 0.23")
}

func TestNoteJSONOmitsOccurrenceWhenZero(t *testing.T) {
	t.Parallel()

	// Build Settings with Main.Name and BirdNET config
	settings := &conf.Settings{}
	settings.Main.Name = "TestNode"
	settings.BirdNET = conf.BirdNETConfig{
		Latitude:    52.5200,
		Longitude:   13.4050,
		Threshold:   0.5,
		Sensitivity: 1.0,
	}

	// Create test observation with occurrence 0.0 (should be omitted due to omitzero tag)
	beginTime := time.Now()
	endTime := beginTime.Add(3 * time.Second)
	species := "Turdus merula_blackbird"
	confidence := 0.85
	source := "test_audio"
	clipName := "test_clip.wav"
	elapsedTime := 100 * time.Millisecond
	occurrence := 0.0

	// Construct the note via observation.New
	note := observation.New(settings, beginTime, endTime, species, confidence, source, clipName, elapsedTime, occurrence)

	// Marshal the note to JSON
	jsonData, err := json.Marshal(note)
	require.NoError(t, err, "JSON marshaling should not error")

	// Unmarshal into map to verify occurrence key is omitted
	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err, "JSON unmarshaling should not error")

	// Assert that the occurrence key does NOT exist when value is zero (omitzero behavior)
	_, exists := jsonMap["occurrence"]
	assert.False(t, exists, "JSON should omit occurrence key when value is 0.0 due to omitzero tag")
}
