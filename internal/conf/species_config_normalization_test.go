package conf

import (
	"testing"

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

	// When two keys normalize to the same value, last one wins (Go map iteration order)
	// This test documents the behavior - in practice users shouldn't have duplicates
	input := map[string]SpeciesConfig{
		"American Robin": {Threshold: 0.8},
		"american robin": {Threshold: 0.9},
	}

	result := NormalizeSpeciesConfigKeys(input)

	// Should have only one key
	require.Len(t, result, 1)
	_, exists := result["american robin"]
	assert.True(t, exists, "normalized key should exist")
}

func TestNormalizeSpeciesConfigKeys_NilMap(t *testing.T) {
	t.Parallel()

	result := NormalizeSpeciesConfigKeys(nil)
	assert.NotNil(t, result, "should return empty map, not nil")
	assert.Empty(t, result)
}
