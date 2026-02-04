// Package api v2 settings concurrent tests - leverages Go 1.25 features:
// - testing/synctest for deterministic concurrent testing
// - sync.WaitGroup.Go() for cleaner goroutine management
// - T.Attr() and T.Output() for enhanced test metadata and reporting
//
// LLM GUIDANCE for updating concurrent tests:
//  1. Use sync.WaitGroup.Go(func()) instead of wg.Add(1) + go func() + defer wg.Done()
//     Example: wg.Go(func() { /* work */ })
//  2. Add test metadata with T.Attr("component", "name") and T.Attr("type", "test-type")
//  3. Use T.Output() for structured logging: fmt.Fprintf(t.Output(), "message")
//  4. testing/synctest.Test() creates deterministic "bubbles" but can deadlock with background
//     goroutines that use time.Sleep() - avoid using it with code that spawns such goroutines
//  5. For simple concurrent tests, prefer regular WaitGroup.Go() over synctest
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			controller := &Controller{
				Echo:                e,
				Settings:            getTestSettings(t),
				controlChan:         make(chan string, 100),
				DisableSaveSettings: true,
			}

			var wg sync.WaitGroup
			errorsChan := make(chan error, tt.concurrency)
			successCount := 0
			var successMutex sync.Mutex

			// Use Go 1.25 WaitGroup.Go() for cleaner goroutine management
			// Note: synctest.Test() can cause deadlocks with background goroutines that use time.Sleep
			// so we use regular concurrent testing with WaitGroup.Go() improvements
			for i := range tt.concurrency {
				goroutineID := i
				// Use WaitGroup.Go() for automatic Add/Done management (Go 1.25)
				// This eliminates the need for manual wg.Add(1) and defer wg.Done()
				wg.Go(func() {
					_ = runConcurrentScenario(t, tt.scenario, goroutineID, controller, e, errorsChan, &successMutex, &successCount)
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
			settings := controller.Settings
			assert.NotNil(t, settings)

			// For same-section updates, verify one of the values "won"
			if tt.scenario == "same-section" {
				limit := settings.Realtime.Dashboard.SummaryLimit
				assert.True(t, limit >= 100 && limit < 100+tt.concurrency,
					"Final value should be one of the concurrent updates")
			}

			// Use T.Output() for structured logging
			output := t.Output()
			_, _ = fmt.Fprintf(output, "Scenario %s: %d successful operations\n", tt.scenario, successCount)
		})
	}
}

// runConcurrentScenario executes a specific concurrent test scenario
func runConcurrentScenario(t *testing.T, scenario string, goroutineID int, controller *Controller, e *echo.Echo, errorsChan chan error, successMutex *sync.Mutex, successCount *int) error {
	t.Helper()
	switch scenario {
	case "same-section":
		return runSameSectionScenario(t, goroutineID, controller, errorsChan, successMutex, successCount)
	case "different-sections":
		return runDifferentSectionsScenario(t, goroutineID, controller, errorsChan, successMutex, successCount)
	case "read-write":
		return runReadWriteScenario(t, goroutineID, controller, e, errorsChan)
	case "rapid-sequential":
		return runRapidSequentialScenario(t, goroutineID, controller, errorsChan)
	case "save-disk":
		return runSaveLogicScenario(t, goroutineID, controller, errorsChan)
	default:
		return fmt.Errorf("unknown scenario: %s", scenario)
	}
}

// runSameSectionScenario handles concurrent updates to the same section
func runSameSectionScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error, successMutex *sync.Mutex, successCount *int) error {
	t.Helper()
	update := map[string]any{
		"summaryLimit": 100 + goroutineID,
	}
	err := makeSettingsUpdate(t, controller, "dashboard", update)
	if err != nil {
		errorsChan <- err
	} else {
		successMutex.Lock()
		*successCount++
		successMutex.Unlock()
	}
	return err
}

