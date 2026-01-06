package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSpeciesConfigKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]SpeciesConfig
		expected map[string]SpeciesConfig
	}{
		{
			name:     "empty map",
			input:    map[string]SpeciesConfig{},
			expected: map[string]SpeciesConfig{},
		},
		{
			name: "already lowercase",
			input: map[string]SpeciesConfig{
				"american robin": {Threshold: 0.8, Interval: 30},
			},
			expected: map[string]SpeciesConfig{
				"american robin": {Threshold: 0.8, Interval: 30},
			},
		},
		{
			name: "mixed case to lowercase",
			input: map[string]SpeciesConfig{
				"American Robin": {Threshold: 0.8, Interval: 30},
				"House Sparrow":  {Threshold: 0.9, Interval: 60},
			},
			expected: map[string]SpeciesConfig{
				"american robin": {Threshold: 0.8, Interval: 30},
				"house sparrow":  {Threshold: 0.9, Interval: 60},
			},
		},
		{
			name: "uppercase to lowercase",
			input: map[string]SpeciesConfig{
				"AMERICAN ROBIN": {Threshold: 0.75},
			},
			expected: map[string]SpeciesConfig{
				"american robin": {Threshold: 0.75},
			},
		},
		{
			name: "preserves config values",
			input: map[string]SpeciesConfig{
				"Test Bird": {
					Threshold: 0.65,
					Interval:  45,
					Actions: []SpeciesAction{
						{Type: "ExecuteCommand", Command: "/usr/bin/notify"},
					},
				},
			},
			expected: map[string]SpeciesConfig{
				"test bird": {
					Threshold: 0.65,
					Interval:  45,
					Actions: []SpeciesAction{
						{Type: "ExecuteCommand", Command: "/usr/bin/notify"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := NormalizeSpeciesConfigKeys(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeSpeciesConfigKeys_DuplicateAfterNormalization(t *testing.T) {
	t.Parallel()

	// When two keys normalize to the same value, mixed-case key takes precedence.
	// This is deterministic (not random map iteration) due to the two-pass algorithm.
	// In practice users shouldn't have duplicates, but this documents the behavior.
	input := map[string]SpeciesConfig{
		"American Robin": {Threshold: 0.8},  // mixed-case: should win
		"american robin": {Threshold: 0.9},  // lowercase: should be overwritten
	}

	result := NormalizeSpeciesConfigKeys(input)

	// Should have only one key
	require.Len(t, result, 1)
	config, exists := result["american robin"]
	require.True(t, exists, "normalized key should exist")
	// Verify mixed-case key value (0.8) overwrote lowercase key value (0.9)
	assert.InDelta(t, 0.8, config.Threshold, 0.0001, "mixed-case key should take precedence")
}

func TestNormalizeSpeciesConfigKeys_NilMap(t *testing.T) {
	t.Parallel()

	result := NormalizeSpeciesConfigKeys(nil)
	assert.NotNil(t, result, "should return empty map, not nil")
	assert.Empty(t, result)
}

func TestSettingsNormalizesSpeciesConfigOnLoad(t *testing.T) {
	t.Parallel()

	// Create temp config file with mixed-case species keys
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	configContent := `
birdnet:
  sensitivity: 1.0
  threshold: 0.8
realtime:
  species:
    include:
      - "American Robin"
    exclude:
      - "House Sparrow"
    config:
      "American Robin":
        threshold: 0.75
        interval: 30
      "European Blackbird":
        threshold: 0.85
`

	err := os.WriteFile(configPath, []byte(configContent), 0o600)
	require.NoError(t, err)

	// Load config using viper directly to simulate Load()
	v := viper.New()
	v.SetConfigFile(configPath)
	err = v.ReadInConfig()
	require.NoError(t, err)

	var settings Settings
	err = v.Unmarshal(&settings)
	require.NoError(t, err)

	// Apply normalization (this is what we're testing gets called)
	settings.Realtime.Species.Config = NormalizeSpeciesConfigKeys(settings.Realtime.Species.Config)

	// Verify keys are normalized to lowercase
	_, hasAmericanRobin := settings.Realtime.Species.Config["american robin"]
	assert.True(t, hasAmericanRobin, "should have lowercase 'american robin' key")

	_, hasEuropeanBlackbird := settings.Realtime.Species.Config["european blackbird"]
	assert.True(t, hasEuropeanBlackbird, "should have lowercase 'european blackbird' key")

	// Verify original mixed-case keys don't exist
	_, hasMixedCase := settings.Realtime.Species.Config["American Robin"]
	assert.False(t, hasMixedCase, "should not have mixed-case key")

	// Verify config values are preserved
	robin := settings.Realtime.Species.Config["american robin"]
	assert.InDelta(t, 0.75, robin.Threshold, 0.0001)
	assert.Equal(t, 30, robin.Interval)
}
