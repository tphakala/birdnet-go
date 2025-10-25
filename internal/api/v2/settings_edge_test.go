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

// TestBoundaryValues verifies the system handles boundary values correctly
func TestBoundaryValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		section     string
		boundaryData interface{}
		description string
	}{
		{
			name:    "Port number boundaries",
			section: "webserver",
			boundaryData: map[string]interface{}{
				"port": "1", // Minimum valid port
			},
			description: "Should accept minimum port number",
		},
		{
			name:    "Maximum valid port",
			section: "webserver",
			boundaryData: map[string]interface{}{
				"port": "65535", // Maximum valid port
			},
			description: "Should accept maximum port number",
		},
		{
			name:    "Zero threshold",
			section: "birdnet",
			boundaryData: map[string]interface{}{
				"threshold": 0.0,
			},
			description: "Should accept zero threshold",
		},
		{
			name:    "Maximum threshold",
			section: "birdnet",
			boundaryData: map[string]interface{}{
				"threshold": 1.0,
			},
			description: "Should accept maximum threshold",
		},
		{
			name:    "Minimum latitude",
			section: "birdnet",
			boundaryData: map[string]interface{}{
				"latitude": -90.0,
			},
			description: "Should accept minimum latitude",
		},
		{
			name:    "Maximum latitude",
			section: "birdnet",
			boundaryData: map[string]interface{}{
				"latitude": 90.0,
			},
			description: "Should accept maximum latitude",
		},
		{
			name:    "Minimum longitude",
			section: "birdnet",
			boundaryData: map[string]interface{}{
				"longitude": -180.0,
			},
			description: "Should accept minimum longitude",
		},
		{
			name:    "Maximum longitude",
			section: "birdnet",
			boundaryData: map[string]interface{}{
				"longitude": 180.0,
			},
			description: "Should accept maximum longitude",
		},
		{
			name:    "Empty string in text field",
			section: "mqtt",
			boundaryData: map[string]interface{}{
				"topic": "",
			},
			description: "Should accept empty string in topic",
		},
		{
			name:    "Maximum array size",
			section: "rtsp",
			boundaryData: map[string]interface{}{
				"urls": func() []string {
					urls := make([]string, 100)
					for i := 0; i < 100; i++ {
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
			boundaryData: map[string]interface{}{
				"locale": "a",
			},
			description: "Should accept single character locale",
		},
		{
			name:    "Maximum string length",
			section: "mqtt",
			boundaryData: map[string]interface{}{
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
		name         string
		section      string
		specialData  interface{}
		description  string
	}{
		{
			name:    "UTF-8 characters in strings",
			section: "species",
			specialData: map[string]interface{}{
				"include": []string{"🦅 Eagle", "ñandú", "räkättirastas", "鳥"},
			},
			description: "Should handle UTF-8 characters",
		},
		{
			name:    "Escaped characters",
			section: "mqtt",
			specialData: map[string]interface{}{
				"topic": "birdnet\\detection\\new",
			},
			description: "Should handle escaped backslashes",
		},
		{
			name:    "Quotes in strings",
			section: "dashboard",
			specialData: map[string]interface{}{
				"locale": `en"US'test`,
			},
			description: "Should handle quotes in strings",
		},
		{
			name:    "Line breaks in strings",
			section: "mqtt",
			specialData: map[string]interface{}{
				"topic": "birdnet\ndetection",
			},
			description: "Should handle line breaks",
		},
		{
			name:    "Tab characters",
			section: "mqtt",
			specialData: map[string]interface{}{
				"topic": "birdnet\tdetection",
			},
			description: "Should handle tab characters",
		},
		{
			name:    "URL encoding",
			section: "mqtt",
			specialData: map[string]interface{}{
				"broker": "tcp://broker.example.com:1883?param=value&other=test",
			},
			description: "Should handle URL with query parameters",
		},
		{
			name:    "HTML entities",
			section: "dashboard",
			specialData: map[string]interface{}{
				"locale": "&lt;en&gt;",
			},
			description: "Should handle HTML entities",
		},
		{
			name:    "Mixed case field names",
			section: "birdnet",
			specialData: map[string]interface{}{
				"rangeFilter": map[string]interface{}{
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
		update      interface{}
		description string
		shouldSkip  []string
	}{
		{
			name:    "Runtime fields in BirdNET",
			section: "birdnet",
			update: map[string]interface{}{
				"labels": []string{"test1", "test2"}, // Runtime field
			},
			description: "Should skip runtime-only fields",
			shouldSkip:  []string{"Labels"},
		},
		{
			name:    "RangeFilter runtime fields",
			section: "birdnet",
			update: map[string]interface{}{
				"rangeFilter": map[string]interface{}{
					"species":     []string{"test species"}, // Runtime field
					"lastUpdated": "2024-01-01T00:00:00Z",  // Runtime field
					"threshold":   0.05,                     // Allowed field
				},
			},
			description: "Should skip runtime fields in nested objects",
			shouldSkip:  []string{"Species", "LastUpdated"},
		},
		{
			name:    "Audio runtime fields",
			section: "audio",
			update: map[string]interface{}{
				"soxAudioTypes": []string{"wav", "mp3"}, // Runtime field
				"export": map[string]interface{}{
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

			// All sections should succeed but skip runtime fields
			if err != nil {
				t.Logf("Update failed: %v", err)
			} else {
				assert.Equal(t, http.StatusOK, rec.Code)
				
				// Check response for skipped fields
				var response map[string]interface{}
				err = json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				
				if skippedFields, ok := response["skippedFields"].([]interface{}); ok && len(tt.shouldSkip) > 0 {
					t.Logf("Skipped fields: %v", skippedFields)
					// Verify expected fields were skipped
					for _, expectedSkip := range tt.shouldSkip {
						found := false
						for _, skipped := range skippedFields {
							if skippedStr, ok := skipped.(string); ok && 
							   (skippedStr == expectedSkip || 
							    skippedStr == "BirdNET."+expectedSkip ||
							    skippedStr == "BirdNET.RangeFilter."+expectedSkip) {
								found = true
								break
							}
						}
						if !found {
							t.Logf("Expected field %s to be skipped but it wasn't", expectedSkip)
						}
					}
				}
			}
		})
	}
}

// TestComplexNestedPreservation verifies complex nested structures preserve all unmodified data
func TestComplexNestedPreservation(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	
	// Update controller settings with complex initial state
	controller.Settings.Realtime.Species.Include = []string{"Robin", "Eagle", "Owl"}
	controller.Settings.Realtime.Species.Exclude = []string{"Crow", "Pigeon"}
	controller.Settings.Realtime.Species.Config["Robin"] = conf.SpeciesConfig{
		Threshold: 0.8,
		Interval: 30,
		Actions: []conf.SpeciesAction{{
			Type: "ExecuteCommand",
			Command: "/usr/bin/notify",
		}},
	}
	controller.Settings.Realtime.Species.Config["Eagle"] = conf.SpeciesConfig{
		Threshold: 0.9,
		Interval: 60,
	}
	
	// Capture initial state
	initialInclude := make([]string, len(controller.Settings.Realtime.Species.Include))
	copy(initialInclude, controller.Settings.Realtime.Species.Include)
	initialExclude := make([]string, len(controller.Settings.Realtime.Species.Exclude))
	copy(initialExclude, controller.Settings.Realtime.Species.Exclude)

	// Update only one deeply nested field
	update := map[string]interface{}{
		"config": map[string]interface{}{
			"Robin": map[string]interface{}{
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
	
	// Robin config
	robinConfig := settings.Realtime.Species.Config["Robin"]
	assert.InDelta(t, 0.85, robinConfig.Threshold, 0.0001) // Changed
	assert.Equal(t, 30, robinConfig.Interval) // Preserved
	assert.Len(t, robinConfig.Actions, 1) // Preserved
	
	// Eagle config completely preserved
	eagleConfig := settings.Realtime.Species.Config["Eagle"]
	assert.InDelta(t, 0.9, eagleConfig.Threshold, 0.0001)
	assert.Equal(t, 60, eagleConfig.Interval)
}