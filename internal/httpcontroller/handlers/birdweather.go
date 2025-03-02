// birdweather.go provides HTTP handlers for BirdWeather-related functionality
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// BirdWeather test stage constants
const (
	stageAPIConnectivity  = "API Connectivity"
	stageAuthentication   = "Authentication"
	stageSoundscapeUpload = "Soundscape Upload"
	stageDetectionPost    = "Detection Post"
)

// TestBirdWeather handles requests to test BirdWeather connectivity and functionality
// API: GET/POST /api/v1/birdweather/test
func (h *Handlers) TestBirdWeather(c echo.Context) error {
	// Define a struct for the test configuration
	type TestConfig struct {
		Enabled          bool    `json:"enabled"`
		ID               string  `json:"id"`
		Threshold        float64 `json:"threshold"`
		LocationAccuracy float64 `json:"locationAccuracy"`
		Debug            bool    `json:"debug"`
	}

	var testConfig TestConfig
	var settings *conf.Settings

	// If this is a POST request, use the provided test configuration
	if c.Request().Method == "POST" {
		if err := c.Bind(&testConfig); err != nil {
			return h.NewHandlerError(err, "Invalid test configuration", http.StatusBadRequest)
		}

		// Create temporary settings for the test
		settings = &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Latitude:  45.0, // Default test values for location
				Longitude: -75.0,
			},
			Realtime: conf.RealtimeSettings{
				Birdweather: conf.BirdweatherSettings{
					Enabled:          testConfig.Enabled,
					ID:               testConfig.ID,
					Threshold:        testConfig.Threshold,
					LocationAccuracy: testConfig.LocationAccuracy,
					Debug:            testConfig.Debug,
				},
			},
		}
	} else {
		// For GET requests, use the current settings
		settings = h.Settings
	}

	// Check if BirdWeather is enabled
	if !settings.Realtime.Birdweather.Enabled {
		return h.NewHandlerError(
			nil,
			"BirdWeather is not enabled in settings",
			http.StatusBadRequest,
		)
	}

	// Check if the station ID is provided
	if settings.Realtime.Birdweather.ID == "" {
		return h.NewHandlerError(
			nil,
			"BirdWeather station ID is not configured",
			http.StatusBadRequest,
		)
	}

	// Create a temporary BirdWeather client for testing
	bwClient, err := birdweather.New(settings)
	if err != nil {
		return h.NewHandlerError(err, "Failed to create BirdWeather client", http.StatusInternalServerError)
	}

	// Create context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up streaming response
	c.Response().Header().Set("Content-Type", "application/x-ndjson")
	c.Response().WriteHeader(http.StatusOK)

	// Create a channel to receive test results
	resultChan := make(chan birdweather.TestResult)

	// Start test in a goroutine
	go func() {
		defer close(resultChan)
		bwClient.TestConnection(ctx, resultChan)
	}()

	// Stream results to client
	enc := json.NewEncoder(c.Response())
	for result := range resultChan {
		// Modify the result enhancement to handle progress messages
		if !result.Success {
			hint := generateBirdWeatherTroubleshootingHint(&result)
			if hint != "" {
				result.Message = fmt.Sprintf("%s\n\n%s\n\n%s",
					result.Message,
					result.Error,
					hint)
				result.Error = ""
			}
		} else {
			// Explicitly mark progress messages
			result.IsProgress = strings.Contains(strings.ToLower(result.Message), "running") ||
				strings.Contains(strings.ToLower(result.Message), "testing") ||
				strings.Contains(strings.ToLower(result.Message), "establishing")
		}

		if err := enc.Encode(result); err != nil {
			// If we can't write to the response, client probably disconnected
			return nil
		}
		c.Response().Flush()
	}

	// Clean up
	bwClient.Close()

	return nil
}

// generateBirdWeatherTroubleshootingHint provides context-specific troubleshooting suggestions
func generateBirdWeatherTroubleshootingHint(result *birdweather.TestResult) string {
	if result.Success {
		return "" // No hint needed for successful tests
	}

	// Return troubleshooting hints based on the stage and error message
	switch result.Stage {
	case stageAPIConnectivity:
		if strings.Contains(result.Error, "timeout") ||
			strings.Contains(result.Error, "i/o timeout") {
			return "Check your internet connection and ensure that app.birdweather.com is accessible from your network. Consider checking for any firewall rules that might be blocking outbound connections."
		}
		if strings.Contains(result.Error, "no such host") ||
			strings.Contains(result.Error, "DNS") {
			return "Could not resolve the BirdWeather API hostname. Check your DNS configuration and internet connectivity."
		}
		return "Unable to connect to the BirdWeather API. Verify your internet connection and network settings."

	case stageAuthentication:
		if strings.Contains(result.Error, "status code 401") ||
			strings.Contains(result.Error, "status code 403") ||
			strings.Contains(result.Error, "invalid station ID") {
			return "Authentication failed. Please verify that your BirdWeather Station ID is correct and that your station is registered and active on the BirdWeather platform."
		}
		return "Failed to authenticate with BirdWeather. Check your BirdWeather token and ensure your station is properly registered on the BirdWeather platform."

	case stageSoundscapeUpload:
		return "Failed to upload the test soundscape. This could be due to network issues, authentication problems, or the BirdWeather service might be experiencing issues. Try again later or check your BirdWeather station settings."

	case stageDetectionPost:
		return "Failed to post the test detection. Make sure your BirdWeather station is properly configured to accept detection uploads."

	default:
		return "Something went wrong with the BirdWeather test. Check your connection settings and try again."
	}
}
