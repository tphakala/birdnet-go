package errors

import (
	"fmt"
	"strings"
	"testing"
)

func TestFastPathNoTelemetry(t *testing.T) {
	t.Parallel()

	// Ensure no telemetry or hooks
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	// Create an error - should use fast path
	err := fmt.Errorf("test error")
	ee := New(err).Build()

	if ee.Err.Error() != "test error" {
		t.Errorf("Expected error message 'test error', got '%s'", ee.Err.Error())
	}

	// Use production constant directly to ensure test stays in sync
	if ee.GetComponent() != ComponentUnknown {
		t.Errorf("Expected component '%s' in fast path, got '%s'", ComponentUnknown, ee.GetComponent())
	}

	if ee.Category != CategoryGeneric {
		t.Errorf("Expected category 'generic' in fast path, got '%s'", ee.Category)
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

			if tt.expected != "" && scrubbed != tt.expected {
				t.Errorf("Expected: %s, got: %s", tt.expected, scrubbed)
			}
			if tt.contains != "" && !strings.Contains(scrubbed, tt.contains) {
				t.Errorf("Expected to contain '%s', got: %s", tt.contains, scrubbed)
			}
			for _, exclude := range tt.excludes {
				if strings.Contains(scrubbed, exclude) {
					t.Errorf("Should not contain '%s', got: %s", exclude, scrubbed)
				}
			}
		})
	}
}