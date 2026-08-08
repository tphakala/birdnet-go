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
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// putRawSettings drives UpdateSettings (PUT /api/v2/settings) with a raw JSON
// body, so a test can send a partial document the way a script or integration
// caller would. This is the path that regressed in #3993: putFullSettings always
// marshals a complete *conf.Settings, so it can never exercise an omitted key.
func putRawSettings(t *testing.T, e *echo.Echo, c *Controller, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader([]byte(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code, "PUT must succeed; body: %s", rec.Body.String())
	return rec
}

// newMergeController builds a settings controller from getTestSettings with an
// optional mutation applied to the seeded snapshot before it is stored.
func newMergeController(t *testing.T, e *echo.Echo, mutate func(*conf.Settings)) *Controller {
	t.Helper()
	c := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	s := getTestSettings(t)
	if mutate != nil {
		mutate(s)
	}
	c.Settings.Store(s)
	return c
}

// TestPutNarrowUpdatePreservesOmittedFields is the #3993 regression: a partial PUT
// that mentions only birdnet.sensitivity must not blank the fields it omitted.
// Before the fix, the typed bind filled every omitted field with its Go zero value
// and the reflective apply wrote those zeros over the live settings.
func TestPutNarrowUpdatePreservesOmittedFields(t *testing.T) {
	e := echo.New()
	controller := newMergeController(t, e, func(s *conf.Settings) {
		s.Output.SQLite.Enabled = true
		s.Output.SQLite.Path = "/data/birdnet.db"
		s.Realtime.Species.Include = []string{"Turdus merula", "Erithacus rubecula"}
		s.TaxonomySynonyms = map[string]string{"Poecile atricapillus": "Parus atricapillus"}
	})

	putRawSettings(t, e, controller, `{"birdnet":{"sensitivity":1.2}}`)

	got := controller.Settings.Load()
	assert.InDelta(t, 1.2, got.BirdNET.Sensitivity, 0.0001, "the field that was sent must change")
	// Everything omitted from the body must be preserved (this is the bug).
	assert.InDelta(t, testNewYorkLatitude, got.BirdNET.Latitude, 0.0001, "latitude must not reset to 0")
	assert.InDelta(t, testNewYorkLongitude, got.BirdNET.Longitude, 0.0001, "longitude must not reset to 0")
	assert.InDelta(t, 0.8, got.BirdNET.Threshold, 0.0001, "threshold must be preserved")
	assert.Equal(t, "/data/birdnet.db", got.Output.SQLite.Path, "sqlite path must not be blanked")
	assert.True(t, got.Output.SQLite.Enabled, "sqlite enabled must be preserved")
	assert.Equal(t, []string{"Turdus merula", "Erithacus rubecula"}, got.Realtime.Species.Include,
		"species include list must be preserved")
	assert.Equal(t, map[string]string{"Poecile atricapillus": "Parus atricapillus"}, got.TaxonomySynonyms,
		"a Go map omitted from the body must be preserved")
}

// TestPutExplicitZeroAndEmptyAreApplied proves the merge does not swallow
// intentional clears: an explicitly-present zero/empty value is applied, unlike an
// absent key which is preserved. Without this, "merge" would be indistinguishable
// from "ignore".
func TestPutExplicitZeroAndEmptyAreApplied(t *testing.T) {
	t.Run("explicit zero number is applied", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, nil)
		putRawSettings(t, e, controller, `{"birdnet":{"sensitivity":0}}`)
		assert.InDelta(t, 0.0, controller.Settings.Load().BirdNET.Sensitivity, 0.0001)
	})

	t.Run("explicit false bool is applied", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, func(s *conf.Settings) {
			s.Realtime.Dashboard.Thumbnails.Summary = true
		})
		putRawSettings(t, e, controller, `{"realtime":{"dashboard":{"thumbnails":{"summary":false}}}}`)
		assert.False(t, controller.Settings.Load().Realtime.Dashboard.Thumbnails.Summary)
	})

	t.Run("explicit empty array clears the list", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, func(s *conf.Settings) {
			s.Realtime.Species.Include = []string{"Turdus merula"}
		})
		putRawSettings(t, e, controller, `{"realtime":{"species":{"include":[]}}}`)
		assert.Empty(t, controller.Settings.Load().Realtime.Species.Include,
			"an explicit empty array must clear the list")
	})

	t.Run("explicit null clears the list while omission preserves it", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, func(s *conf.Settings) {
			s.Realtime.Species.Include = []string{"Turdus merula"}
		})
		// Omitting the section preserves the slice.
		putRawSettings(t, e, controller, `{"birdnet":{"sensitivity":1.1}}`)
		assert.Equal(t, []string{"Turdus merula"}, controller.Settings.Load().Realtime.Species.Include,
			"omitting the key must preserve the slice")
		// Explicit null clears it.
		putRawSettings(t, e, controller, `{"realtime":{"species":{"include":null}}}`)
		assert.Empty(t, controller.Settings.Load().Realtime.Species.Include,
			"an explicit null must clear the slice")
	})

	t.Run("explicit null on a scalar is a no-op (matches PATCH)", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, func(s *conf.Settings) {
			s.BirdNET.Sensitivity = 1.2
		})
		// json.Unmarshal ignores a JSON null for a non-pointer scalar, so the merge
		// leaves sensitivity unchanged rather than resetting it to 0. This mirrors
		// PATCH's mergeJSONIntoStruct exactly; only slices and maps are cleared by
		// an explicit null.
		putRawSettings(t, e, controller, `{"birdnet":{"sensitivity":null}}`)
		assert.InDelta(t, 1.2, controller.Settings.Load().BirdNET.Sensitivity, 0.0001,
			"null on a scalar must be a no-op, not a reset to zero")
	})
}

