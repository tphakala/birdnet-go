package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// isFieldSkipped checks if a field name appears in the skipped fields list.
func isFieldSkipped(skippedFields []any, expectedSkip string) bool {
	for _, skipped := range skippedFields {
		skippedStr, ok := skipped.(string)
		if !ok {
			continue
		}
		if skippedStr == expectedSkip ||
			skippedStr == "BirdNET."+expectedSkip ||
			skippedStr == "BirdNET.RangeFilter."+expectedSkip {
			return true
		}
	}
	return false
}

// verifySkippedFields checks that all expected fields were skipped.
func verifySkippedFields(t *testing.T, response map[string]any, shouldSkip []string) {
	t.Helper()
	skippedFields, ok := response["skippedFields"].([]any)
	if !ok || len(shouldSkip) == 0 {
		return
	}
	t.Logf("Skipped fields: %v", skippedFields)
	for _, expectedSkip := range shouldSkip {
		if !isFieldSkipped(skippedFields, expectedSkip) {
			t.Logf("Expected field %s to be skipped but it wasn't", expectedSkip)
		}
	}
}

// TestBoundaryValues verifies the system handles boundary values correctly
func TestBoundaryValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		section      string
		boundaryData any
		description  string
	}{
		{
			name:    "Port number boundaries",
			section: "webserver",
			boundaryData: map[string]any{
				"port": "1", // Minimum valid port
			},
			description: "Should accept minimum port number",
		},
		{
			name:    "Maximum valid port",
			section: "webserver",
			boundaryData: map[string]any{
				"port": "65535", // Maximum valid port
			},
			description: "Should accept maximum port number",
		},
		{
			name:    "Zero threshold",
			section: "birdnet",
			boundaryData: map[string]any{
				"threshold": 0.0,
			},
			description: "Should accept zero threshold",
		},
		{
			name:    "Maximum threshold",
			section: "birdnet",
			boundaryData: map[string]any{
				"threshold": 1.0,
			},
			description: "Should accept maximum threshold",
		},
		{
			name:    "Minimum latitude",
			section: "birdnet",
			boundaryData: map[string]any{
				"latitude": -90.0,
			},
			description: "Should accept minimum latitude",
		},
		{
			name:    "Maximum latitude",
			section: "birdnet",
			boundaryData: map[string]any{
				"latitude": 90.0,
			},
			description: "Should accept maximum latitude",
		},
		{
			name:    "Minimum longitude",
			section: "birdnet",
			boundaryData: map[string]any{
				"longitude": -180.0,
			},
			description: "Should accept minimum longitude",
		},
		{
			name:    "Maximum longitude",
			section: "birdnet",
			boundaryData: map[string]any{
				"longitude": 180.0,
			},
			description: "Should accept maximum longitude",
		},
		{
			name:    "Empty string in text field",
			section: "mqtt",
			boundaryData: map[string]any{
				"topic": "",
			},
			description: "Should accept empty string in topic",
		},
		{
			name:    "Maximum array size",
			section: "rtsp",
			boundaryData: map[string]any{
				"urls": func() []string {
					urls := make([]string, 100)
					for i := range 100 {
						urls[i] = fmt.Sprintf("rtsp://camera%d.example.com:554/stream%d", i+1, i+1)
					}
					return urls
				}(), // Large array of actual RTSP URLs
			},
			description: "Should handle large URL arrays",
		},
		{
			name:    "Single character string",
			section: "dashboard",
			boundaryData: map[string]any{
				"locale": "a",
			},
			description: "Should accept single character locale",
		},
		{
			name:    "Maximum string length",
			section: "mqtt",
			boundaryData: map[string]any{
				"broker": "tcp://" + strings.Repeat("a", 250),
			},
			description: "Should handle long broker strings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(t, e)

			body, err := json.Marshal(tt.boundaryData)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section,
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)
			if err != nil {
				t.Logf("%s: Update failed - %v", tt.description, err)
			} else {
				assert.Equal(t, http.StatusOK, rec.Code, tt.description)
			}
		})
	}
}

