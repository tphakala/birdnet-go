package conf

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

// ExampleValidateBirdNETSettings demonstrates validating BirdNET configuration
// without side effects. This is useful for testing validation logic in isolation.
func ExampleValidateBirdNETSettings() {
	config := &BirdNETConfig{
		Sensitivity: 1.0,
		Threshold:   0.7,
		Latitude:    45.0,
		Longitude:   -122.0,
	}

	result := ValidateBirdNETSettings(config)

	if result.Valid {
		fmt.Println("Configuration is valid")
	} else {
		fmt.Printf("Validation errors: %v\n", result.Errors)
	}
	// Output: Configuration is valid
}

// ExampleValidateBirdNETSettings_invalid demonstrates handling validation errors.
// The ValidationResult contains all errors and warnings without logging.
func ExampleValidateBirdNETSettings_invalid() {
	config := &BirdNETConfig{
		Threshold: 1.5,   // Invalid: must be between 0 and 1
		Latitude:  100.0, // Invalid: must be between -90 and 90
	}

	result := ValidateBirdNETSettings(config)

	if !result.Valid {
		fmt.Printf("Found %d validation errors\n", len(result.Errors))
		// In a real test, you would check specific error messages
	}
	// Output: Found 2 validation errors
}

// ExampleValidateMQTTSettings demonstrates validating MQTT configuration.
// Use this to test MQTT settings without actually connecting to a broker.
func ExampleValidateMQTTSettings() {
	settings := &MQTTSettings{
		Enabled: true,
		Broker:  "tcp://localhost:1883",
		Topic:   "birdnet/detections",
	}

	result := ValidateMQTTSettings(settings)

	if result.Valid {
		fmt.Println("MQTT configuration is valid")
	}
	// Output: MQTT configuration is valid
}

// ExampleValidateWebhookProvider demonstrates validating webhook configurations.
// This validates endpoint URLs, HTTP methods, and template syntax.
func ExampleValidateWebhookProvider() {
	provider := &PushProviderConfig{
		Name:    "custom-webhook",
		Enabled: true,
		Endpoints: []WebhookEndpointConfig{
			{
				URL:    "https://api.example.com/webhook",
				Method: "POST",
				Headers: map[string]string{
					"Authorization": "Bearer token123",
					"Content-Type":  "application/json",
				},
			},
		},
	}

	result := ValidateWebhookProvider(provider)

	if result.Valid {
		fmt.Printf("Webhook provider '%s' is valid\n", provider.Name)
	}
	// Output: Webhook provider 'custom-webhook' is valid
}

// ExamplePrepareSettingsForSave demonstrates the seasonal tracking auto-population.
// This function is used internally by SaveSettings to add default seasons based on latitude.
func Example_prepareSettingsForSave() {
	settings := &Settings{}
	settings.Realtime.SpeciesTracking.SeasonalTracking.Enabled = true
	// No seasons defined yet

	// For northern hemisphere (latitude > 10°)
	result := prepareSettingsForSave(settings, 45.0)

	if len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons) > 0 {
		fmt.Printf("Auto-populated %d seasons for northern hemisphere\n",
			len(result.Realtime.SpeciesTracking.SeasonalTracking.Seasons))
	}
	// Output: Auto-populated 4 seasons for northern hemisphere
}

// Example_hemisphereDetection demonstrates how the system determines which
// default seasons to use based on latitude.
func Example_hemisphereDetection() {
	latitudes := []float64{45.0, -33.9, 5.0}
	names := []string{"Northern", "Southern", "Equatorial"}

	for i, lat := range latitudes {
		hemisphere := DetectHemisphere(lat)
		fmt.Printf("%s (%.1f°): %s hemisphere\n", names[i], lat, hemisphere)
	}
	// Output:
	// Northern (45.0°): northern hemisphere
	// Southern (-33.9°): southern hemisphere
	// Equatorial (5.0°): equatorial hemisphere
}

// Example_testHelpers demonstrates using test assertion helpers to write
// cleaner and more maintainable tests.
func Example_testHelpers() {
	// This example shows the pattern, but doesn't actually run as a test
	// See test_helpers.go for the actual implementations

	// Example 1: Validating a configuration passes
	config := &BirdNETConfig{
		Threshold: 0.7,
		Latitude:  45.0,
	}
	_ = ValidateBirdNETSettings(config)

	// Instead of manual checks:
	// if !result.Valid || len(result.Errors) > 0 { ... }

	// Use helper for cleaner code:
	// assertValidationPasses(t, result)

	// Example 2: Checking for specific errors
	invalidConfig := &BirdNETConfig{
		Threshold: 1.5, // Invalid
	}
	_ = ValidateBirdNETSettings(invalidConfig)

	// Instead of looping through errors:
	// Use helper:
	// assertErrorContains(t, result, "threshold must be between 0 and 1")

	fmt.Println("Test helpers improve test readability")
	// Output: Test helpers improve test readability
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

// Example_validationWorkflow demonstrates a typical validation workflow
// in application code using the pure validation functions.
func Example_validationWorkflow() {
	// User-provided configuration
	userConfig := &BirdNETConfig{
		Threshold: 0.7,
		Latitude:  45.0,
		Longitude: -122.0,
	}

	// Validate without side effects
	result := ValidateBirdNETSettings(userConfig)

	if !result.Valid {
		// Collect all errors for user feedback
		fmt.Printf("Configuration errors:\n")
		for _, err := range result.Errors {
			fmt.Printf("  - %s\n", err)
		}
		return
	}

	// Check for warnings (non-fatal issues)
	if len(result.Warnings) > 0 {
		fmt.Printf("Configuration warnings:\n")
		for _, warning := range result.Warnings {
			fmt.Printf("  - %s\n", warning)
		}
	}

	// Apply normalized configuration
	if result.Normalized != nil {
		normalized := result.Normalized.(*BirdNETConfig)
		fmt.Printf("Using configuration with threshold: %.1f\n", normalized.Threshold)
	}

	// Output: Using configuration with threshold: 0.7
}
