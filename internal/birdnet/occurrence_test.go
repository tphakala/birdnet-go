package birdnet

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSpeciesOccurrence(t *testing.T) {
	// Create a mock BirdNET instance with test settings
	settings := &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			Latitude:  52.5200,  // Berlin coordinates
			Longitude: 13.4050,
			RangeFilter: conf.RangeFilterSettings{
				Threshold: 0.01,
			},
		},
	}

	bn := &BirdNET{
		Settings: settings,
	}

	// Test case 1: Location not set - should return 0
	bn.Settings.BirdNET.Latitude = 0
	bn.Settings.BirdNET.Longitude = 0
	occurrence := bn.GetSpeciesOccurrence("Turdus_merula")
	assert.InDelta(t, 0.0, occurrence, 0.001, "Should return 0 when location is not set")

	// Test case 2: Location set but no interpreter (returns 0 gracefully)
	bn.Settings.BirdNET.Latitude = 52.5200
	bn.Settings.BirdNET.Longitude = 13.4050
	// This will return 0 because RangeInterpreter is nil (no model loaded)
	occurrence = bn.GetSpeciesOccurrence("Turdus_merula")
	assert.InDelta(t, 0.0, occurrence, 0.001, "Should return 0 when range filter model is not loaded")
	
	// Note: Testing with actual models would require full BirdNET initialization
	// which is covered in integration tests
}

func TestObservationWithOccurrence(t *testing.T) {
	// Create test settings
	settings := &conf.Settings{}
	settings.Main.Name = "TestNode"
	settings.BirdNET = conf.BirdNETConfig{
		Latitude:    52.5200,
		Longitude:   13.4050,
		Threshold:   0.5,
		Sensitivity: 1.0,
	}

	// Create a test observation with occurrence value
	beginTime := time.Now()
	endTime := beginTime.Add(3 * time.Second)
	species := "Turdus merula_blackbird"  // Format: "Scientific Name_Common Name"
	confidence := 0.85
	source := "test_audio"
	clipName := "test_clip.wav"
	elapsedTime := 100 * time.Millisecond
	occurrence := 0.75 // Test occurrence value

	// Create the note
	note := observation.New(settings, beginTime, endTime, species, confidence, source, clipName, elapsedTime, occurrence)

	// Verify all fields are set correctly
	require.NotNil(t, note)
	assert.Equal(t, "TestNode", note.SourceNode)
	assert.Equal(t, "Turdus merula", note.ScientificName)
	assert.Equal(t, "blackbird", note.CommonName)
	assert.InEpsilon(t, 0.85, note.Confidence, 0.001)
	assert.InEpsilon(t, 0.75, note.Occurrence, 0.001, "Occurrence should be set to the provided value")
	assert.InEpsilon(t, 52.5200, note.Latitude, 0.001)
	assert.InEpsilon(t, 13.4050, note.Longitude, 0.001)
	assert.InEpsilon(t, 0.5, note.Threshold, 0.001)
	assert.InEpsilon(t, 1.0, note.Sensitivity, 0.001)
	assert.Equal(t, "test_clip.wav", note.ClipName)
	assert.Equal(t, elapsedTime, note.ProcessingTime)
}

func TestProcessChunkWithOccurrence(t *testing.T) {
	t.Skip("Requires full BirdNET initialization with models")
	
	// This test would require a fully initialized BirdNET instance
	// with models loaded, which is beyond the scope of this unit test
	// In a real integration test, you would:
	// 1. Initialize BirdNET with test models
	// 2. Process a chunk of audio
	// 3. Verify that the returned notes have occurrence values set
}