// TestPutPreservesRuntimeOnlyFields verifies that json:"-" runtime fields survive a
// PUT merge. Such fields are absent from both the marshaled current settings and the
// request body, so zeroJSONSliceAndMapFields must SKIP them; if it zeroed them,
// json.Unmarshal would have nothing to repopulate them with and the runtime value
// would be lost. IncludedScientificNames is json:"-" and NOT in the blocked-field
// map, so only that skip (not restoreBlockedFields) protects it here.
func TestPutPreservesRuntimeOnlyFields(t *testing.T) {
	e := echo.New()
	controller := newMergeController(t, e, func(s *conf.Settings) {
		s.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{
			"turdus merula":      {},
			"erithacus rubecula": {},
		}
	})

	putRawSettings(t, e, controller, `{"birdnet":{"sensitivity":1.3}}`)

	got := controller.Settings.Load()
	assert.InDelta(t, 1.3, got.BirdNET.Sensitivity, 0.0001, "the sent field must change")
	assert.Len(t, got.BirdNET.RangeFilter.IncludedScientificNames, 2,
		"a json:\"-\" runtime map must survive the merge, not be zeroed")
	assert.Contains(t, got.BirdNET.RangeFilter.IncludedScientificNames, "turdus merula")
}

// TestPutReplacesSpeciesConfigMap covers the frontend delete/rename flow: the UI
// edits the species-config Go map, drops the removed/renamed key, and PUTs the
// whole settings object. A naive whole-settings deep-merge would deep-merge the
// map key-by-key and resurrect the dropped entry, so the merge must REPLACE Go map
// fields wholesale when they are present in the body.
func TestPutReplacesSpeciesConfigMap(t *testing.T) {
	seed := func(s *conf.Settings) {
		s.Realtime.Species.Config = map[string]conf.SpeciesConfig{
			"american robin": {Threshold: 0.8, Interval: 30},
			"american crow":  {Threshold: 0.5, Interval: 15},
		}
	}

	t.Run("deleting an entry (omitting its key) removes it", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, seed)
		full := conf.CloneSettings(controller.Settings.Load())
		full.Realtime.Species.Config = map[string]conf.SpeciesConfig{
			"american robin": {Threshold: 0.8, Interval: 30},
		}
		putFullSettings(t, e, controller, full)

		got := controller.Settings.Load().Realtime.Species.Config
		assert.Len(t, got, 1, "the omitted species must be deleted")
		assert.Contains(t, got, "american robin")
		assert.NotContains(t, got, "american crow", "a full-object PUT that omits a species must delete it")
	})

	t.Run("renaming an entry does not leave the old key behind", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, func(s *conf.Settings) {
			s.Realtime.Species.Config = map[string]conf.SpeciesConfig{
				"american robin": {Threshold: 0.8, Interval: 30},
			}
		})
		full := conf.CloneSettings(controller.Settings.Load())
		full.Realtime.Species.Config = map[string]conf.SpeciesConfig{
			"european robin": {Threshold: 0.8, Interval: 30},
		}
		putFullSettings(t, e, controller, full)

		got := controller.Settings.Load().Realtime.Species.Config
		assert.Len(t, got, 1)
		assert.Contains(t, got, "european robin")
		assert.NotContains(t, got, "american robin", "the renamed-away key must not survive")
	})

	t.Run("a species-config entry present in the body is updated, not dropped", func(t *testing.T) {
		e := echo.New()
		controller := newMergeController(t, e, seed)
		full := conf.CloneSettings(controller.Settings.Load())
		robin := full.Realtime.Species.Config["american robin"]
		robin.Threshold = 0.95
		full.Realtime.Species.Config["american robin"] = robin
		putFullSettings(t, e, controller, full)

		got := controller.Settings.Load().Realtime.Species.Config
		require.Len(t, got, 2, "entries kept in the body must remain")
		assert.InDelta(t, 0.95, got["american robin"].Threshold, 0.0001)
		assert.InDelta(t, 0.5, got["american crow"].Threshold, 0.0001)
	})
}

