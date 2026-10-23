// Package api v2 settings concurrent tests - leverages Go 1.25 features:
// - sync.WaitGroup.Go() for cleaner goroutine management
// - T.Attr() for enhanced test metadata
//
// LLM GUIDANCE for updating concurrent tests:
//  1. Use sync.WaitGroup.Go(func()) instead of wg.Add(1) + go func() + defer wg.Done()
//     Example: wg.Go(func() { /* work */ })
//  2. Add test metadata with T.Attr("component", "name") and T.Attr("type", "test-type")
//  3. testing/synctest.Test() creates deterministic "bubbles" but can deadlock with background
//     goroutines that use time.Sleep() - avoid using it with code that spawns such goroutines
//  4. For simple concurrent tests, prefer regular WaitGroup.Go() over synctest
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// readDuringWriteSummaryLimit is the summaryLimit written while readers run in
// the "Read during write" race scenario. It differs from the baseline that
// getTestSettings sets.
const readDuringWriteSummaryLimit = 999

// TestConcurrentSectionPatchesAllLand sends one PATCH per section at the same
// time and requires every one to succeed and to be visible afterwards. The
// handler holds settingsMutex (settings.go:475) for its whole read-modify-write,
// so a writer that skipped the lock could publish a clone taken before another
// writer's change. Each section gets its own value, so a lost update shows as
// that section still holding its baseline.
//
// Not parallel: see the note in settings_blocked_fields_test.go.
func TestConcurrentSectionPatchesAllLand(t *testing.T) {
	t.Attr("component", "settings")
	t.Attr("type", "concurrent")

	patches := []struct {
		section string
		body    map[string]any
		landed  func(t *testing.T, s *conf.Settings)
	}{
		{
			section: "dashboard",
			body:    map[string]any{"summaryLimit": 200},
			landed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, 200, s.Realtime.Dashboard.SummaryLimit)
			},
		},
		{
			section: "mqtt",
			body:    map[string]any{"topic": "concurrent/topic"},
			landed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "concurrent/topic", s.Realtime.MQTT.Topic)
			},
		},
		{
			section: "birdnet",
			body:    map[string]any{"threshold": 0.55},
			landed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.InDelta(t, 0.55, s.BirdNET.Threshold, 0.0001)
			},
		},
		{
			section: "weather",
			body:    map[string]any{"pollInterval": 90},
			landed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, 90, s.Realtime.Weather.PollInterval)
			},
		},
		{
			section: "audio",
			body:    map[string]any{"export": map[string]any{"bitrate": "96k"}},
			landed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "96k", s.Realtime.Audio.Export.Bitrate)
			},
		},
	}

	e := echo.New()
	controller := getTestController(t, e)
	baseline := controller.Settings.Load()

	// Workers write their own slot and nothing else; assertions run on the test
	// goroutine after Wait.
	results := make([]error, len(patches))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range patches {
		wg.Go(func() {
			<-start
			results[i] = makeSettingsUpdate(t, controller, patches[i].section, patches[i].body)
		})
	}
	close(start)
	wg.Wait()

	for i, p := range patches {
		require.NoError(t, results[i], "PATCH %s must succeed", p.section)
	}
	final := controller.Settings.Load()
	require.NotSame(t, baseline, final, "the patches must publish a new snapshot")
	for _, p := range patches {
		p.landed(t, final)
	}
}

// TestRaceConditionScenarios tests specific race condition scenarios
func TestRaceConditionScenarios(t *testing.T) {
	t.Attr("component", "settings")
	t.Attr("type", "race-detection")

	tests := []struct {
		name        string
		description string
		scenario    func(t *testing.T, controller *Controller)
	}{
		{
			name:        "Read during write",
			description: "Verify reads during writes return consistent data",
			scenario: func(t *testing.T, controller *Controller) {
				t.Helper()
				// Cannot use synctest.Test here: handleSettingsChanges spawns a
				// background goroutine with time.Sleep that outlives the test
				// function, causing synctest's deadlock detector to fire.
				const readers = 10
				var wg sync.WaitGroup
				// Workers report through errs; assertions run on the test goroutine.
				errs := make(chan error, readers+1)

				wg.Go(func() {
					update := map[string]any{
						"summaryLimit": readDuringWriteSummaryLimit,
					}
					errs <- makeSettingsUpdate(t, controller, "dashboard", update)
				})

				for range readers {
					wg.Go(func() {
						errs <- readSectionSettings(controller, "dashboard")
					})
				}

				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}

				// The write must have landed, not just returned without a handler error.
				assert.Equal(t, readDuringWriteSummaryLimit, controller.Settings.Load().Realtime.Dashboard.SummaryLimit)
			},
		},
		{
			name:        "Conflicting updates to nested fields",
			description: "Verify nested field updates don't corrupt parent objects",
			scenario: func(t *testing.T, controller *Controller) {
				t.Helper()
				// Start with both flags false so a rejected write, or a merge that
				// resets a sibling field, leaves a flag false after both writes set true.
				baseline := getTestSettings(t)
				baseline.Realtime.Dashboard.Thumbnails.Summary = false
				baseline.Realtime.Dashboard.Thumbnails.Recent = false
				controller.Settings.Store(baseline)
				wantProvider := baseline.Realtime.Dashboard.Thumbnails.ImageProvider

				const writers = 2
				var wg sync.WaitGroup
				// Workers report through errs; assertions run on the test goroutine.
				errs := make(chan error, writers)

				wg.Go(func() {
					update := map[string]any{
						"thumbnails": map[string]any{
							"summary": true,
						},
					}
					errs <- makeSettingsUpdate(t, controller, "dashboard", update)
				})

				wg.Go(func() {
					update := map[string]any{
						"thumbnails": map[string]any{
							"recent": true,
						},
					}
					errs <- makeSettingsUpdate(t, controller, "dashboard", update)
				})

				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}

				// Both writes must have landed and the untouched sibling must be intact
				thumbnails := controller.Settings.Load().Realtime.Dashboard.Thumbnails
				assert.True(t, thumbnails.Summary)
				assert.True(t, thumbnails.Recent)
				assert.Equal(t, wantProvider, thumbnails.ImageProvider)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, 100), DisableSaveSettings: true}
			controller.Settings.Store(getTestSettings(t))

			tt.scenario(t, controller)
		})
	}
}

// makeSettingsUpdate sends a PATCH to the section and returns an error when the
// handler returns one or the response status is not 200. HandleError writes a
// 4xx/5xx response and returns nil (internal/api/v2/apicore/errors.go:131,
// 182-184), so the status must be checked as well.
func makeSettingsUpdate(t *testing.T, controller *Controller, section string, update any) error {
	t.Helper()

	body, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed to marshal update: %w", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+section,
		bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := controller.Echo.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(section)

	if err := controller.UpdateSectionSettings(ctx); err != nil {
		return err
	}
	if rec.Code != http.StatusOK {
		return fmt.Errorf("PATCH %s: status %d: %s", section, rec.Code, rec.Body.String())
	}
	return nil
}

// readSectionSettings sends a GET for the section and returns an error when the
// handler returns one, the response status is not 200, or the body is not JSON.
func readSectionSettings(controller *Controller, section string) error {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/"+section, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := controller.Echo.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(section)

	if err := controller.GetSectionSettings(ctx); err != nil {
		return err
	}
	if rec.Code != http.StatusOK {
		return fmt.Errorf("GET %s: status %d: %s", section, rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		return fmt.Errorf("GET %s: decode response: %w", section, err)
	}
	return nil
}
