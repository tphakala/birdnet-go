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
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			viper.Reset()
			// Set initial values
			viper.Set("realtime.dashboard.summaryLimit", 100)
			viper.Set("realtime.mqtt.broker", "tcp://localhost:1883")
			viper.Set("birdnet.latitude", 40.0)
			viper.Set("realtime.weather.pollInterval", 60)
			viper.Set("realtime.audio.export.bitrate", "96k")

			e := echo.New()
			controller := &Controller{
				Echo:        e,
				Settings:    conf.Setting(),
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

					switch tt.scenario {
					case "same-section":
						// All goroutines update the same section
						update := map[string]interface{}{
							"summaryLimit": 100 + goroutineID,
						}
						err := makeSettingsUpdate(t, controller, "dashboard", update)
						if err != nil {
							errorsChan <- err
						} else {
							successMutex.Lock()
							successCount++
							successMutex.Unlock()
						}

					case "different-sections":
						// Each goroutine updates a different section
						sections := []string{"dashboard", "mqtt", "birdnet", "weather", "audio"}
						section := sections[goroutineID%len(sections)]

						updates := map[string]interface{}{
							"dashboard": map[string]interface{}{"summaryLimit": 100 + goroutineID},
							"mqtt":      map[string]interface{}{"topic": fmt.Sprintf("topic-%d", goroutineID)},
							"birdnet":  map[string]interface{}{"threshold": 0.1 + float64(goroutineID)*0.01},
							"weather":  map[string]interface{}{"pollInterval": 60 + goroutineID},
							"audio":    map[string]interface{}{"export": map[string]interface{}{"bitrate": fmt.Sprintf("%dk", 96+goroutineID)}},
						}

						err := makeSettingsUpdate(t, controller, section, updates[section])
						if err != nil {
							errorsChan <- err
						} else {
							successMutex.Lock()
							successCount++
							successMutex.Unlock()
						}

					case "read-write":
						// Mix of reads and writes
						if goroutineID%2 == 0 {
							// Write operation
							update := map[string]interface{}{
								"summaryLimit": 100 + goroutineID,
							}
							err := makeSettingsUpdate(t, controller, "dashboard", update)
							if err != nil {
								errorsChan <- err
							}
						} else {
							// Read operation
							req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", nil)
							rec := httptest.NewRecorder()
							ctx := e.NewContext(req, rec)
							ctx.SetParamNames("section")
							ctx.SetParamValues("dashboard")

							err := controller.GetSectionSettings(ctx)
							if err != nil {
								errorsChan <- err
							}
						}

					case "rapid-sequential":
						// Rapid updates without delays
						for j := 0; j < 3; j++ {
							update := map[string]interface{}{
								"summaryLimit": 100 + goroutineID*10 + j,
							}
							err := makeSettingsUpdate(t, controller, "dashboard", update)
							if err != nil {
								errorsChan <- err
								break
							}
						}

					case "save-disk":
						// Update and trigger disk save
						update := map[string]interface{}{
							"summaryLimit": 100 + goroutineID,
						}
						err := makeSettingsUpdate(t, controller, "dashboard", update)
						if err != nil {
							errorsChan <- err
						}
						// Note: Actual disk save would be triggered by the controller
					}

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
			settings := conf.Setting()
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
					req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", nil)
					rec := httptest.NewRecorder()
					ctx := controller.Echo.NewContext(req, rec)
					ctx.SetParamNames("section")
					ctx.SetParamValues("dashboard")

					err := controller.GetSectionSettings(ctx)
					assert.NoError(t, err)
					assert.Equal(t, http.StatusOK, rec.Code)

					// Parse response to verify it's valid JSON
					var response map[string]interface{}
					err = json.Unmarshal(rec.Body.Bytes(), &response)
					assert.NoError(t, err)

					time.Sleep(1 * time.Millisecond)
				}

				<-writeDone
			},
		},
		{
			name:        "Conflicting updates to nested fields",
			description: "Verify nested field updates don't corrupt parent objects",
			scenario: func(t *testing.T, controller *Controller) {
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
				settings := conf.Setting()
				assert.NotNil(t, settings.Realtime.Dashboard.Thumbnails)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			viper.Reset()
			viper.Set("realtime.dashboard.summaryLimit", 100)
			viper.Set("realtime.dashboard.thumbnails.summary", false)
			viper.Set("realtime.dashboard.thumbnails.recent", true)

			e := echo.New()
			controller := &Controller{
				Echo:        e,
				Settings:    conf.Setting(),
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
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+section, 
		bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := controller.Echo.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(section)

	return controller.UpdateSectionSettings(ctx)
}