// TestPutRoundTripPreservesSecrets simulates a GET (sanitized) -> edit -> PUT round
// trip, the exact flow the settings page performs. Redacted secrets (SessionSecret,
// passwords, tokens) and the BLANKED BasicAuth client credentials must all survive
// because restoreRedactedSecrets restores the redacted placeholders and
// restoreBlockedFields reverts the blanked/blocked client credentials.
func TestPutRoundTripPreservesSecrets(t *testing.T) {
	e := echo.New()
	controller := newMergeController(t, e, func(s *conf.Settings) {
		s.Security.SessionSecret = "server-session-secret"
		s.Security.BasicAuth.Password = "server-basic-password"
		s.Security.BasicAuth.ClientID = "server-client-id"
		s.Security.BasicAuth.ClientSecret = "server-client-secret"
		s.Realtime.MQTT.Password = "server-mqtt-password"
	})

	// Body the frontend would send back: the sanitized (redacted/blanked) snapshot
	// with one unrelated field changed.
	sanitized := sanitizeSettingsForAPI(controller.Settings.Load())
	sanitized.Main.Name = "renamed-node"
	body, err := json.Marshal(sanitized)
	require.NoError(t, err)

	putRawSettings(t, e, controller, string(body))

	got := controller.Settings.Load()
	assert.Equal(t, "renamed-node", got.Main.Name, "the real edit must be applied")
	assert.Equal(t, "server-session-secret", got.Security.SessionSecret, "redacted session secret must be restored")
	assert.Equal(t, "server-basic-password", got.Security.BasicAuth.Password, "redacted basic-auth password must be restored")
	assert.Equal(t, "server-mqtt-password", got.Realtime.MQTT.Password, "redacted mqtt password must be restored")
	assert.Equal(t, "server-client-id", got.Security.BasicAuth.ClientID, "blanked client id must be reverted, not wiped")
	assert.Equal(t, "server-client-secret", got.Security.BasicAuth.ClientSecret, "blanked client secret must be reverted, not wiped")
}
