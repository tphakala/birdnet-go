package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// testHelper provides common assertion helpers for configuration tests.
// These helpers reduce code duplication and improve test readability.

// requireEnhancedError asserts that the error is an EnhancedError and returns it.
func requireEnhancedError(t *testing.T, err error) *errors.EnhancedError {
	t.Helper()
	require.Error(t, err)
	var enhanced *errors.EnhancedError
	require.ErrorAs(t, err, &enhanced, "expected EnhancedError type, got %T", err)
	return enhanced
}

// assertValidationError asserts that an error is an EnhancedError with
// CategoryValidation and the specified validation type in context.
func assertValidationError(t *testing.T, err error, validationType string) {
	t.Helper()
	enhanced := requireEnhancedError(t, err)
	assert.Equal(t, errors.CategoryValidation, enhanced.Category,
		"expected CategoryValidation, got %s", enhanced.Category)

	if validationType != "" {
		ctx, exists := enhanced.Context["validation_type"]
		assert.True(t, exists, "expected validation_type context to be set")
		assert.Equal(t, validationType, ctx,
			"expected validation_type = %s, got %s", validationType, ctx)
	}
}

// assertValidationPasses verifies that a ValidationResult indicates success.
func assertValidationPasses(t *testing.T, result ValidationResult) {
	t.Helper()
	assert.True(t, result.Valid, "expected valid config, got errors: %v", result.Errors)
	assert.Empty(t, result.Errors, "expected no errors")
}

// assertValidationFails verifies that a ValidationResult indicates failure.
func assertValidationFails(t *testing.T, result ValidationResult) {
	t.Helper()
	assert.False(t, result.Valid, "expected invalid config to fail validation")
	assert.NotEmpty(t, result.Errors, "expected validation errors but got none")
}

// assertErrorContains verifies that the ValidationResult contains an error
// message that includes the expected substring.
func assertErrorContains(t *testing.T, result ValidationResult, expectedSubstring string) {
	t.Helper()
	for _, err := range result.Errors {
		if strings.Contains(err, expectedSubstring) {
			return
		}
	}
	assert.Fail(t, "expected error not found",
		"expected error containing %q, got errors: %v", expectedSubstring, result.Errors)
}

// assertWarningContains verifies that the ValidationResult contains a warning
// message that includes the expected substring.
func assertWarningContains(t *testing.T, result ValidationResult, expectedSubstring string) {
	t.Helper()
	for _, warning := range result.Warnings {
		if strings.Contains(warning, expectedSubstring) {
			return
		}
	}
	assert.Fail(t, "expected warning not found",
		"expected warning containing %q, got warnings: %v", expectedSubstring, result.Warnings)
}

// assertNoWarnings verifies that the ValidationResult contains no warnings.
func assertNoWarnings(t *testing.T, result ValidationResult) {
	t.Helper()
	assert.Empty(t, result.Warnings, "expected no warnings")
}

// assertSeasonExists verifies that a specific season exists in the seasonal tracking settings
// and optionally checks its start month.
func assertSeasonExists(t *testing.T, settings *Settings, seasonName string, expectedStartMonth int) {
	t.Helper()
	season, exists := settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons[seasonName]
	require.True(t, exists, "expected season %q to exist", seasonName)
	if expectedStartMonth > 0 {
		assert.Equal(t, expectedStartMonth, season.StartMonth,
			"expected season %q to start in month %d", seasonName, expectedStartMonth)
	}
}

// assertSeasonsCount verifies the number of seasons in seasonal tracking settings.
func assertSeasonsCount(t *testing.T, settings *Settings, expectedCount int) {
	t.Helper()
	got := len(settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons)
	assert.Equal(t, expectedCount, got, "season count mismatch")
}

// assertMQTTEnabled verifies that MQTT is enabled in settings.
func assertMQTTEnabled(t *testing.T, settings *Settings) {
	t.Helper()
	assert.True(t, settings.Realtime.MQTT.Enabled, "expected MQTT to be enabled")
}

// assertSpeciesTrackingEnabled verifies that species tracking is enabled in settings.
func assertSpeciesTrackingEnabled(t *testing.T, settings *Settings) {
	t.Helper()
	assert.True(t, settings.Realtime.SpeciesTracking.Enabled, "expected species tracking to be enabled")
}

// assertAudioExportEnabled verifies that audio export is enabled in settings.
func assertAudioExportEnabled(t *testing.T, settings *Settings) {
	t.Helper()
	assert.True(t, settings.Realtime.Audio.Export.Enabled, "expected audio export to be enabled")
}
