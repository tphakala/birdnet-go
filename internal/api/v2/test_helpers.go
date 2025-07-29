package api

import (
	"log"
	"os"
	
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// getTestController creates a test controller with disabled saving
func getTestController(e *echo.Echo) *Controller {
	return &Controller{
		Echo:                e,
		Settings:            getTestSettings(),
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true, // Disable saving to disk during tests
		logger:              log.New(os.Stderr, "TEST: ", log.LstdFlags), // Add logger for tests
	}
}

// getTestSettings returns a valid Settings instance for testing
// This bypasses the global singleton and config file loading
func getTestSettings() *conf.Settings {
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
	settings.Realtime.MQTT.Topic = "birdnet/detections"
	
	// BirdNET settings
	settings.BirdNET.Latitude = 40.7128
	settings.BirdNET.Longitude = -74.0060
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
	
	// Initialize other maps to prevent nil pointer issues
	settings.Realtime.MQTT.RetrySettings.MaxRetries = 3
	settings.Realtime.MQTT.RetrySettings.InitialDelay = 10
	settings.Realtime.MQTT.RetrySettings.MaxDelay = 300
	settings.Realtime.MQTT.RetrySettings.BackoffMultiplier = 2.0
	
	return settings
}