package telemetry

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
)

func TestConvertToSentryLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    string
		expected sentry.Level
	}{
		{
			name:     "debug_level",
			level:    "debug",
			expected: sentry.LevelDebug,
		},
		{
			name:     "info_level",
			level:    "info",
			expected: sentry.LevelInfo,
		},
		{
			name:     "warning_level",
			level:    "warning",
			expected: sentry.LevelWarning,
		},
		{
			name:     "error_level",
			level:    "error",
			expected: sentry.LevelError,
		},
		{
			name:     "critical_level",
			level:    "critical",
			expected: sentry.LevelFatal,
		},
		{
			name:     "fatal_level",
			level:    "fatal",
			expected: sentry.LevelFatal,
		},
		{
			name:     "unknown_level_defaults_to_info",
			level:    "unknown",
			expected: sentry.LevelInfo,
		},
		{
			name:     "empty_level_defaults_to_info",
			level:    "",
			expected: sentry.LevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := convertToSentryLevel(tt.level)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSentryNotificationReporter_IsEnabled(t *testing.T) {
	t.Parallel()

	// Enable test mode to bypass settings check
	EnableTestMode()
	defer DisableTestMode()

	t.Run("enabled_reporter_returns_true", func(t *testing.T) {
		reporter := NewNotificationReporter(true)
		assert.True(t, reporter.IsEnabled())
	})

	t.Run("disabled_reporter_returns_false", func(t *testing.T) {
		reporter := NewNotificationReporter(false)
		assert.False(t, reporter.IsEnabled())
	})
}

func TestNewNotificationReporter(t *testing.T) {
	t.Parallel()

	t.Run("creates_enabled_reporter", func(t *testing.T) {
		t.Parallel()
		reporter := NewNotificationReporter(true)
		assert.NotNil(t, reporter)
		// Type assertion to check internal state
		if r, ok := reporter.(*SentryNotificationReporter); ok {
			assert.True(t, r.enabled)
		}
	})

	t.Run("creates_disabled_reporter", func(t *testing.T) {
		t.Parallel()
		reporter := NewNotificationReporter(false)
		assert.NotNil(t, reporter)
		// Type assertion to check internal state
		if r, ok := reporter.(*SentryNotificationReporter); ok {
			assert.False(t, r.enabled)
		}
	})
}
