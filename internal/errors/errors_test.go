package errors

import (
	"fmt"
	"testing"
)

func TestFastPathNoTelemetry(t *testing.T) {
	// Ensure no telemetry or hooks
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	// Create an error - should use fast path
	err := fmt.Errorf("test error")
	ee := New(err).Build()

	if ee.Err.Error() != "test error" {
		t.Errorf("Expected error message 'test error', got '%s'", ee.Err.Error())
	}

	if ee.GetComponent() != "unknown" {
		t.Errorf("Expected component 'unknown' in fast path, got '%s'", ee.GetComponent())
	}

	if ee.Category != CategoryGeneric {
		t.Errorf("Expected category 'generic' in fast path, got '%s'", ee.Category)
	}
}

func TestRegexPrecompilation(t *testing.T) {
	// Test that regex patterns are pre-compiled
	testMessage := "Error at https://api.example.com?api_key=secret123&token=abc"
	scrubbed := basicURLScrub(testMessage)

	expectedPatterns := []string{
		"[REDACTED]",           // URL params
		"[API_KEY_REDACTED]",   // API key
	}

	for _, pattern := range expectedPatterns {
		if !contains(scrubbed, pattern) {
			t.Errorf("Expected scrubbed message to contain '%s', got: %s", pattern, scrubbed)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && substr != "" && 
		(s == substr || len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		func() bool {
			for i := 1; i < len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}()))
}