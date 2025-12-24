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