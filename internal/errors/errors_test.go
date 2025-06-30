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

	if ee.GetComponent() != "unknown" {
		t.Errorf("Expected component 'unknown' in fast path, got '%s'", ee.GetComponent())
	}

	if ee.Category != CategoryGeneric {
		t.Errorf("Expected category 'generic' in fast path, got '%s'", ee.Category)
	}
}

func TestRegexPrecompilation(t *testing.T) {
	t.Parallel()
	
	// Test that regex patterns are pre-compiled and work correctly
	
	// Test URL scrubbing
	testMessage1 := "Error at https://api.example.com?api_key=secret123&token=abc"
	scrubbed1 := basicURLScrub(testMessage1)
	expected1 := "Error at https://api.example.com?[REDACTED]"
	if scrubbed1 != expected1 {
		t.Errorf("URL scrubbing failed. Expected: %s, got: %s", expected1, scrubbed1)
	}
	
	// Test API key scrubbing in non-URL context
	testMessage2 := "Config error: api_key=secret123 is invalid"
	scrubbed2 := basicURLScrub(testMessage2)
	if !strings.Contains(scrubbed2, "[API_KEY_REDACTED]") {
		t.Errorf("API key scrubbing failed. Expected to contain '[API_KEY_REDACTED]', got: %s", scrubbed2)
	}
	
	// Test multiple patterns
	testMessage3 := "Auth failed with token=abc123 and auth=xyz789"
	scrubbed3 := basicURLScrub(testMessage3)
	if strings.Contains(scrubbed3, "abc123") || strings.Contains(scrubbed3, "xyz789") {
		t.Errorf("Token scrubbing failed. Sensitive data still present: %s", scrubbed3)
	}
}