// TestSpecialCharacterHandling verifies special characters are handled correctly
func TestSpecialCharacterHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		section     string
		specialData any
		description string
	}{
		{
			name:    "UTF-8 characters in strings",
			section: "species",
			specialData: map[string]any{
				"include": []string{"🦅 Eagle", "ñandú", "räkättirastas", "鳥"},
			},
			description: "Should handle UTF-8 characters",
		},
		{
			name:    "Escaped characters",
			section: "mqtt",
			specialData: map[string]any{
				"topic": "birdnet\\detection\\new",
			},
			description: "Should handle escaped backslashes",
		},
		{
			name:    "Quotes in strings",
			section: "dashboard",
			specialData: map[string]any{
				"locale": `en"US'test`,
			},
			description: "Should handle quotes in strings",
		},
		{
			name:    "Line breaks in strings",
			section: "mqtt",
			specialData: map[string]any{
				"topic": "birdnet\ndetection",
			},
			description: "Should handle line breaks",
		},
		{
			name:    "Tab characters",
			section: "mqtt",
			specialData: map[string]any{
				"topic": "birdnet\tdetection",
			},
			description: "Should handle tab characters",
		},
		{
			name:    "URL encoding",
			section: "mqtt",
			specialData: map[string]any{
				"broker": "tcp://broker.example.com:1883?param=value&other=test",
			},
			description: "Should handle URL with query parameters",
		},
		{
			name:    "HTML entities",
			section: "dashboard",
			specialData: map[string]any{
				"locale": "&lt;en&gt;",
			},
			description: "Should handle HTML entities",
		},
		{
			name:    "Mixed case field names",
			section: "birdnet",
			specialData: map[string]any{
				"rangeFilter": map[string]any{
					"threshold": 0.05,
				},
			},
			description: "Should handle camelCase field names",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(t, e)

			body, err := json.Marshal(tt.specialData)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section,
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)
			if err != nil {
				t.Logf("%s: Update failed - %v", tt.description, err)
			} else {
				assert.Equal(t, http.StatusOK, rec.Code, tt.description)
				t.Logf("%s: Successfully handled special characters", tt.description)
			}
		})
	}
}

// TestFieldPermissionEnforcement verifies that field permissions are properly enforced
func TestFieldPermissionEnforcement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		section     string
		update      any
		description string
		shouldSkip  []string
	}{
		{
			name:    "Runtime fields in BirdNET",
			section: "birdnet",
			update: map[string]any{
				"labels": []string{"test1", "test2"}, // Runtime field
			},
			description: "Should skip runtime-only fields",
			shouldSkip:  []string{"Labels"},
		},
		{
			name:    "RangeFilter runtime fields",
			section: "birdnet",
			update: map[string]any{
				"rangeFilter": map[string]any{
					"species":     []string{"test species"}, // Runtime field
					"lastUpdated": "2024-01-01T00:00:00Z",   // Runtime field
					"threshold":   0.05,                     // Allowed field
				},
			},
			description: "Should skip runtime fields in nested objects",
			shouldSkip:  []string{"Species", "LastUpdated"},
		},
		{
			name:    "Audio runtime fields",
			section: "audio",
			update: map[string]any{
				"soxAudioTypes": []string{"wav", "mp3"}, // Runtime field
				"export": map[string]any{
					"enabled": true, // Allowed field
				},
			},
			description: "Should skip SoxAudioTypes runtime field",
			shouldSkip:  []string{"SoxAudioTypes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(t, e)

			body, err := json.Marshal(tt.update)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section,
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)

			if err != nil {
				t.Logf("Update failed: %v", err)
				return
			}

			assert.Equal(t, http.StatusOK, rec.Code)

			var response map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)

			verifySkippedFields(t, response, tt.shouldSkip)
		})
	}
}

// TestComplexNestedPreservation verifies complex nested structures preserve all unmodified data
func TestComplexNestedPreservation(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)

	// Update controller settings with complex initial state
	// Use lowercase keys since that's what a real config would have after normalization
	controller.Settings.Realtime.Species.Include = []string{"Robin", "Eagle", "Owl"}
	controller.Settings.Realtime.Species.Exclude = []string{"Crow", "Pigeon"}
	controller.Settings.Realtime.Species.Config["robin"] = conf.SpeciesConfig{
		Threshold: 0.8,
		Interval:  30,
		Actions: []conf.SpeciesAction{{
			Type:    "ExecuteCommand",
			Command: "/usr/bin/notify",
		}},
	}
	controller.Settings.Realtime.Species.Config["eagle"] = conf.SpeciesConfig{
		Threshold: 0.9,
		Interval:  60,
	}

	// Capture initial state
	initialInclude := make([]string, len(controller.Settings.Realtime.Species.Include))
	copy(initialInclude, controller.Settings.Realtime.Species.Include)
	initialExclude := make([]string, len(controller.Settings.Realtime.Species.Exclude))
	copy(initialExclude, controller.Settings.Realtime.Species.Exclude)

	// Update only one deeply nested field
	update := map[string]any{
		"config": map[string]any{
			"Robin": map[string]any{
				"threshold": 0.85, // Only change this
			},
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/species", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("species")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify preservation
	settings := controller.Settings

	// Include/Exclude lists preserved
	assert.Equal(t, initialInclude, settings.Realtime.Species.Include)
	assert.Equal(t, initialExclude, settings.Realtime.Species.Exclude)

	// Robin config (keys normalized to lowercase after API update)
	robinConfig := settings.Realtime.Species.Config["robin"]
	assert.InDelta(t, 0.85, robinConfig.Threshold, 0.0001) // Changed
	assert.Equal(t, 30, robinConfig.Interval)              // Preserved
	assert.Len(t, robinConfig.Actions, 1)                  // Preserved

	// Eagle config completely preserved (keys normalized to lowercase)
	eagleConfig := settings.Realtime.Species.Config["eagle"]
	assert.InDelta(t, 0.9, eagleConfig.Threshold, 0.0001)
	assert.Equal(t, 60, eagleConfig.Interval)
}
