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

// verifySkippedFields asserts that every field in shouldSkip appears in the
// response's skippedFields list, and that no field in shouldNotSkip does. A field
// tagged json:"-" is never carried through the PATCH merge, so it is silently
// ignored rather than reverted, and must therefore NOT appear in skippedFields;
// such fields belong in shouldNotSkip.
func verifySkippedFields(t *testing.T, response map[string]any, shouldSkip, shouldNotSkip []string) {
	t.Helper()

	skippedRaw, present := response["skippedFields"]
	skippedFields, isList := skippedRaw.([]any)

	// When we expect skips, the field must be present and a list. When we only
	// assert absence, an empty or missing list is a valid (passing) state.
	if len(shouldSkip) > 0 {
		require.True(t, present, "response is missing skippedFields")
		require.True(t, isList, "skippedFields is not a list, got %T", skippedRaw)
	}

	for _, expected := range shouldSkip {
		assert.True(t, isFieldSkipped(skippedFields, expected),
			"expected field %q to be skipped, got %v", expected, skippedFields)
	}
	for _, unexpected := range shouldNotSkip {
		assert.False(t, isFieldSkipped(skippedFields, unexpected),
			"field %q must not be reported as skipped (json:%q fields are ignored, not reverted), got %v",
			unexpected, "-", skippedFields)
	}
}

// TestDiagnosticsProfilingRateValidation is the HTTP-boundary regression for the
// diagnostics validator gap: a PATCH carrying a negative profiling rate used to
// return 200 and persist the negative to config.yaml. It must now be rejected,
// while a valid rate still succeeds.
func TestDiagnosticsProfilingRateValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		payload      map[string]any
		wantRejected bool
	}{
		{
			name:         "negative block rate rejected",
			payload:      map[string]any{"profiling": map[string]any{"blockRate": -1}},
			wantRejected: true,
		},
		{
			name:         "negative mutex fraction rejected",
			payload:      map[string]any{"profiling": map[string]any{"mutexFraction": -5}},
			wantRejected: true,
		},
		{
			name:         "valid rates accepted",
			payload:      map[string]any{"profiling": map[string]any{"blockRate": 10000, "mutexFraction": 100}},
			wantRejected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(t, e)

			body, err := json.Marshal(tt.payload)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/diagnostics",
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues("diagnostics")

			err = controller.UpdateSectionSettings(ctx)
			if tt.wantRejected {
				// A rejection is either a returned error or a non-2xx response;
				// HandleError writes a 400 and returns nil today. Assert the update
				// was NOT accepted unconditionally, so a future refactor that returns
				// the error instead of writing it cannot make this branch vacuous.
				accepted := err == nil && rec.Code == http.StatusOK
				assert.Falsef(t, accepted,
					"out-of-range rate must be rejected (err=%v, code=%d)", err, rec.Code)
				if err == nil {
					assert.Equal(t, http.StatusBadRequest, rec.Code,
						"negative rate should be rejected with 400")
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code, "valid rate should be accepted")
			}
		})
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
		name          string
		section       string
		update        any
		description   string
		shouldSkip    []string
		shouldNotSkip []string
	}{
		{
			name:    "Labels is ignored, not skipped",
			section: "birdnet",
			update: map[string]any{
				"labels": []string{"test1", "test2"}, // Runtime field, json:"-"
			},
			// Labels is tagged json:"-", so the PATCH merge never carries it and it
			// is never reverted: it must NOT appear in skippedFields. (A PUT walk
			// would list it as runtime-only; a PATCH cannot.)
			description:   "json:\"-\" field is silently ignored on PATCH",
			shouldNotSkip: []string{"Labels"},
		},
		{
			name:    "RangeFilter runtime fields",
			section: "birdnet",
			update: map[string]any{
				"rangeFilter": map[string]any{
					"species":     []string{"test species"}, // Runtime field (yaml:"-", json present)
					"lastUpdated": "2024-01-01T00:00:00Z",   // Runtime field (yaml:"-", json present)
					"threshold":   0.05,                     // Allowed field
				},
			},
			description: "reachable runtime fields are reverted and reported as skipped",
			shouldSkip:  []string{"Species", "LastUpdated"},
		},
		{
			name:    "SoxAudioTypes is ignored, not skipped",
			section: "audio",
			update: map[string]any{
				"soxAudioTypes": []string{"wav", "mp3"}, // Runtime field, json:"-"
				"export": map[string]any{
					"enabled": true, // Allowed field
				},
			},
			description:   "json:\"-\" field is silently ignored on PATCH",
			shouldNotSkip: []string{"SoxAudioTypes"},
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
			require.NoError(t, err, "UpdateSectionSettings returned an error")

			assert.Equal(t, http.StatusOK, rec.Code)

			var response map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)

			verifySkippedFields(t, response, tt.shouldSkip, tt.shouldNotSkip)
		})
	}
}

// TestComplexNestedPreservation verifies complex nested structures preserve all unmodified data
func TestComplexNestedPreservation(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)

	// Update controller settings with complex initial state
	// Use lowercase keys since that's what a real config would have after normalization
	controller.Settings.Load().Realtime.Species.Include = []string{"Robin", "Eagle", "Owl"}
	controller.Settings.Load().Realtime.Species.Exclude = []string{"Crow", "Pigeon"}
	controller.Settings.Load().Realtime.Species.Config["robin"] = conf.SpeciesConfig{
		Threshold: 0.8,
		Interval:  30,
		Actions: []conf.SpeciesAction{{
			Type:    "ExecuteCommand",
			Command: "/usr/bin/notify",
		}},
	}
	controller.Settings.Load().Realtime.Species.Config["eagle"] = conf.SpeciesConfig{
		Threshold: 0.9,
		Interval:  60,
	}

	// Capture initial state
	initialInclude := make([]string, len(controller.Settings.Load().Realtime.Species.Include))
	copy(initialInclude, controller.Settings.Load().Realtime.Species.Include)
	initialExclude := make([]string, len(controller.Settings.Load().Realtime.Species.Exclude))
	copy(initialExclude, controller.Settings.Load().Realtime.Species.Exclude)

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
	settings := controller.Settings.Load()

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
