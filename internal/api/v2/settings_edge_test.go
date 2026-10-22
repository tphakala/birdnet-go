package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
//
// Not parallel: a valid rate is applied to the process-global block and mutex
// profile rates, which resetProfileRates puts back to off afterwards.
func TestDiagnosticsProfilingRateValidation(t *testing.T) {
	resetProfileRates(t)

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

// TestComplexNestedPreservation verifies that updating one species entry leaves
// the include and exclude lists and the other entries as they were, including an
// entry's actions with their parameters. The second row overwrites an existing
// entry with zero values (threshold, interval and an emptied actions list), which
// a merge that skips zero values would silently keep.
//
// Not parallel: see the note in settings_blocked_fields_test.go.
func TestComplexNestedPreservation(t *testing.T) {
	// Seed keys are lowercase, as a real config has after load normalization.
	notifyActions := []conf.SpeciesAction{{
		Type:            "ExecuteCommand",
		Command:         "/usr/bin/notify",
		Parameters:      []string{"--species", "American Robin"},
		ExecuteDefaults: true,
	}}
	eagle := conf.SpeciesConfig{Threshold: 0.9, Interval: 60}

	tests := []struct {
		name    string
		section string
		seed    conf.SpeciesSettings
		body    map[string]any
		check   func(t *testing.T, got conf.SpeciesSettings)
	}{
		{
			name:    "threshold change keeps the lists, the actions and the other entry",
			section: "species",
			seed: conf.SpeciesSettings{
				Include: []string{"Robin", "Eagle", "Owl"},
				Exclude: []string{"Crow", "Pigeon"},
				Config: map[string]conf.SpeciesConfig{
					"american robin": {Threshold: 0.8, Interval: 30, Actions: notifyActions},
					"eagle":          eagle,
				},
			},
			// The payload key is cased differently from the stored one: keys are
			// normalized to lowercase.
			body: map[string]any{"config": map[string]any{"American Robin": map[string]any{"threshold": 0.85}}},
			check: func(t *testing.T, got conf.SpeciesSettings) {
				t.Helper()
				assert.Equal(t, []string{"Robin", "Eagle", "Owl"}, got.Include)
				assert.Equal(t, []string{"Crow", "Pigeon"}, got.Exclude)

				robin := got.Config["american robin"]
				assert.InDelta(t, 0.85, robin.Threshold, 0.0001)
				assert.Equal(t, 30, robin.Interval)
				assert.Equal(t, notifyActions, robin.Actions)

				assert.Equal(t, eagle, got.Config["eagle"])
			},
		},
		{
			name:    "zero values overwrite an existing entry and leave the next one",
			section: "realtime",
			seed: conf.SpeciesSettings{
				Config: map[string]conf.SpeciesConfig{
					"bird a": {Threshold: 0.7, Interval: 30, Actions: notifyActions},
					"bird b": {Threshold: 0.8, Interval: 60, Actions: []conf.SpeciesAction{}},
				},
			},
			body: map[string]any{"species": map[string]any{"config": map[string]any{
				"Bird A": map[string]any{"threshold": 0.0, "interval": 0, "actions": []any{}},
				"Bird B": map[string]any{"threshold": 0.8, "interval": 60, "actions": []any{}},
			}}},
			check: func(t *testing.T, got conf.SpeciesSettings) {
				t.Helper()
				birdA := got.Config["bird a"]
				assert.InDelta(t, 0.0, birdA.Threshold, 0.0001, "bird a threshold should be zero")
				assert.Equal(t, 0, birdA.Interval, "bird a interval should be zero")
				assert.Empty(t, birdA.Actions, "bird a actions should be cleared")
				assert.NotNil(t, birdA.Actions, "bird a actions should be an empty slice, not nil")

				birdB := got.Config["bird b"]
				assert.InDelta(t, 0.8, birdB.Threshold, 0.0001, "bird b threshold should be unchanged")
				assert.Equal(t, 60, birdB.Interval, "bird b interval should be unchanged")
				assert.NotNil(t, birdB.Actions, "bird b actions should be an empty slice, not nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := newMergeController(t, e, func(s *conf.Settings) { s.Realtime.Species = tt.seed })

			patchSection(t, e, controller, tt.section, tt.body)

			tt.check(t, controller.Settings.Load().Realtime.Species)
		})
	}
}
