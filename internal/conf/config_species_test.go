package conf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSpeciesConfigYAMLPersistence tests that species config with zero values persists correctly
func TestSpeciesConfigYAMLPersistence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		config    SpeciesConfig
		expectNil bool // whether we expect fields to be missing in YAML
	}{
		{
			name: "zero_threshold_and_interval",
			config: SpeciesConfig{
				Threshold: 0.0,
				Interval:  0,
				Actions:   []SpeciesAction{},
			},
			expectNil: false, // We want these to persist even when zero
		},
		{
			name: "non_zero_values",
			config: SpeciesConfig{
				Threshold: 0.75,
				Interval:  30,
				Actions: []SpeciesAction{
					{
						Type:            "ExecuteCommand",
						Command:         "/usr/bin/notify",
						Parameters:      []string{"CommonName"},
						ExecuteDefaults: true,
					},
				},
			},
			expectNil: false,
		},
		{
			name: "only_threshold_set",
			config: SpeciesConfig{
				Threshold: 0.5,
				Interval:  0, // This should still persist
				Actions:   []SpeciesAction{},
			},
			expectNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Marshal to YAML
			yamlData, err := yaml.Marshal(tt.config)
			require.NoError(t, err, "Failed to marshal config to YAML")

			// Unmarshal back to map to check presence of fields
			var yamlMap map[string]any
			err = yaml.Unmarshal(yamlData, &yamlMap)
			require.NoError(t, err, "Failed to unmarshal YAML to map")

			// Check if threshold is present
			_, hasThreshold := yamlMap["threshold"]
			assert.True(t, hasThreshold, "threshold field should always be present in YAML")

			// Check if interval is present - regression guard: ensure interval persists even when zero
			_, hasInterval := yamlMap["interval"]
			if !tt.expectNil {
				assert.True(t, hasInterval, "interval field should be present in YAML even when zero")
			}

			// Unmarshal back to struct
			var unmarshaledConfig SpeciesConfig
			err = yaml.Unmarshal(yamlData, &unmarshaledConfig)
			require.NoError(t, err, "Failed to unmarshal YAML back to struct")

			// Verify values are preserved
			assert.InDelta(t, tt.config.Threshold, unmarshaledConfig.Threshold, 0.0001, "Threshold should be preserved")
			assert.Equal(t, tt.config.Interval, unmarshaledConfig.Interval, "Interval should be preserved")
			assert.Equal(t, tt.config.Actions, unmarshaledConfig.Actions, "Actions should be preserved")
		})
	}
}

// TestSpeciesConfigJSONPersistence tests JSON marshaling for API communication
func TestSpeciesConfigJSONPersistence(t *testing.T) {
	t.Parallel()
	speciesSettings := SpeciesSettings{
		Include: []string{"Robin", "Blue Jay"},
		Exclude: []string{"Crow"},
		Config: map[string]SpeciesConfig{
			"Rare Bird": {
				Threshold: 0.0, // Zero threshold should persist
				Interval:  0,   // Zero interval should persist
				Actions:   []SpeciesAction{},
			},
			"Common Bird": {
				Threshold: 0.95,
				Interval:  60,
				Actions:   []SpeciesAction{},
			},
		},
	}

	// Marshal to JSON (simulating API response)
	jsonData, err := json.Marshal(speciesSettings)
	require.NoError(t, err, "Failed to marshal to JSON")

	// Check that zero values are present in JSON
	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err, "Failed to unmarshal JSON to map")

	// Navigate to the Rare Bird config
	configInterface, ok := jsonMap["config"]
	require.True(t, ok, "JSON should contain config field")
	configMap, ok := configInterface.(map[string]any)
	require.True(t, ok, "config field should be a map")
	
	rareBirdInterface, ok := configMap["Rare Bird"]
	require.True(t, ok, "config should contain Rare Bird entry")
	rareBird, ok := rareBirdInterface.(map[string]any)
	require.True(t, ok, "Rare Bird entry should be a map")

	// Regression guard: ensure interval field persists in JSON even when zero
	_, hasInterval := rareBird["interval"]
	assert.True(t, hasInterval, "interval field should be present in JSON even when zero")

	// Verify threshold is present
	_, hasThreshold := rareBird["threshold"]
	assert.True(t, hasThreshold, "threshold field should be present in JSON")

	// Unmarshal back and verify values
	var unmarshaledSettings SpeciesSettings
	err = json.Unmarshal(jsonData, &unmarshaledSettings)
	require.NoError(t, err, "Failed to unmarshal JSON")

	// Check Rare Bird config
	rareBirdConfig := unmarshaledSettings.Config["Rare Bird"]
	assert.InDelta(t, 0.0, rareBirdConfig.Threshold, 0.0001, "Zero threshold should be preserved")
	assert.Equal(t, 0, rareBirdConfig.Interval, "Zero interval should be preserved")
}

