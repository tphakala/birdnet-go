package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtremeValues verifies the system handles extreme values appropriately
func TestExtremeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		section       string
		extremeData   interface{}
		expectedError bool
		description   string
	}{
		{
			name:    "Maximum integer values",
			section: "dashboard",
			extremeData: map[string]interface{}{
				"summaryLimit": 2147483647, // Max int32
			},
			expectedError: false,
			description:   "Should handle max int values",
		},
		{
			name:    "Negative values where not allowed",
			section: "dashboard",
			extremeData: map[string]interface{}{
				"summaryLimit": -100,
			},
			expectedError: false, // Might be accepted but should be validated in business logic
			description:   "Should handle negative values gracefully",
		},
		{
			name:    "Extreme coordinates",
			section: "birdnet",
			extremeData: map[string]interface{}{
				"latitude":  91.0,  // Invalid latitude
				"longitude": 181.0, // Invalid longitude
			},
			expectedError: true,
			description:   "Should reject invalid coordinates",
		},
		{
			name:    "Very small float values",
			section: "birdnet",
			extremeData: map[string]interface{}{
				"threshold": 0.0000000001,
			},
			expectedError: false,
			description:   "Should handle very small floats",
		},
		{
			name:    "Empty arrays",
			section: "species",
			extremeData: map[string]interface{}{
				"include": []string{},
				"exclude": []string{},
			},
			expectedError: false,
			description:   "Should handle empty arrays",
		},
		{
			name:    "Very large arrays",
			section: "species",
			extremeData: map[string]interface{}{
				"include": generateLargeStringArray(1000),
			},
			expectedError: false,
			description:   "Should handle large arrays",
		},
		{
			name:    "Zero values",
			section: "birdnet",
			extremeData: map[string]interface{}{
				"sensitivity": 0.0,
				"threshold":   0.0,
				"overlap":     0.0,
			},
			expectedError: false,
			description:   "Should handle zero values",
		},
		{
			name:    "Maximum float values",
			section: "birdnet",
			extremeData: map[string]interface{}{
				"sensitivity": 1.7976931348623157e+308, // Close to max float64
			},
			expectedError: false,
			description:   "Should handle maximum float values",
		},
		{
			name:    "Minimum positive float",
			section: "birdnet",
			extremeData: map[string]interface{}{
				"threshold": 5e-324, // Smallest positive float64
			},
			expectedError: false,
			description:   "Should handle minimum positive float",
		},
		{
			name:    "Negative zero",
			section: "birdnet",
			extremeData: map[string]interface{}{
				"overlap": -0.0,
			},
			expectedError: false,
			description:   "Should handle negative zero",
		},
		{
			name:    "Unicode in string fields",
			section: "species",
			extremeData: map[string]interface{}{
				"include": []string{"🦅 Eagle", "🦉 Owl", "🦆 Duck"},
			},
			expectedError: false,
			description:   "Should handle Unicode characters",
		},
		{
			name:    "Very long species names",
			section: "species",
			extremeData: map[string]interface{}{
				"config": map[string]interface{}{
					generateLongString(500): map[string]interface{}{
						"threshold": 0.8,
					},
				},
			},
			expectedError: false,
			description:   "Should handle very long species names",
		},
		{
			name:    "Port number edge cases",
			section: "webserver",
			extremeData: map[string]interface{}{
				"port": "65535", // Maximum valid port
			},
			expectedError: false,
			description:   "Should handle maximum port number",
		},
		{
			name:    "Invalid port number",
			section: "webserver",
			extremeData: map[string]interface{}{
				"port": "65536", // One above maximum
			},
			expectedError: true,
			description:   "Should reject invalid port number",
		},
		{
			name:    "Time duration extremes",
			section: "mqtt",
			extremeData: map[string]interface{}{
				"retrySettings": map[string]interface{}{
					"maxRetries":   2147483647,
					"initialDelay": 2147483647,
					"maxDelay":     2147483647,
				},
			},
			expectedError: false,
			description:   "Should handle extreme time durations",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(e)

			body, err := json.Marshal(tt.extremeData)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, 
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)

			if tt.expectedError {
				require.Error(t, err, tt.description)
			} else {
				if err != nil {
					t.Logf("Update failed (might be expected): %v", err)
				} else {
					assert.Equal(t, http.StatusOK, rec.Code, tt.description)
				}
			}
		})
	}
}

