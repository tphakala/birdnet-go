package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSpeciesConfigIntegration tests the full integration of species config with zero values
func TestSpeciesConfigIntegration(t *testing.T) {
	// Create a temporary directory for the test
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_integration_config.yaml")

	// Create initial config with species settings containing zero values
	initialConfig := `
main:
  name: TestNode
birdnet:
  threshold: 0.3
  locale: en
realtime:
  interval: 15
  species:
    include:
      - Robin
      - Blue Jay
    exclude:
      - Crow
    config:
      "Rare Bird":
        threshold: 0.0
        interval: 0
        actions: []
      "Common Bird":
        threshold: 0.95
        interval: 60
        actions: []
`

	// Write initial config
	err := os.WriteFile(configPath, []byte(initialConfig), 0o644)
	require.NoError(t, err, "Failed to write initial config")

	// Read and parse the config
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err, "Failed to read config")

	var settings Settings
	err = yaml.Unmarshal(configData, &settings)
	require.NoError(t, err, "Failed to unmarshal config")

	// Verify initial values loaded correctly
	assert.Len(t, settings.Realtime.Species.Config, 2, "Should have 2 species configs")
	
	rareBird := settings.Realtime.Species.Config["Rare Bird"]
	assert.InDelta(t, 0.0, rareBird.Threshold, 0.0001, "Rare Bird threshold should be 0.0")
	assert.Equal(t, 0, rareBird.Interval, "Rare Bird interval should be 0")
	
	commonBird := settings.Realtime.Species.Config["Common Bird"]
	assert.InDelta(t, 0.95, commonBird.Threshold, 0.0001, "Common Bird threshold should be 0.95")
	assert.Equal(t, 60, commonBird.Interval, "Common Bird interval should be 60")

	// Modify the settings - update Common Bird to zero values
	settings.Realtime.Species.Config["Common Bird"] = SpeciesConfig{
		Threshold: 0.0,
		Interval:  0,
		Actions:   []SpeciesAction{},
	}

	// Add a new bird with zero values
	settings.Realtime.Species.Config["Zero Bird"] = SpeciesConfig{
		Threshold: 0.0,
		Interval:  0,
		Actions:   []SpeciesAction{},
	}

	// Save the modified settings
	err = SaveYAMLConfig(configPath, &settings)
	require.NoError(t, err, "Failed to save modified config")

	// Read the saved config back
	savedData, err := os.ReadFile(configPath)
	require.NoError(t, err, "Failed to read saved config")

	// Parse the saved YAML to verify structure
	var yamlMap map[string]interface{}
	err = yaml.Unmarshal(savedData, &yamlMap)
	require.NoError(t, err, "Failed to parse saved YAML")

	// Navigate to species configs
	realtime := yamlMap["realtime"].(map[string]interface{})
	species := realtime["species"].(map[string]interface{})
	configs := species["config"].(map[string]interface{})

	// Check that all three birds exist
	assert.Contains(t, configs, "Rare Bird", "Rare Bird should exist")
	assert.Contains(t, configs, "Common Bird", "Common Bird should exist")
	assert.Contains(t, configs, "Zero Bird", "Zero Bird should exist")

	// Verify zero values are present in the YAML
	for birdName, configInterface := range configs {
		config := configInterface.(map[string]interface{})
		
		// Check that threshold exists
		_, hasThreshold := config["threshold"]
		assert.True(t, hasThreshold, "%s should have threshold field", birdName)
		
		// Check that interval exists (THIS IS THE KEY TEST)
		_, hasInterval := config["interval"]
		assert.True(t, hasInterval, "%s should have interval field even when zero", birdName)
	}

	// Load the config again to verify round-trip
	var reloadedSettings Settings
	err = yaml.Unmarshal(savedData, &reloadedSettings)
	require.NoError(t, err, "Failed to reload settings")

	// Verify all birds have correct values after reload
	assert.Len(t, reloadedSettings.Realtime.Species.Config, 3, "Should have 3 species configs after reload")
	
	// Check Rare Bird (unchanged)
	rareBird = reloadedSettings.Realtime.Species.Config["Rare Bird"]
	assert.InDelta(t, 0.0, rareBird.Threshold, 0.0001, "Rare Bird threshold should still be 0.0")
	assert.Equal(t, 0, rareBird.Interval, "Rare Bird interval should still be 0")
	
	// Check Common Bird (updated to zeros)
	commonBird = reloadedSettings.Realtime.Species.Config["Common Bird"]
	assert.InDelta(t, 0.0, commonBird.Threshold, 0.0001, "Common Bird threshold should now be 0.0")
	assert.Equal(t, 0, commonBird.Interval, "Common Bird interval should now be 0")
	
	// Check Zero Bird (new)
	zeroBird := reloadedSettings.Realtime.Species.Config["Zero Bird"]
	assert.InDelta(t, 0.0, zeroBird.Threshold, 0.0001, "Zero Bird threshold should be 0.0")
	assert.Equal(t, 0, zeroBird.Interval, "Zero Bird interval should be 0")
}