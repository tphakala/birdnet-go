package api

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// summaryLimitReset is the value an out-of-range dashboard summaryLimit is reset
// to (internal/conf/validate_realtime.go:169-172).
const summaryLimitReset = 10

// summaryLimitIsReset asserts the dashboard summaryLimit was reset, not stored.
func summaryLimitIsReset(t *testing.T, s *conf.Settings) {
	t.Helper()
	assert.Equal(t, summaryLimitReset, s.Realtime.Dashboard.SummaryLimit)
}

// TestPatchNumericBounds pins the numeric limits a section PATCH enforces.
// Rejected rows must answer 400 and publish nothing. Accepted rows must answer
// 200 and store the value, which an out-of-range value that is clamped instead
// of rejected (summaryLimit) shows as the clamped result.
//
// Every row sits on a limit that a validator checks (conf.ValidateBirdNETSettings
// and conf.ValidateWebServerSettings in internal/conf/validate_services.go,
// validateDashboardSettings in internal/conf/validate_realtime.go). A value no
// validator bounds would only prove that encoding/json round-trips it, so there
// are no such rows.
//
// Not parallel: see the note in settings_blocked_fields_test.go.
func TestPatchNumericBounds(t *testing.T) {
	tests := []struct {
		name    string
		section string
		body    map[string]any
		// accepted is nil for a row that must be rejected with 400.
		accepted func(t *testing.T, s *conf.Settings)
	}{
		{
			name:    "coordinates beyond the globe are rejected",
			section: "birdnet",
			body:    map[string]any{"latitude": 91.0, "longitude": 181.0},
		},
		{
			name:    "sensitivity near the float maximum is rejected",
			section: "birdnet",
			body:    map[string]any{"sensitivity": 1.7976931348623157e+308},
		},
		{
			name:    "port one above the maximum is rejected",
			section: "webserver",
			body:    map[string]any{"port": "65536"},
		},
		{
			name:    "coordinates at the minimum are accepted",
			section: "birdnet",
			body:    map[string]any{"latitude": -90.0, "longitude": -180.0},
			accepted: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.InDelta(t, -90.0, s.BirdNET.Latitude, 0.0001)
				assert.InDelta(t, -180.0, s.BirdNET.Longitude, 0.0001)
			},
		},
		{
			name:    "coordinates at the maximum are accepted",
			section: "birdnet",
			body:    map[string]any{"latitude": 90.0, "longitude": 180.0},
			accepted: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.InDelta(t, 90.0, s.BirdNET.Latitude, 0.0001)
				assert.InDelta(t, 180.0, s.BirdNET.Longitude, 0.0001)
			},
		},
		{
			name:    "sensitivity, threshold and overlap at their lower bounds are accepted",
			section: "birdnet",
			body:    map[string]any{"sensitivity": 0.0, "threshold": 0.0, "overlap": 0.0},
			accepted: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.InDelta(t, 0.0, s.BirdNET.Sensitivity, 0.0001)
				assert.InDelta(t, 0.0, s.BirdNET.Threshold, 0.0001)
				assert.InDelta(t, 0.0, s.BirdNET.Overlap, 0.0001)
			},
		},
		{
			name:    "sensitivity, threshold and overlap at their upper bounds are accepted",
			section: "birdnet",
			body:    map[string]any{"sensitivity": 1.5, "threshold": 1.0, "overlap": 2.99},
			accepted: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.InDelta(t, 1.5, s.BirdNET.Sensitivity, 0.0001)
				assert.InDelta(t, 1.0, s.BirdNET.Threshold, 0.0001)
				assert.InDelta(t, 2.99, s.BirdNET.Overlap, 0.0001)
			},
		},
		{
			name:    "minimum port is accepted",
			section: "webserver",
			body:    map[string]any{"port": "1"},
			accepted: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "1", s.WebServer.Port)
			},
		},
		{
			name:    "maximum port is accepted",
			section: "webserver",
			body:    map[string]any{"port": "65535"},
			accepted: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "65535", s.WebServer.Port)
			},
		},
		// An out-of-range summaryLimit is reset instead of rejected
		// (internal/conf/validate_realtime.go:169-172).
		{
			name:     "summaryLimit above the maximum is reset to the minimum",
			section:  "dashboard",
			body:     map[string]any{"summaryLimit": 2147483647},
			accepted: summaryLimitIsReset,
		},
		{
			name:     "negative summaryLimit is reset to the minimum",
			section:  "dashboard",
			body:     map[string]any{"summaryLimit": -100},
			accepted: summaryLimitIsReset,
		},
		{
			name:     "summaryLimit below the minimum is reset to the minimum",
			section:  "dashboard",
			body:     map[string]any{"summaryLimit": 5},
			accepted: summaryLimitIsReset,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)
			before := controller.Settings.Load()

			rec := sendSectionPatch(t, e, controller, tt.section, tt.body)

			if tt.accepted == nil {
				assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
				assert.Same(t, before, controller.Settings.Load(), "a rejected value must not be published")
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			tt.accepted(t, controller.Settings.Load())
		})
	}
}
