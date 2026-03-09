package errors

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFastPathNoTelemetry(t *testing.T) {
	t.Parallel()

	// Ensure no telemetry or hooks
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	// Create an error - should use fast path
	err := fmt.Errorf("test error")
	ee := New(err).Build()

	assert.Equal(t, "test error", ee.Err.Error(), "expected error message 'test error'")

	// Use production constant directly to ensure test stays in sync
	assert.Equal(t, ComponentUnknown, ee.GetComponent(), "expected component to be unknown in fast path")

	assert.Equal(t, CategoryGeneric, ee.Category, "expected category 'generic' in fast path")
}

func TestErrorOriginTag(t *testing.T) {
	tests := []struct {
		name     string
		category ErrorCategory
		expected string
	}{
		{"validation is code", CategoryValidation, "code"},
		{"not-found is code", CategoryNotFound, "code"},
		{"model-init is code", CategoryModelInit, "code"},
		{"image-cache is code", CategoryImageCache, "code"},
		{"sound-level is code", CategorySoundLevel, "code"},
		{"network is environment", CategoryNetwork, "environment"},
		{"database is environment", CategoryDatabase, "environment"},
		{"file-io is environment", CategoryFileIO, "environment"},
		{"configuration is environment", CategoryConfiguration, "environment"},
		{"rtsp is environment", CategoryRTSP, "environment"},
		{"mqtt-connection is environment", CategoryMQTTConnection, "environment"},
		{"mqtt-publish is environment", CategoryMQTTPublish, "environment"},
		{"integration is external", CategoryIntegration, "external"},
		{"timeout is unknown", CategoryTimeout, "unknown"},
		{"generic is unknown", CategoryGeneric, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := GetErrorOrigin(tt.category)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRegexPrecompilation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
		contains string
		excludes []string
	}{
		{
			name:     "URL parameter scrubbing",
			input:    "Error at https://api.example.com?api_key=secret123&token=abc",
			expected: "Error at https://api.example.com?[REDACTED]",
		},
		{
			name:     "API key scrubbing",
			input:    "Config error: api_key=secret123 is invalid",
			contains: "[API_KEY_REDACTED]",
		},
		{
			name:     "Multi-token scrubbing",
			input:    "Auth failed with token=abc123 and auth=xyz789",
			excludes: []string{"abc123", "xyz789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scrubbed := basicURLScrub(tt.input)

			if tt.expected != "" {
				assert.Equal(t, tt.expected, scrubbed)
			}
			if tt.contains != "" {
				assert.Contains(t, scrubbed, tt.contains)
			}
			for _, exclude := range tt.excludes {
				assert.NotContains(t, scrubbed, exclude)
			}
		})
	}
}