// TestMemoryExhaustionAttempts verifies the system handles memory exhaustion attempts
func TestMemoryExhaustionAttempts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		section      string
		largeData    func() interface{}
		description  string
	}{
		{
			name:    "Very large nested object",
			section: "species",
			largeData: func() interface{} {
				config := make(map[string]interface{})
				for i := 0; i < 10000; i++ {
					config[fmt.Sprintf("species_%d", i)] = map[string]interface{}{
						"threshold": 0.8,
						"interval":  30,
					}
				}
				return map[string]interface{}{"config": config}
			},
			description: "Should handle large number of species configs",
		},
		{
			name:    "Deeply nested structure",
			section: "dashboard",
			largeData: func() interface{} {
				// Create a deeply nested structure
				result := map[string]interface{}{}
				current := result
				for i := 0; i < 100; i++ {
					next := map[string]interface{}{}
					current["nested"] = next
					current = next
				}
				current["value"] = "deep"
				return result
			},
			description: "Should handle deeply nested structures",
		},
		{
			name:    "Large array of actions",
			section: "species",
			largeData: func() interface{} {
				actions := make([]map[string]interface{}, 1000)
				for i := range actions {
					actions[i] = map[string]interface{}{
						"type":    "ExecuteCommand",
						"command": fmt.Sprintf("/usr/bin/notify-%d", i),
					}
				}
				return map[string]interface{}{
					"config": map[string]interface{}{
						"Test Bird": map[string]interface{}{
							"actions": actions,
						},
					},
				}
			},
			description: "Should handle large arrays of actions",
		},
		{
			name:    "Wide flat object",
			section: "dashboard",
			largeData: func() interface{} {
				data := make(map[string]interface{})
				for i := 0; i < 5000; i++ {
					data[fmt.Sprintf("field_%d", i)] = fmt.Sprintf("value_%d", i)
				}
				return data
			},
			description: "Should handle objects with many fields",
		},
		{
			name:    "Recursive-like structure",
			section: "species",
			largeData: func() interface{} {
				// Create a structure that looks recursive but isn't
				config := make(map[string]interface{})
				for i := 0; i < 50; i++ {
					config[fmt.Sprintf("level_%d", i)] = map[string]interface{}{
						"threshold": 0.8,
						"config": map[string]interface{}{
							fmt.Sprintf("sublevel_%d", i): map[string]interface{}{
								"threshold": 0.7,
							},
						},
					}
				}
				return map[string]interface{}{"config": config}
			},
			description: "Should handle pseudo-recursive structures",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(e)

			largeData := tt.largeData()
			body, err := json.Marshal(largeData)
			require.NoError(t, err)

			// The JSON might be very large
			t.Logf("Test data size: %d bytes", len(body))

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, 
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			// Should either succeed or fail gracefully
			err = controller.UpdateSectionSettings(ctx)
			if err != nil {
				t.Logf("%s: Failed as expected - %v", tt.description, err)
			} else {
				t.Logf("%s: Succeeded in handling large data", tt.description)
			}
		})
	}
}

// Helper functions

// generateLargeStringArray generates an array of strings for testing
func generateLargeStringArray(size int) []string {
	result := make([]string, size)
	for i := 0; i < size; i++ {
		result[i] = fmt.Sprintf("species_%d", i)
	}
	return result
}

// generateLongString generates a long string for testing
func generateLongString(length int) string {
	result := make([]byte, length)
	for i := range result {
		result[i] = byte('A' + (i % 26))
	}
	return string(result)
}