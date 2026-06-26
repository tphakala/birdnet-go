package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test MQTT topic constant
const testMQTTTopic = "birdnet/detections"

// Test controller constants
const (
	testControlChanBuffer = 10       // Buffer size for control channel in tests
	testNewYorkLongitude  = -74.0060 // New York City longitude for test data
	// testFailFastTimeout is a short timeout injected into deliberate-failure
	// waits (unreachable ntfy host, audio file that never appears) so those
	// tests reach the expected timeout state quickly instead of waiting the full
	// production defaults. It is intentionally well above any local/CI dial or
	// scheduling latency while staying far below the production timeouts.
	testFailFastTimeout = 200 * time.Millisecond
)

// getTestController creates a test controller with disabled saving
// Note: apitest.DisableHTTPKeepAlivesForTesting() is called in TestMain before any tests run
func getTestController(t *testing.T, e *echo.Echo) *Controller {
	t.Helper()
	c := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	c.Settings.Store(getTestSettings(t))
	return c
}

// getTestSettings returns a valid Settings instance for testing
// This bypasses the global singleton and config file loading
func getTestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	settings := &conf.Settings{}

	// Initialize with valid defaults
	settings.Realtime.Interval = 15                // Must be positive after hardening
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
	settings.BirdNET.Locale = "en"
	settings.BirdNET.RangeFilter.Model = "latest"
	settings.BirdNET.RangeFilter.Threshold = 0.03

	// Audio settings
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
		Name:   "Test Sound Card",
		Device: "default",
	}}
	settings.Realtime.Audio.Export.Enabled = true
	settings.Realtime.Audio.Export.Type = "wav"
	settings.Realtime.Audio.Export.Path = "clips"
	settings.Realtime.Audio.Export.Bitrate = "192k"
	settings.Realtime.Audio.Export.Length = 15

	// Species settings
	settings.Realtime.Species.Include = []string{"American Robin"}
	settings.Realtime.Species.Config = make(map[string]conf.SpeciesConfig)

	// WebServer settings
	settings.WebServer.Port = "8080"
	settings.WebServer.Enabled = true
	settings.WebServer.LiveStream.BitRate = 128
	settings.WebServer.LiveStream.SegmentLength = 5

	// Security settings - session duration must be positive
	settings.Security.SessionDuration = 168 * time.Hour // 7 days

	// Output settings - SQLite path for prerequisite checks
	// Use t.TempDir() for test-isolated, auto-cleaned directory
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet-test.db")

	// Initialize other maps to prevent nil pointer issues
	settings.Realtime.MQTT.RetrySettings.MaxRetries = 3
	settings.Realtime.MQTT.RetrySettings.InitialDelay = 10
	settings.Realtime.MQTT.RetrySettings.MaxDelay = 300
	settings.Realtime.MQTT.RetrySettings.BackoffMultiplier = 2.0

	return settings
}
