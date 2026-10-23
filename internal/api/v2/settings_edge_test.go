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
