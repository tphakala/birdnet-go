package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestMergeJSONIntoStruct tests the mergeJSONIntoStruct function
func TestMergeJSONIntoStruct(t *testing.T) {
	tests := []struct {
		name     string
		initial  conf.BirdNETConfig
		update   string
		validate func(t *testing.T, result conf.BirdNETConfig)
	}{
		{
			name: "Update coordinates preserves range filter",
			initial: conf.BirdNETConfig{
				Latitude:  40.7128,
				Longitude: -74.0060,
				RangeFilter: conf.RangeFilterSettings{
					Model:     "latest",
					Threshold: 0.03,
				},
			},
			update: `{
				"latitude": 51.5074,
				"longitude": -0.1278
			}`,
			validate: func(t *testing.T, result conf.BirdNETConfig) {
				t.Helper()
				assert.InDelta(t, 51.5074, result.Latitude, 0.0001)
				assert.InDelta(t, -0.1278, result.Longitude, 0.0001)
				assert.Equal(t, "latest", result.RangeFilter.Model)
				assert.InDelta(t, float32(0.03), result.RangeFilter.Threshold, 0.0001)
			},
		},
		{
			name: "Update range filter threshold preserves other fields",
			initial: conf.BirdNETConfig{
				Latitude:  40.7128,
				Longitude: -74.0060,
				RangeFilter: conf.RangeFilterSettings{
					Model:     "latest",
					Threshold: 0.03,
				},
			},
			update: `{
				"rangeFilter": {
					"threshold": 0.05
				}
			}`,
			validate: func(t *testing.T, result conf.BirdNETConfig) {
				t.Helper()
				assert.InDelta(t, 40.7128, result.Latitude, 0.0001)
				assert.InDelta(t, -74.0060, result.Longitude, 0.0001)
				assert.Equal(t, "latest", result.RangeFilter.Model)
				assert.InDelta(t, float32(0.05), result.RangeFilter.Threshold, 0.0001)
			},
		},
		{
			name: "Complex update with nested partial objects",
			initial: conf.BirdNETConfig{
				Latitude:    40.7128,
				Longitude:   -74.0060,
				Sensitivity: 1.0,
				Threshold:   0.8,
				RangeFilter: conf.RangeFilterSettings{
					Model:     "legacy",
					Threshold: 0.02,
				},
			},
			update: `{
				"latitude": 48.8566,
				"longitude": 2.3522,
				"sensitivity": 1.2,
				"rangeFilter": {
					"model": "latest"
				}
			}`,
			validate: func(t *testing.T, result conf.BirdNETConfig) {
				t.Helper()
				assert.InDelta(t, 48.8566, result.Latitude, 0.0001)
				assert.InDelta(t, 2.3522, result.Longitude, 0.0001)
				assert.InDelta(t, 1.2, result.Sensitivity, 0.0001)
				assert.InDelta(t, 0.8, result.Threshold, 0.0001) // Should be preserved
				assert.Equal(t, "latest", result.RangeFilter.Model)
				assert.InDelta(t, float32(0.02), result.RangeFilter.Threshold, 0.0001) // Should be preserved
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy of initial settings
			result := tt.initial

			// Apply the merge
			err := mergeJSONIntoStruct(json.RawMessage(tt.update), &result)
			require.NoError(t, err)

			// Validate the result
			tt.validate(t, result)
		})
	}
}

// TestDeepMergeMaps tests the deep merge functionality
func TestDeepMergeMaps(t *testing.T) {
	tests := []struct {
		name     string
		dst      map[string]interface{}
		src      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "Simple merge",
			dst: map[string]interface{}{
				"a": 1,
				"b": 2,
			},
			src: map[string]interface{}{
				"b": 3,
				"c": 4,
			},
			expected: map[string]interface{}{
				"a": 1,
				"b": 3,
				"c": 4,
			},
		},
		{
			name: "Nested merge",
			dst: map[string]interface{}{
				"top": map[string]interface{}{
					"a": 1,
					"b": 2,
				},
			},
			src: map[string]interface{}{
				"top": map[string]interface{}{
					"b": 3,
					"c": 4,
				},
			},
			expected: map[string]interface{}{
				"top": map[string]interface{}{
					"a": 1,
					"b": 3,
					"c": 4,
				},
			},
		},
		{
			name: "Preserve unmodified nested objects",
			dst: map[string]interface{}{
				"settings": map[string]interface{}{
					"rangeFilter": map[string]interface{}{
						"model":     "latest",
						"threshold": 0.03,
					},
					"latitude": 40.0,
				},
			},
			src: map[string]interface{}{
				"settings": map[string]interface{}{
					"latitude": 50.0,
				},
			},
			expected: map[string]interface{}{
				"settings": map[string]interface{}{
					"rangeFilter": map[string]interface{}{
						"model":     "latest",
						"threshold": 0.03,
					},
					"latitude": 50.0,
				},
			},
		},
		{
			name: "Handle nil values",
			dst: map[string]interface{}{
				"a": 1,
				"b": 2,
			},
			src: map[string]interface{}{
				"b": nil,
				"c": 3,
			},
			expected: map[string]interface{}{
				"a": 1,
				"b": nil,
				"c": 3,
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deepMergeMaps(tt.dst, tt.src)
			assert.Equal(t, tt.expected, result)
		})
	}
}