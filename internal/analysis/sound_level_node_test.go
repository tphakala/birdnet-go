package analysis

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// TestCompactSoundLevelData_IncludesNodeName tests that the node name is included in MQTT messages
func TestCompactSoundLevelData_IncludesNodeName(t *testing.T) {
	// Test data
	nodeName := "BirdNET-Go-TestNode"
	soundData := myaudio.SoundLevelData{
		Timestamp: time.Now(),
		Source:    "test-source",
		Name:      "Test Audio Source",
		Duration:  10,
		OctaveBands: map[string]myaudio.OctaveBandData{
			"125_Hz": {
				CenterFreq:  125,
				Min:         -60.5,
				Max:         -40.2,
				Mean:        -50.3,
				SampleCount: 100,
			},
			"250_Hz": {
				CenterFreq:  250,
				Min:         -55.0,
				Max:         -35.0,
				Mean:        -45.0,
				SampleCount: 100,
			},
		},
	}

	// Convert to compact format
	compactData := toCompactFormat(soundData, nodeName)

	// Verify node name is set
	assert.Equal(t, nodeName, compactData.Node, "Node name should be included in compact data")
	assert.Equal(t, soundData.Source, compactData.Src, "Source should be preserved")
	assert.Equal(t, soundData.Name, compactData.Name, "Name should be preserved")
	assert.Equal(t, soundData.Duration, compactData.Dur, "Duration should be preserved")

	// Marshal to JSON to verify it's included in the output
	jsonData, err := json.Marshal(compactData)
	require.NoError(t, err, "Should marshal to JSON without error")

	// Unmarshal to verify
	var unmarshaled CompactSoundLevelData
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err, "Should unmarshal from JSON without error")

	assert.Equal(t, nodeName, unmarshaled.Node, "Node name should survive JSON round-trip")
	assert.Equal(t, compactData.Src, unmarshaled.Src, "Source should survive JSON round-trip")
	assert.Equal(t, compactData.Name, unmarshaled.Name, "Name should survive JSON round-trip")

	// Verify JSON contains the node field
	jsonStr := string(jsonData)
	assert.Contains(t, jsonStr, `"node":"BirdNET-Go-TestNode"`, "JSON should contain node field with correct value")
}

// TestCompactSoundLevelData_EmptyNodeName tests handling of empty node name
func TestCompactSoundLevelData_EmptyNodeName(t *testing.T) {
	soundData := myaudio.SoundLevelData{
		Timestamp: time.Now(),
		Source:    "test-source",
		Name:      "Test Audio Source",
		Duration:  10,
		OctaveBands: map[string]myaudio.OctaveBandData{
			"500_Hz": {
				CenterFreq:  500,
				Min:         -50.0,
				Max:         -30.0,
				Mean:        -40.0,
				SampleCount: 100,
			},
		},
	}

	// Convert with empty node name
	compactData := toCompactFormat(soundData, "")

	// Empty node name should be preserved (not replaced with a default)
	assert.Empty(t, compactData.Node, "Empty node name should be preserved")

	// Marshal to JSON
	jsonData, err := json.Marshal(compactData)
	require.NoError(t, err, "Should marshal to JSON without error")

	// Verify JSON contains empty node field
	jsonStr := string(jsonData)
	assert.Contains(t, jsonStr, `"node":""`, "JSON should contain node field even when empty")
}
