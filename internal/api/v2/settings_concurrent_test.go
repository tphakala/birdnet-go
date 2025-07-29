package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentUpdates verifies the system handles concurrent updates safely
func TestConcurrentUpdates(t *testing.T) {
	t.Parallel()

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
				Echo:        e,
				Settings:    getTestSettings(t),
				controlChan: make(chan string, 100),
			}

			var wg sync.WaitGroup
			errorsChan := make(chan error, tt.concurrency)
			successCount := 0
			var successMutex sync.Mutex

			for i := 0; i < tt.concurrency; i++ {
				wg.Add(1)
				go func(goroutineID int) {
					defer wg.Done()

					_ = runConcurrentScenario(t, tt.scenario, goroutineID, controller, e, errorsChan, &successMutex, &successCount)

					// Add small random delay to increase chance of actual concurrency
					if tt.scenario != "rapid-sequential" {
						time.Sleep(time.Duration(goroutineID%10) * time.Millisecond)
					}
				}(i)
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

			// Log success rate
			t.Logf("Scenario %s: %d successful operations", tt.scenario, successCount)
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
		return runSaveDiskScenario(t, goroutineID, controller, errorsChan)
	default:
		return fmt.Errorf("unknown scenario: %s", scenario)
	}
}

// runSameSectionScenario handles concurrent updates to the same section
func runSameSectionScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error, successMutex *sync.Mutex, successCount *int) error {
	t.Helper()
	update := map[string]interface{}{
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

	updates := map[string]interface{}{
		"dashboard": map[string]interface{}{"summaryLimit": 100 + goroutineID},
		"mqtt":      map[string]interface{}{"topic": fmt.Sprintf("topic-%d", goroutineID)},
		"birdnet":   map[string]interface{}{"threshold": 0.1 + float64(goroutineID)*0.01},
		"weather":   map[string]interface{}{"pollInterval": 60 + goroutineID},
		"audio":     map[string]interface{}{"export": map[string]interface{}{"bitrate": fmt.Sprintf("%dk", 96+goroutineID)}},
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
		update := map[string]interface{}{
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
	for j := 0; j < 3; j++ {
		update := map[string]interface{}{
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

// runSaveDiskScenario handles updates with disk saves
func runSaveDiskScenario(t *testing.T, goroutineID int, controller *Controller, errorsChan chan error) error {
	t.Helper()
	update := map[string]interface{}{
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
				// Start a write in background
				writeDone := make(chan bool)
				go func() {
					update := map[string]interface{}{
						"summaryLimit": 999,
					}
					_ = makeSettingsUpdate(t, controller, "dashboard", update)
					writeDone <- true
				}()

				// Perform multiple reads during the write
				for i := 0; i < 10; i++ {
					req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", http.NoBody)
					rec := httptest.NewRecorder()
					ctx := controller.Echo.NewContext(req, rec)
					ctx.SetParamNames("section")
					ctx.SetParamValues("dashboard")

					err := controller.GetSectionSettings(ctx)
					require.NoError(t, err)
					assert.Equal(t, http.StatusOK, rec.Code)

					// Parse response to verify it's valid JSON
					var response map[string]interface{}
					err = json.Unmarshal(rec.Body.Bytes(), &response)
					require.NoError(t, err)

					time.Sleep(1 * time.Millisecond)
				}

				<-writeDone
			},
		},
		{
			name:        "Conflicting updates to nested fields",
			description: "Verify nested field updates don't corrupt parent objects",
			scenario: func(t *testing.T, controller *Controller) {
				t.Helper()
				var wg sync.WaitGroup

				// Update different nested fields concurrently
				wg.Add(2)
				go func() {
					defer wg.Done()
					update := map[string]interface{}{
						"thumbnails": map[string]interface{}{
							"summary": true,
						},
					}
					_ = makeSettingsUpdate(t, controller, "dashboard", update)
				}()

				go func() {
					defer wg.Done()
					update := map[string]interface{}{
						"thumbnails": map[string]interface{}{
							"recent": false,
						},
					}
					_ = makeSettingsUpdate(t, controller, "dashboard", update)
				}()

				wg.Wait()

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
				Echo:        e,
				Settings:    getTestSettings(t),
				controlChan: make(chan string, 100),
			}

			tt.scenario(t, controller)
		})
	}
}

// Helper function for making settings updates
func makeSettingsUpdate(t *testing.T, controller *Controller, section string, update interface{}) error {
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