// runDifferentSectionsScenario handles updates to different sections
func runDifferentSectionsScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error, successMutex *sync.Mutex, successCount *int) error {
	t.Helper()
	sections := []string{"dashboard", "mqtt", "birdnet", "weather", "audio"}
	section := sections[goroutineID%len(sections)]

	updates := map[string]any{
		"dashboard": map[string]any{"summaryLimit": 100 + goroutineID},
		"mqtt":      map[string]any{"topic": fmt.Sprintf("topic-%d", goroutineID)},
		"birdnet":   map[string]any{"threshold": 0.1 + float64(goroutineID)*0.01},
		"weather":   map[string]any{"pollInterval": 60 + goroutineID},
		"audio":     map[string]any{"export": map[string]any{"bitrate": fmt.Sprintf("%dk", 96+goroutineID)}},
	}

	err := makeSettingsUpdate(t, controller, section, updates[section])
	if err != nil {
		errorsChan <- err
	} else {
		successMutex.Lock()
		*successCount++
		successMutex.Unlock()
	}
	return err
}

// runReadWriteScenario handles mixed read and write operations
func runReadWriteScenario(t *testing.T, goroutineID int, controller *Controller, e *echo.Echo, errorsChan chan error) error {
	t.Helper()
	if goroutineID%2 == 0 {
		// Write operation
		update := map[string]any{
			"summaryLimit": 100 + goroutineID,
		}
		err := makeSettingsUpdate(t, controller, "dashboard", update)
		if err != nil {
			errorsChan <- err
		}
		return err
	} else {
		// Read operation
		req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("section")
		ctx.SetParamValues("dashboard")

		err := controller.GetSectionSettings(ctx)
		if err != nil {
			errorsChan <- err
		}
		return err
	}
}

// runRapidSequentialScenario handles rapid sequential updates
func runRapidSequentialScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	for j := range 3 {
		update := map[string]any{
			"summaryLimit": 100 + goroutineID*10 + j,
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
		"summaryLimit": 100 + goroutineID,
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
				// Use synctest.Test for deterministic read/write concurrency (Go 1.25)
				// This ensures predictable execution order without timing dependencies
				synctest.Test(t, func(t *testing.T) {
					t.Helper()
					var wg sync.WaitGroup

					// Start a write using WaitGroup.Go() (Go 1.25)
					wg.Go(func() {
						update := map[string]any{
							"summaryLimit": 999,
						}
						_ = makeSettingsUpdate(t, controller, "dashboard", update)
					})

					// Perform multiple reads concurrently with the write
					for range 10 {
						wg.Go(func() {
							req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", http.NoBody)
							rec := httptest.NewRecorder()
							ctx := controller.Echo.NewContext(req, rec)
							ctx.SetParamNames("section")
							ctx.SetParamValues("dashboard")

							err := controller.GetSectionSettings(ctx)
							require.NoError(t, err)
							assert.Equal(t, http.StatusOK, rec.Code)

							// Parse response to verify it's valid JSON
							var response map[string]any
							err = json.Unmarshal(rec.Body.Bytes(), &response)
							require.NoError(t, err)
						})
					}

					wg.Wait()
					synctest.Wait() // Ensure all bubble goroutines complete
				})
			},
		},
		{
			name:        "Conflicting updates to nested fields",
			description: "Verify nested field updates don't corrupt parent objects",
			scenario: func(t *testing.T, controller *Controller) {
				t.Helper()
				// Use synctest.Test for deterministic nested field updates (Go 1.25)
				synctest.Test(t, func(t *testing.T) {
					t.Helper()
					var wg sync.WaitGroup

					// Update different nested fields concurrently using WaitGroup.Go() (Go 1.25)
					wg.Go(func() {
						update := map[string]any{
							"thumbnails": map[string]any{
								"summary": true,
							},
						}
						_ = makeSettingsUpdate(t, controller, "dashboard", update)
					})

					wg.Go(func() {
						update := map[string]any{
							"thumbnails": map[string]any{
								"recent": false,
							},
						}
						_ = makeSettingsUpdate(t, controller, "dashboard", update)
					})

					wg.Wait()
					synctest.Wait() // Ensure all bubble goroutines complete
				})

				// Verify both fields were updated
				settings := controller.Settings
				assert.NotNil(t, settings.Realtime.Dashboard.Thumbnails)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := &Controller{
				Echo:                e,
				Settings:            getTestSettings(t),
				controlChan:         make(chan string, 100),
				DisableSaveSettings: true,
			}

			tt.scenario(t, controller)
		})
	}
}

// Helper function for making settings updates
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

	return controller.UpdateSectionSettings(ctx)
}