// TestSettingsSaveAndLoad tests the full save/load cycle with species configs
func TestSettingsSaveAndLoad(t *testing.T) {
	t.Parallel()
	
	// Create temp directory for test
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.yaml")

	// Create settings with species config containing zero values
	settings := &Settings{
		Main: struct {
			Name      string    `json:"name"`
			TimeAs24h bool      `json:"timeAs24h"`
			Log       LogConfig `json:"log"`
		}{
			Name: "TestNode",
		},
		BirdNET: BirdNETConfig{
			Threshold: 0.3,
			Locale:    "en",
		},
		Realtime: RealtimeSettings{
			Species: SpeciesSettings{
				Include: []string{"Test Bird"},
				Config: map[string]SpeciesConfig{
					"Zero Values Bird": {
						Threshold: 0.0,
						Interval:  0,
						Actions:   []SpeciesAction{},
					},
					"Normal Bird": {
						Threshold: 0.8,
						Interval:  45,
						Actions:   []SpeciesAction{},
					},
				},
			},
		},
	}

	// Save settings
	err := SaveYAMLConfig(configPath, settings)
	require.NoError(t, err, "Failed to save settings")

	// Read the raw YAML file to check field presence
	yamlContent, err := os.ReadFile(configPath)
	require.NoError(t, err, "Failed to read config file")

	// Parse YAML to check structure
	var yamlData map[string]any
	err = yaml.Unmarshal(yamlContent, &yamlData)
	require.NoError(t, err, "Failed to parse YAML")

	// Navigate to species config with safe type assertions
	realtimeRaw, ok := yamlData["realtime"]
	require.True(t, ok, "realtime section should exist in YAML data")
	realtime, ok := realtimeRaw.(map[string]any)
	require.True(t, ok, "realtime should be a map")

	speciesRaw, ok := realtime["species"]
	require.True(t, ok, "species section should exist in realtime")
	species, ok := speciesRaw.(map[string]any)
	require.True(t, ok, "species should be a map")

	configMapRaw, ok := species["config"]
	require.True(t, ok, "config section should exist in species")
	configMap, ok := configMapRaw.(map[string]any)
	require.True(t, ok, "config should be a map")
	
	// Check Zero Values Bird with safe type assertion
	zeroValuesBirdRaw, ok := configMap["Zero Values Bird"]
	require.True(t, ok, "Zero Values Bird should exist in config")
	zeroValuesBird, ok := zeroValuesBirdRaw.(map[string]any)
	require.True(t, ok, "Zero Values Bird should be a map")
	
	// Regression guard: ensure interval field persists even when zero
	_, hasInterval := zeroValuesBird["interval"]
	assert.True(t, hasInterval, "interval field should be saved even when zero")
	
	_, hasThreshold := zeroValuesBird["threshold"]
	assert.True(t, hasThreshold, "threshold field should be saved")

	// Load settings back
	loadedContent, err := os.ReadFile(configPath)
	require.NoError(t, err, "Failed to read config for loading")

	var loadedSettings Settings
	err = yaml.Unmarshal(loadedContent, &loadedSettings)
	require.NoError(t, err, "Failed to unmarshal loaded settings")

	// Verify Zero Values Bird config is preserved
	zeroConfig := loadedSettings.Realtime.Species.Config["Zero Values Bird"]
	assert.InDelta(t, 0.0, zeroConfig.Threshold, 0.0001, "Zero threshold should be loaded correctly")
	assert.Equal(t, 0, zeroConfig.Interval, "Zero interval should be loaded correctly")

	// Verify Normal Bird config
	normalConfig := loadedSettings.Realtime.Species.Config["Normal Bird"]
	assert.InDelta(t, 0.8, normalConfig.Threshold, 0.0001, "Normal threshold should be loaded correctly")
	assert.Equal(t, 45, normalConfig.Interval, "Normal interval should be loaded correctly")
}

// TestSpeciesConfigUpdate tests updating existing species config
func TestSpeciesConfigUpdate(t *testing.T) {
	t.Parallel()
	
	// Initial settings with one species config
	settings := &Settings{
		Realtime: RealtimeSettings{
			Species: SpeciesSettings{
				Config: map[string]SpeciesConfig{
					"Existing Bird": {
						Threshold: 0.7,
						Interval:  30,
						Actions:   []SpeciesAction{},
					},
				},
			},
		},
	}

	// Simulate updating with zero values
	settings.Realtime.Species.Config["Existing Bird"] = SpeciesConfig{
		Threshold: 0.0, // Update to zero
		Interval:  0,   // Update to zero
		Actions:   []SpeciesAction{},
	}

	// Marshal and unmarshal to simulate save/load
	yamlData, err := yaml.Marshal(settings)
	require.NoError(t, err)

	var reloaded Settings
	err = yaml.Unmarshal(yamlData, &reloaded)
	require.NoError(t, err)

	// Verify zero values are preserved
	config := reloaded.Realtime.Species.Config["Existing Bird"]
	assert.InDelta(t, 0.0, config.Threshold, 0.0001, "Zero threshold should be preserved after update")
	assert.Equal(t, 0, config.Interval, "Zero interval should be preserved after update")
}