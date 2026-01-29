package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test MQTT topic constant
const testMQTTTopic = "birdnet/detections"

// Test controller constants
const (
	testControlChanBuffer     = 10               // Buffer size for control channel in tests
	testNewYorkLongitude      = -74.0060         // New York City longitude for test data
	testResponseHeaderTimeout = 30 * time.Second // HTTP response header timeout for tests
)

// getTestController creates a test controller with disabled saving
// Note: DisableHTTPKeepAlivesForTesting() is called in TestMain before any tests run
func getTestController(t *testing.T, e *echo.Echo) *Controller {
	t.Helper()
	return &Controller{
		Echo:                e,
		Settings:            getTestSettings(t),
		controlChan:         make(chan string, testControlChanBuffer),
		DisableSaveSettings: true, // Disable saving to disk during tests
	}
}

// getTestSettings returns a valid Settings instance for testing
// This bypasses the global singleton and config file loading
func getTestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	settings := &conf.Settings{}

	// Initialize with valid defaults
	settings.Realtime.Dashboard.SummaryLimit = 100 // Valid range: 10-1000
	settings.Realtime.Dashboard.Thumbnails.Summary = true
	settings.Realtime.Dashboard.Thumbnails.Recent = true
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
	settings.Realtime.Dashboard.Locale = "en"

	// Weather settings
	settings.Realtime.Weather.Provider = "yrno"
	settings.Realtime.Weather.PollInterval = 60

	// MQTT settings
	settings.Realtime.MQTT.Enabled = false
	settings.Realtime.MQTT.Broker = "tcp://localhost:1883"
	settings.Realtime.MQTT.Topic = testMQTTTopic

	// BirdNET settings
	settings.BirdNET.Latitude = 40.7128
	settings.BirdNET.Longitude = testNewYorkLongitude
	settings.BirdNET.Sensitivity = 1.0
	settings.BirdNET.Threshold = 0.8
	settings.BirdNET.RangeFilter.Model = "latest"
	settings.BirdNET.RangeFilter.Threshold = 0.03

	// Audio settings
	settings.Realtime.Audio.Source = "default"
	settings.Realtime.Audio.Export.Enabled = true
	settings.Realtime.Audio.Export.Type = "wav"
	settings.Realtime.Audio.Export.Path = "/clips"
	settings.Realtime.Audio.Export.Bitrate = "192k"

	// Species settings
	settings.Realtime.Species.Include = []string{"American Robin"}
	settings.Realtime.Species.Config = make(map[string]conf.SpeciesConfig)

	// WebServer settings
	settings.WebServer.Port = "8080"
	settings.WebServer.Enabled = true

	// Output settings - SQLite path for prerequisite checks
	// Use temp directory which is guaranteed to exist and be writable
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = os.TempDir() + "/birdnet-test.db"

	// Initialize other maps to prevent nil pointer issues
	settings.Realtime.MQTT.RetrySettings.MaxRetries = 3
	settings.Realtime.MQTT.RetrySettings.InitialDelay = 10
	settings.Realtime.MQTT.RetrySettings.MaxDelay = 300
	settings.Realtime.MQTT.RetrySettings.BackoffMultiplier = 2.0

	return settings
}

// assertControllerError is a helper function that asserts controller error responses
// It handles both cases: when the controller returns an HTTP error or sends an HTTP response
func assertControllerError(t *testing.T, err error, rec *httptest.ResponseRecorder, expectedCode int, expectedMessage string) {
	t.Helper()

	if err == nil {
		// Controller handled the error and sent an HTTP response
		assert.Equal(t, expectedCode, rec.Code, "Expected HTTP status code")

		var response map[string]any
		jsonErr := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, jsonErr, "Response should be valid JSON")

		// Check that the error response contains the expected message (if specified)
		if expectedMessage != "" {
			if message, exists := response["message"]; exists {
				assert.Contains(t, message, expectedMessage, "Error message should contain expected text")
			}
		}
	} else {
		// Controller returned an error directly
		var httpErr *echo.HTTPError
		require.ErrorAs(t, err, &httpErr, "Error should be an echo.HTTPError")
		assert.Equal(t, expectedCode, httpErr.Code, "Error should have expected HTTP code")
		if expectedMessage != "" {
			assert.Contains(t, httpErr.Message, expectedMessage, "Error message should contain expected text")
		}
	}
}

// createTestHTTPClient creates an HTTP client optimized for tests to prevent goroutine leaks
func createTestHTTPClient(timeout time.Duration) *http.Client {
	// Create custom transport that disables keep-alive to prevent persistent connection goroutines
	transport := &http.Transport{
		DisableKeepAlives:     true, // This prevents persistent connections and their goroutines
		IdleConnTimeout:       1 * time.Second,
		MaxIdleConns:          0, // Disable connection pooling
		MaxIdleConnsPerHost:   0,
		DisableCompression:    false, // Keep compression for realistic testing
		ForceAttemptHTTP2:     false, // Disable HTTP/2 for simplicity in tests
		ResponseHeaderTimeout: timeout,
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// DisableHTTPKeepAlivesForTesting configures the default HTTP client transport
// to prevent goroutine leaks from persistent connections during testing
func DisableHTTPKeepAlivesForTesting() {
	// Override the default transport to prevent goroutine leaks in ALL HTTP clients
	http.DefaultTransport = &http.Transport{
		DisableKeepAlives:     true,
		IdleConnTimeout:       1 * time.Second,
		MaxIdleConns:          0,
		MaxIdleConnsPerHost:   0,
		DisableCompression:    false,
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: testResponseHeaderTimeout,
	}
}
