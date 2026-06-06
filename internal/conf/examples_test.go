package conf

import (
	"fmt"
)

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
	settings.BirdNET.LocationConfigured = true
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
