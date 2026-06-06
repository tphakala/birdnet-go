package conftest

import (
	"fmt"
)

// ExampleNewTestSettings demonstrates creating test settings with the builder pattern.
// This is the recommended way to configure settings in tests as it's more readable
// and less error-prone than manual struct construction.
func ExampleNewTestSettings() {
	// Create settings with minimal configuration for unit tests
	settings := NewTestSettings().Build()

	fmt.Printf("Default threshold: %.1f\n", settings.BirdNET.Threshold)
	// Output: Default threshold: 0.8
}

// ExampleSettingsBuilder_WithBirdNET demonstrates configuring BirdNET settings.
// Use this when your test needs specific BirdNET threshold or location values.
func ExampleSettingsBuilder_WithBirdNET() {
	settings := NewTestSettings().
		WithBirdNET(0.7, 45.5, -122.6).
		Build()

	fmt.Printf("Threshold: %.1f, Latitude: %.1f\n",
		settings.BirdNET.Threshold,
		settings.BirdNET.Latitude)
	// Output: Threshold: 0.7, Latitude: 45.5
}

// ExampleSettingsBuilder_WithMQTT demonstrates configuring MQTT settings.
// This automatically enables MQTT and sets the broker and topic.
func ExampleSettingsBuilder_WithMQTT() {
	settings := NewTestSettings().
		WithMQTT("tcp://localhost:1883", "birdnet/detections").
		Build()

	if settings.Realtime.MQTT.Enabled {
		fmt.Printf("MQTT enabled on %s\n", settings.Realtime.MQTT.Topic)
	}
	// Output: MQTT enabled on birdnet/detections
}

// ExampleSettingsBuilder_methodChaining demonstrates the fluent interface.
// Multiple configuration methods can be chained together for complex test scenarios.
func ExampleSettingsBuilder_methodChaining() {
	settings := NewTestSettings().
		WithBirdNET(0.75, 40.0, -100.0).
		WithMQTT("tcp://localhost:1883", "test/topic").
		WithAudioExport("/tmp/audio", "mp3", "192k").
		WithWebServer("8080", true).
		Build()

	fmt.Printf("Web server on port %s, MQTT enabled: %v\n",
		settings.WebServer.Port,
		settings.Realtime.MQTT.Enabled)
	// Output: Web server on port 8080, MQTT enabled: true
}

// Example_complexScenario demonstrates testing a complex configuration
// scenario with multiple components enabled.
func Example_complexScenario() {
	// Scenario: Setting up a complete monitoring station with:
	// - BirdNET detection
	// - MQTT notifications
	// - Audio export
	// - Species tracking
	// - Web dashboard

	settings := NewTestSettings().
		WithBirdNET(0.75, 45.5231, -122.6765). // Portland, OR coordinates
		WithMQTT("ssl://broker.hivemq.com:8883", "backyard/birds").
		WithAudioExport("/data/recordings", "mp3", "192k").
		WithSpeciesTracking(7, 60). // 7-day window, sync every 60 minutes
		WithImageProvider("wikimedia", "all").
		WithWebServer("8080", true).
		Build()

	// Verify the complex configuration
	fmt.Printf("BirdNET threshold: %.2f\n", settings.BirdNET.Threshold)
	fmt.Printf("MQTT enabled: %v\n", settings.Realtime.MQTT.Enabled)
	fmt.Printf("Audio export: %s\n", settings.Realtime.Audio.Export.Type)
	fmt.Printf("Species tracking: %v\n", settings.Realtime.SpeciesTracking.Enabled)
	fmt.Printf("Web server port: %s\n", settings.WebServer.Port)

	// Output:
	// BirdNET threshold: 0.75
	// MQTT enabled: true
	// Audio export: mp3
	// Species tracking: true
	// Web server port: 8080
}
