package conf

import (
	"strings"
	"testing"
)

// testHelper provides common assertion helpers for configuration tests.
// These helpers reduce code duplication and improve test readability.

// assertValidationPasses verifies that a ValidationResult indicates success.
// It checks that Valid is true and there are no errors.
//
// Example:
//
//	result := ValidateBirdNETSettings(&config)
//	assertValidationPasses(t, result)
func assertValidationPasses(t *testing.T, result ValidationResult) {
	t.Helper()
	if !result.Valid {
		t.Errorf("Expected valid config, got errors: %v", result.Errors)
	}
	if len(result.Errors) > 0 {
		t.Errorf("Expected no errors, got: %v", result.Errors)
	}
}

// assertValidationFails verifies that a ValidationResult indicates failure.
// It checks that Valid is false and at least one error is present.
//
// Example:
//
//	result := ValidateBirdNETSettings(&invalidConfig)
//	assertValidationFails(t, result)
func assertValidationFails(t *testing.T, result ValidationResult) {
	t.Helper()
	if result.Valid {
		t.Error("Expected invalid config to fail validation")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected validation errors but got none")
	}
}

// assertErrorContains verifies that the ValidationResult contains an error
// message that includes the expected substring.
//
// Example:
//
//	result := ValidateBirdNETSettings(&config)
//	assertErrorContains(t, result, "threshold must be between 0 and 1")
func assertErrorContains(t *testing.T, result ValidationResult, expectedSubstring string) {
	t.Helper()
	for _, err := range result.Errors {
		if strings.Contains(err, expectedSubstring) {
			return
		}
	}
	t.Errorf("Expected error containing %q, got errors: %v", expectedSubstring, result.Errors)
}

// assertWarningContains verifies that the ValidationResult contains a warning
// message that includes the expected substring.
//
// Example:
//
//	result := ValidateBirdNETSettings(&config)
//	assertWarningContains(t, result, "using default locale")
func assertWarningContains(t *testing.T, result ValidationResult, expectedSubstring string) {
	t.Helper()
	for _, warning := range result.Warnings {
		if strings.Contains(warning, expectedSubstring) {
			return
		}
	}
	t.Errorf("Expected warning containing %q, got warnings: %v", expectedSubstring, result.Warnings)
}

// assertNoWarnings verifies that the ValidationResult contains no warnings.
//
// Example:
//
//	result := ValidateBirdNETSettings(&config)
//	assertNoWarnings(t, result)
func assertNoWarnings(t *testing.T, result ValidationResult) {
	t.Helper()
	if len(result.Warnings) > 0 {
		t.Errorf("Expected no warnings, got: %v", result.Warnings)
	}
}

// assertBuilderFieldEquals verifies that a settings field matches the expected value.
// This is a generic helper for testing builder methods.
//
// Example:
//
//	settings := NewTestSettings().WithBirdNET(0.8, 45.0, -122.0).Build()
//	assertBuilderFieldEquals(t, "threshold", settings.BirdNET.Threshold, 0.8)
func assertBuilderFieldEquals[T comparable](t *testing.T, fieldName string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("Expected %s %v, got %v", fieldName, want, got)
	}
}

// assertSeasonExists verifies that a specific season exists in the seasonal tracking settings
// and optionally checks its start month.
//
// Example:
//
//	settings := prepareSettingsForSave(&s, 45.0)
//	assertSeasonExists(t, &settings, "spring", 3)  // Expects spring starting in March
func assertSeasonExists(t *testing.T, settings *Settings, seasonName string, expectedStartMonth int) {
	t.Helper()
	season, exists := settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons[seasonName]
	if !exists {
		t.Errorf("Expected season %q to exist", seasonName)
		return
	}
	if expectedStartMonth > 0 && season.StartMonth != expectedStartMonth {
		t.Errorf("Expected season %q to start in month %d, got %d",
			seasonName, expectedStartMonth, season.StartMonth)
	}
}

// assertSeasonsCount verifies the number of seasons in seasonal tracking settings.
//
// Example:
//
//	settings := prepareSettingsForSave(&s, 45.0)
//	assertSeasonsCount(t, &settings, 4)  // Northern hemisphere has 4 seasons
func assertSeasonsCount(t *testing.T, settings *Settings, expectedCount int) {
	t.Helper()
	got := len(settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons)
	if got != expectedCount {
		t.Errorf("Expected %d seasons, got %d", expectedCount, got)
	}
}

// assertMQTTEnabled verifies that MQTT is enabled in settings.
//
// Example:
//
//	settings := NewTestSettings().WithMQTT("tcp://localhost:1883", "test").Build()
//	assertMQTTEnabled(t, settings)
func assertMQTTEnabled(t *testing.T, settings *Settings) {
	t.Helper()
	if !settings.Realtime.MQTT.Enabled {
		t.Error("Expected MQTT to be enabled")
	}
}

// assertSpeciesTrackingEnabled verifies that species tracking is enabled in settings.
//
// Example:
//
//	settings := NewTestSettings().WithSpeciesTracking(7, 3600).Build()
//	assertSpeciesTrackingEnabled(t, settings)
func assertSpeciesTrackingEnabled(t *testing.T, settings *Settings) {
	t.Helper()
	if !settings.Realtime.SpeciesTracking.Enabled {
		t.Error("Expected species tracking to be enabled")
	}
}

// assertAudioExportEnabled verifies that audio export is enabled in settings.
//
// Example:
//
//	settings := NewTestSettings().WithAudioExport("/tmp", "mp3", "192k").Build()
//	assertAudioExportEnabled(t, settings)
func assertAudioExportEnabled(t *testing.T, settings *Settings) {
	t.Helper()
	if !settings.Realtime.Audio.Export.Enabled {
		t.Error("Expected audio export to be enabled")
	}
}
