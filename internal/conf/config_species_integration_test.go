package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Test helper functions for YAML/JSON unmarshaling

// unmarshalYAML is a generic helper for YAML unmarshaling with error handling
func unmarshalYAML[T any](t *testing.T, data []byte, target *T, failureMessage string) {
	t.Helper()
	err := yaml.Unmarshal(data, target)
	require.NoError(t, err, failureMessage)
}

// safeMapAccess is a helper for safe map key access returning any type
func safeMapAccess(t *testing.T, m map[string]any, key, description string) any {
	t.Helper()
	valueInterface, hasKey := m[key]
	require.True(t, hasKey, "%s should contain %s field", description, key)
	return valueInterface
}

// safeMapAccessMap is a helper for safe map access when expecting a nested map
func safeMapAccessMap(t *testing.T, m map[string]any, key, description string) map[string]any {
	t.Helper()
	valueInterface, hasKey := m[key]
	require.True(t, hasKey, "%s should contain %s field", description, key)
	value, ok := valueInterface.(map[string]any)
	require.True(t, ok, "%s field should be a map", key)
	return value
}

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
	err := os.WriteFile(configPath, []byte(initialConfig), 0o600)
	require.NoError(t, err, "Failed to write initial config")

	// Read and parse the config
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err, "Failed to read config")

	var settings Settings
	unmarshalYAML(t, configData, &settings, "Failed to unmarshal config")

	// Verify initial values loaded correctly
	assert.Len(t, settings.Realtime.Species.Config, 2, "Should have 2 species configs")
	
	rareBird, hasRareBird := settings.Realtime.Species.Config["Rare Bird"]
	require.True(t, hasRareBird, "Rare Bird should exist in config")
	assert.InDelta(t, 0.0, rareBird.Threshold, 0.0001, "Rare Bird threshold should be 0.0")
	assert.Equal(t, 0, rareBird.Interval, "Rare Bird interval should be 0")
	
	commonBird, hasCommonBird := settings.Realtime.Species.Config["Common Bird"]
	require.True(t, hasCommonBird, "Common Bird should exist in config")
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
	var yamlMap map[string]any
	unmarshalYAML(t, savedData, &yamlMap, "Failed to parse saved YAML")

	// Navigate to species configs with safe type assertions
	realtime := safeMapAccessMap(t, yamlMap, "realtime", "YAML")
	species := safeMapAccessMap(t, realtime, "species", "realtime")
	configs := safeMapAccessMap(t, species, "config", "species")

	// Check that all three birds exist
	assert.Contains(t, configs, "Rare Bird", "Rare Bird should exist")
	assert.Contains(t, configs, "Common Bird", "Common Bird should exist")
	assert.Contains(t, configs, "Zero Bird", "Zero Bird should exist")

	// Verify zero values are present in the YAML
	for birdName, configInterface := range configs {
		config, ok := configInterface.(map[string]any)
		require.True(t, ok, "Bird config %s should be a map", birdName)
		
		// Check that threshold exists
		_, hasThreshold := config["threshold"]
		assert.True(t, hasThreshold, "%s should have threshold field", birdName)
		
		// Check that interval exists (THIS IS THE KEY TEST)
		_, hasInterval := config["interval"]
		assert.True(t, hasInterval, "%s should have interval field even when zero", birdName)
	}

	// Load the config again to verify round-trip
	var reloadedSettings Settings
	unmarshalYAML(t, savedData, &reloadedSettings, "Failed to reload settings")

	// Verify all birds have correct values after reload
	assert.Len(t, reloadedSettings.Realtime.Species.Config, 3, "Should have 3 species configs after reload")
	
	// Check Rare Bird (unchanged)
	rareBird, hasRareBirdReloaded := reloadedSettings.Realtime.Species.Config["Rare Bird"]
	require.True(t, hasRareBirdReloaded, "Rare Bird should exist in reloaded config")
	assert.InDelta(t, 0.0, rareBird.Threshold, 0.0001, "Rare Bird threshold should still be 0.0")
	assert.Equal(t, 0, rareBird.Interval, "Rare Bird interval should still be 0")
	
	// Check Common Bird (updated to zeros)
	commonBird, hasCommonBirdReloaded := reloadedSettings.Realtime.Species.Config["Common Bird"]
	require.True(t, hasCommonBirdReloaded, "Common Bird should exist in reloaded config")
	assert.InDelta(t, 0.0, commonBird.Threshold, 0.0001, "Common Bird threshold should now be 0.0")
	assert.Equal(t, 0, commonBird.Interval, "Common Bird interval should now be 0")
	
	// Check Zero Bird (new)
	zeroBird, hasZeroBird := reloadedSettings.Realtime.Species.Config["Zero Bird"]
	require.True(t, hasZeroBird, "Zero Bird should exist in reloaded config")
	assert.InDelta(t, 0.0, zeroBird.Threshold, 0.0001, "Zero Bird threshold should be 0.0")
	assert.Equal(t, 0, zeroBird.Interval, "Zero Bird interval should be 0")
}