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

// safeMapAccessMap is a helper for safe map access when expecting a nested map
func safeMapAccessMap(t *testing.T, m map[string]any, key, description string) map[string]any {
	t.Helper()
	valueInterface, hasKey := m[key]
	require.True(t, hasKey, "%s should contain '%s' field", description, key)
	value, ok := valueInterface.(map[string]any)
	require.True(t, ok, "'%s' field should be a map[string]any (object)", key)
	return value
}

// TestSpeciesConfigIntegration tests the full integration of species config with zero values
func TestSpeciesConfigIntegration(t *testing.T) {
	t.Parallel()

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

		// Check that threshold exists and validate expected values
		thresholdValue, hasThreshold := config["threshold"]
		assert.True(t, hasThreshold, "%s should have threshold field", birdName)
		// YAML unmarshaling can give us float64 for zero values
		var threshold float64
		switch v := thresholdValue.(type) {
		case float64:
			threshold = v
		case int:
			threshold = float64(v)
		default:
			t.Errorf("%s threshold should be numeric, got %T: %v", birdName, thresholdValue, thresholdValue)
			continue
		}

		// Check that interval exists and validate expected values
		intervalValue, hasInterval := config["interval"]
		assert.True(t, hasInterval, "%s should have interval field even when zero", birdName)
		// YAML unmarshaling can give us int for zero values
		var interval int
		switch v := intervalValue.(type) {
		case int:
			interval = v
		case float64:
			interval = int(v)
		default:
			t.Errorf("%s interval should be numeric, got %T: %v", birdName, intervalValue, intervalValue)
			continue
		}

		// Validate concrete expected values based on bird name
		switch birdName {
		case "Rare Bird":
			assert.InDelta(t, 0.0, threshold, 0.0001, "Rare Bird should have 0.0 threshold")
			assert.Equal(t, 0, interval, "Rare Bird should have 0 interval")
		case "Common Bird":
			assert.InDelta(t, 0.0, threshold, 0.0001, "Common Bird should have updated 0.0 threshold")
			assert.Equal(t, 0, interval, "Common Bird should have updated 0 interval")
		case "Zero Bird":
			assert.InDelta(t, 0.0, threshold, 0.0001, "Zero Bird should have 0.0 threshold")
			assert.Equal(t, 0, interval, "Zero Bird should have 0 interval")
		}

		// Check that actions field exists and has correct type/shape
		actionsValue, hasActions := config["actions"]
		assert.True(t, hasActions, "%s should have actions field", birdName)
		actions, ok := actionsValue.([]any)
		require.True(t, ok, "%s actions should be a slice/list", birdName)
		assert.Empty(t, actions, "%s actions should be empty", birdName)
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
	require.NotNil(t, rareBird.Actions, "Rare Bird actions should not be nil")
	assert.Empty(t, rareBird.Actions, "Rare Bird actions should be empty")

	// Check Common Bird (updated to zeros)
	commonBird, hasCommonBirdReloaded := reloadedSettings.Realtime.Species.Config["Common Bird"]
	require.True(t, hasCommonBirdReloaded, "Common Bird should exist in reloaded config")
	assert.InDelta(t, 0.0, commonBird.Threshold, 0.0001, "Common Bird threshold should now be 0.0")
	assert.Equal(t, 0, commonBird.Interval, "Common Bird interval should now be 0")
	require.NotNil(t, commonBird.Actions, "Common Bird actions should not be nil")
	assert.Empty(t, commonBird.Actions, "Common Bird actions should be empty")

	// Check Zero Bird (new)
	zeroBird, hasZeroBird := reloadedSettings.Realtime.Species.Config["Zero Bird"]
	require.True(t, hasZeroBird, "Zero Bird should exist in reloaded config")
	assert.InDelta(t, 0.0, zeroBird.Threshold, 0.0001, "Zero Bird threshold should be 0.0")
	assert.Equal(t, 0, zeroBird.Interval, "Zero Bird interval should be 0")
	require.NotNil(t, zeroBird.Actions, "Zero Bird actions should not be nil")
	assert.Empty(t, zeroBird.Actions, "Zero Bird actions should be empty")
}
