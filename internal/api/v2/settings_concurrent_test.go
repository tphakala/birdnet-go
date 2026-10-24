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
)

const (
	// concurrentSummaryLimitBase is the base for every summaryLimit that the
	// TestConcurrentUpdates scenarios send. It must differ from the baseline
	// summaryLimit that getTestSettings sets, so a rejected update leaves a value
	// outside the range the same-section check accepts, and base plus the largest
	// offset a scenario adds must stay inside the validated 10-1000 range, since
	// an out-of-range value is reset to 10 by validation
	// (internal/conf/validate_realtime.go:170-172) instead of being rejected.
	concurrentSummaryLimitBase = 200

	// readDuringWriteSummaryLimit is the summaryLimit written while readers run
	// in the "Read during write" race scenario. It differs from the baseline.
	readDuringWriteSummaryLimit = 999
)

// TestConcurrentUpdates verifies the system handles concurrent updates safely
func TestConcurrentUpdates(t *testing.T) {
	t.Parallel()
	t.Attr("component", "settings")
	t.Attr("type", "concurrent")

	tests := []struct {
		name        string
		concurrency int
		scenario    string
	}{
		{
			name:        "Multiple updates to same section",
			concurrency: 10,
			scenario:    "same-section",
		},
		{
			name:        "Updates to different sections",
			concurrency: 5,
			scenario:    "different-sections",
		},
		{
			name:        "Mixed reads and writes",
			concurrency: 10,
			scenario:    "read-write",
		},
		{
			name:        "Rapid sequential updates",
			concurrency: 20,
			scenario:    "rapid-sequential",
		},
		{
			name:        "Concurrent saves to disk",
			concurrency: 5,
			scenario:    "save-disk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Initialize test settings with known values

			e := echo.New()
			controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, 100), DisableSaveSettings: true}
			controller.Settings.Store(getTestSettings(t))

			if tt.scenario == "same-section" {
				// The final-value check below only means something if the baseline
				// lies outside the range of values the updates send.
				initial := controller.Settings.Load().Realtime.Dashboard.SummaryLimit
				require.False(t, initial >= concurrentSummaryLimitBase && initial < concurrentSummaryLimitBase+tt.concurrency,
					"baseline summaryLimit %d must lie outside the range the updates send", initial)
			}

			var wg sync.WaitGroup
			errorsChan := make(chan error, tt.concurrency)

			// Use Go 1.25 WaitGroup.Go() for cleaner goroutine management
			// Note: synctest.Test() can cause deadlocks with background goroutines that use time.Sleep
			// so we use regular concurrent testing with WaitGroup.Go() improvements
			for i := range tt.concurrency {
				goroutineID := i
				// Use WaitGroup.Go() for automatic Add/Done management (Go 1.25)
				// This eliminates the need for manual wg.Add(1) and defer wg.Done()
				wg.Go(func() {
					// Errors are reported through errorsChan, so the return value is not needed.
					_ = runConcurrentScenario(t, tt.scenario, goroutineID, controller, errorsChan)
				})
			}
			wg.Wait()
			close(errorsChan)

			// Check for any errors
			var errors []error
			for err := range errorsChan {
				errors = append(errors, err)
			}

			assert.Empty(t, errors, "Concurrent operations should not produce errors")

			// Verify final state is consistent
			settings := controller.Settings.Load()
			assert.NotNil(t, settings)

			// For same-section updates, verify one of the values "won"
			if tt.scenario == "same-section" {
				limit := settings.Realtime.Dashboard.SummaryLimit
				assert.GreaterOrEqual(t, limit, concurrentSummaryLimitBase,
					"Final value should be one of the concurrent updates")
				assert.Less(t, limit, concurrentSummaryLimitBase+tt.concurrency,
					"Final value should be one of the concurrent updates")
			}
		})
	}
}

// runConcurrentScenario executes a specific concurrent test scenario
func runConcurrentScenario(t *testing.T, scenario string, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	switch scenario {
	case "same-section":
		return runSameSectionScenario(t, goroutineID, controller, errorsChan)
	case "different-sections":
		return runDifferentSectionsScenario(t, goroutineID, controller, errorsChan)
	case "read-write":
		return runReadWriteScenario(t, goroutineID, controller, errorsChan)
	case "rapid-sequential":
		return runRapidSequentialScenario(t, goroutineID, controller, errorsChan)
	case "save-disk":
		return runSaveLogicScenario(t, goroutineID, controller, errorsChan)
	default:
		err := fmt.Errorf("unknown scenario: %s", scenario)
		errorsChan <- err
		return err
	}
}

// runSameSectionScenario handles concurrent updates to the same section
func runSameSectionScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	update := map[string]any{
		"summaryLimit": concurrentSummaryLimitBase + goroutineID,
	}
	err := makeSettingsUpdate(t, controller, "dashboard", update)
	if err != nil {
		errorsChan <- err
	}
	return err
}

// runDifferentSectionsScenario handles updates to different sections
func runDifferentSectionsScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	sections := []string{"dashboard", "mqtt", "birdnet", "weather", "audio"}
	section := sections[goroutineID%len(sections)]

	updates := map[string]any{
		"dashboard": map[string]any{"summaryLimit": concurrentSummaryLimitBase + goroutineID},
		"mqtt":      map[string]any{"topic": fmt.Sprintf("topic-%d", goroutineID)},
		"birdnet":   map[string]any{"threshold": 0.1 + float64(goroutineID)*0.01},
		"weather":   map[string]any{"pollInterval": 60 + goroutineID},
		"audio":     map[string]any{"export": map[string]any{"bitrate": fmt.Sprintf("%dk", 96+goroutineID)}},
	}

	err := makeSettingsUpdate(t, controller, section, updates[section])
	if err != nil {
		errorsChan <- err
	}
	return err
}

// runReadWriteScenario handles mixed read and write operations
func runReadWriteScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	if goroutineID%2 == 0 {
		// Write operation
		update := map[string]any{
			"summaryLimit": concurrentSummaryLimitBase + goroutineID,
		}
		err := makeSettingsUpdate(t, controller, "dashboard", update)
		if err != nil {
			errorsChan <- err
		}
		return err
	}
	// Read operation
	err := readSectionSettings(controller, "dashboard")
	if err != nil {
		errorsChan <- err
	}
	return err
}

// runRapidSequentialScenario handles rapid sequential updates
func runRapidSequentialScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	for j := range 3 {
		update := map[string]any{
			"summaryLimit": concurrentSummaryLimitBase + goroutineID*10 + j,
		}
		err := makeSettingsUpdate(t, controller, "dashboard", update)
		if err != nil {
			errorsChan <- err
			return err
		}
	}
	return nil
}

// runSaveLogicScenario tests save logic without actual disk I/O (DisableSaveSettings prevents disk writes)
func runSaveLogicScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	update := map[string]any{
		"summaryLimit": concurrentSummaryLimitBase + goroutineID,
	}
	err := makeSettingsUpdate(t, controller, "dashboard", update)
	if err != nil {
		errorsChan <- err
	}
	return err
}

// TestRaceConditionScenarios tests specific race condition scenarios
func TestRaceConditionScenarios(t *testing.T) {
	t.Parallel()
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
			t.Parallel()

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
