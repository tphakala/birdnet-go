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

// TestPatchMissingSections verifies that PATCH requests to the logging,
// alerting, backup, and output sections return 200 instead of 400 (unknown
// section). This is a regression test for adding these four section cases to
// getSettingsSectionValue.
func TestPatchMissingSections(t *testing.T) {
	tests := []struct {
		name    string
		section string
		body    map[string]any
		verify  func(t *testing.T, settings *conf.Settings)
	}{
		{
			name:    "logging section accepts valid update",
			section: "logging",
			body: map[string]any{
				"level": "debug",
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				assert.Equal(t, "debug", settings.Logging.Level)
			},
		},
		{
			name:    "alerting section accepts valid update",
			section: "alerting",
			body: map[string]any{
				"historyRetentionDays": 30,
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				assert.Equal(t, 30, settings.Alerting.HistoryRetentionDays)
			},
		},
		{
			name:    "backup section accepts valid update",
			section: "backup",
			body: map[string]any{
				"enabled": true,
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				assert.True(t, settings.Backup.Enabled)
			},
		},
		{
			name:    "output section accepts valid update",
			section: "output",
			body: map[string]any{
				"sqlite": map[string]any{
					"enabled": true,
					"path":    "/tmp/test.db",
				},
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				assert.True(t, settings.Output.SQLite.Enabled)
				assert.Equal(t, "/tmp/test.db", settings.Output.SQLite.Path)
			},
		},
		{
			name:    "perch section accepts valid update",
			section: "perch",
			body: map[string]any{
				"threshold": 0.8,
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				assert.InDelta(t, 0.8, settings.Perch.Threshold, 1e-9)
			},
		},
		{
			name:    "models section accepts valid update",
			section: "models",
			body: map[string]any{
				"enabled": []string{"birdnet", "perch_v2"},
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				assert.Equal(t, []string{"birdnet", "perch_v2"}, settings.Models.Enabled)
			},
		},
		{
			name:    "taxonomysynonyms section accepts valid update",
			section: "taxonomysynonyms",
			body: map[string]any{
				"Parus major": "Great Tit",
			},
			verify: func(t *testing.T, settings *conf.Settings) {
				t.Helper()
				require.Contains(t, settings.TaxonomySynonyms, "Parus major")
				assert.Equal(t, "Great Tit", settings.TaxonomySynonyms["Parus major"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)

			body, err := json.Marshal(tt.body)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			tt.verify(t, controller.Settings)
		})
	}
}

// TestPatchTaxonomySynonymsMerge verifies that PATCH merges new synonym
// entries with existing ones instead of replacing the entire map.
func TestPatchTaxonomySynonymsMerge(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)

	controller.Settings.TaxonomySynonyms = map[string]string{
		"Corvus corax": "Common Raven",
	}

	body, err := json.Marshal(map[string]any{
		"Parus major": "Great Tit",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/taxonomysynonyms", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("taxonomysynonyms")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "Common Raven", controller.Settings.TaxonomySynonyms["Corvus corax"],
		"existing entry must be preserved after PATCH")
	assert.Equal(t, "Great Tit", controller.Settings.TaxonomySynonyms["Parus major"],
		"new entry must be added by PATCH")
}

// TestPatchAlertingValidation verifies that the alerting section validator
// rejects negative historyRetentionDays.
func TestPatchAlertingValidation(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	// Enable debug so the raw validation error is exposed in the "error" field
	controller.Settings.WebServer.Debug = true

	body, err := json.Marshal(map[string]any{
		"historyRetentionDays": -1,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/alerting", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("alerting")

	err = controller.UpdateSectionSettings(ctx)
	// The handler wraps the validation error, so check the status code and
	// the raw error field (exposed in debug mode) for the validation message.
	require.NoError(t, err, "handler should return nil and send HTTP response")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "historyRetentionDays must be non-negative")
}

// TestPatchAlertingZeroRetention verifies that zero is accepted as a valid
// value for historyRetentionDays (meaning unlimited retention).
func TestPatchAlertingZeroRetention(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Alerting.HistoryRetentionDays = 30

	body, err := json.Marshal(map[string]any{
		"historyRetentionDays": 0,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/alerting", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("alerting")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, controller.Settings.Alerting.HistoryRetentionDays)
}
