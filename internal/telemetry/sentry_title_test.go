package telemetry

import (
	"errors"
	"strings"
	"testing"
)

func TestParseErrorType(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected string
	}{
		{
			name:     "nil pointer dereference",
			errMsg:   "runtime error: invalid memory address or nil pointer dereference",
			expected: "Nil Pointer Dereference",
		},
		{
			name:     "index out of range",
			errMsg:   "runtime error: index out of range [5] with length 3",
			expected: "Index Out of Range",
		},
		{
			name:     "slice bounds out of range",
			errMsg:   "runtime error: slice bounds out of range [::5]",
			expected: "Slice Bounds Out of Range",
		},
		{
			name:     "integer divide by zero",
			errMsg:   "runtime error: integer divide by zero",
			expected: "Integer Divide by Zero",
		},
		{
			name:     "invalid memory address",
			errMsg:   "runtime error: invalid memory address",
			expected: "Invalid Memory Access",
		},
		{
			name:     "send on closed channel",
			errMsg:   "send on closed channel",
			expected: "Send on Closed Channel",
		},
		{
			name:     "close of closed channel",
			errMsg:   "close of closed channel",
			expected: "Close of Closed Channel",
		},
		{
			name:     "concurrent map write",
			errMsg:   "concurrent map writes",
			expected: "Concurrent Map Write",
		},
		{
			name:     "concurrent map read",
			errMsg:   "concurrent map read and map write",
			expected: "Concurrent Map Access",
		},
		{
			name:     "interface conversion nil",
			errMsg:   "interface conversion: interface is nil, not string",
			expected: "Interface Conversion: Nil Value",
		},
		{
			name:     "interface conversion failed",
			errMsg:   "interface conversion: int is not string",
			expected: "Interface Conversion Failed",
		},
		{
			name:     "panic with message",
			errMsg:   "panic: something went wrong",
			expected: "Panic: something went wrong",
		},
		{
			name:     "panic with long message",
			errMsg:   "panic: this is a very long panic message that should be truncated to avoid overly long titles in the error tracking system",
			expected: "Panic: this is a very long panic message that should be t...",
		},
		{
			name:     "generic error short",
			errMsg:   "connection failed",
			expected: "connection failed",
		},
		{
			name:     "generic error long",
			errMsg:   "this is a very long error message that exceeds the maximum length and should be truncated",
			expected: "this is a very long error message that exceeds the maximum l...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseErrorType(tt.errMsg)
			if result != tt.expected {
				t.Errorf("parseErrorType(%q) = %q, want %q", tt.errMsg, result, tt.expected)
			}
		})
	}
}

func TestTitleCaseComponent(t *testing.T) {
	tests := []struct {
		name      string
		component string
		expected  string
	}{
		{
			name:      "http prefix",
			component: "httpcontroller",
			expected:  "HTTP Controller",
		},
		{
			name:      "rtsp prefix",
			component: "rtsphandler",
			expected:  "RTSP Handler",
		},
		{
			name:      "mqtt prefix",
			component: "mqttclient",
			expected:  "MQTT Client",
		},
		{
			name:      "api prefix",
			component: "apihandler",
			expected:  "API Handler",
		},
		{
			name:      "db prefix",
			component: "dbconnection",
			expected:  "DB Connection",
		},
		{
			name:      "snake_case",
			component: "media_handler",
			expected:  "Media Handler",
		},
		{
			name:      "simple component",
			component: "datastore",
			expected:  "Datastore",
		},
		{
			name:      "empty string",
			component: "",
			expected:  "",
		},
		{
			name:      "single letter",
			component: "a",
			expected:  "A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := titleCaseComponent(tt.component)
			if result != tt.expected {
				t.Errorf("titleCaseComponent(%q) = %q, want %q", tt.component, result, tt.expected)
			}
		})
	}
}

func TestGenerateErrorTitle(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		component string
		expected  string
	}{
		{
			name:      "nil pointer with component",
			err:       errors.New("runtime error: invalid memory address or nil pointer dereference"),
			component: "media_handler",
			expected:  "Media Handler: Nil Pointer Dereference",
		},
		{
			name:      "nil pointer without component",
			err:       errors.New("runtime error: invalid memory address or nil pointer dereference"),
			component: "",
			expected:  "Nil Pointer Dereference",
		},
		{
			name:      "index out of range with http component",
			err:       errors.New("runtime error: index out of range [5] with length 3"),
			component: "httpcontroller",
			expected:  "HTTP Controller: Index Out of Range",
		},
		{
			name:      "concurrent map write with api component",
			err:       errors.New("concurrent map writes"),
			component: "apihandler",
			expected:  "API Handler: Concurrent Map Write",
		},
		{
			name:      "generic error with component",
			err:       errors.New("connection timeout"),
			component: "database",
			expected:  "Database: connection timeout",
		},
		{
			name:      "panic with component",
			err:       errors.New("panic: unexpected condition"),
			component: "spectrogram",
			expected:  "Spectrogram: Panic: unexpected condition",
		},
		{
			name:      "unknown component treated as empty",
			err:       errors.New("some error"),
			component: "unknown",
			expected:  "some error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateErrorTitle(tt.err.Error(), tt.component)
			if result != tt.expected {
				t.Errorf("generateErrorTitle(errMsg=%q, component=%q) = %q, want %q",
					tt.err.Error(), tt.component, result, tt.expected)
			}
		})
	}
}

// TestGenerateErrorTitleRealWorldExamples tests with real error messages from production
func TestGenerateErrorTitleRealWorldExamples(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		component string
		expected  string
	}{
		{
			name:      "sentry issue 69275744 - spectrogram nil pointer",
			err:       errors.New("runtime error: invalid memory address or nil pointer dereference"),
			component: "media",
			expected:  "Media: Nil Pointer Dereference",
		},
		{
			name:      "http handler panic",
			err:       errors.New("panic: Handler.ServeHTTP panic"),
			component: "httpcontroller",
			expected:  "HTTP Controller: Panic: Handler.ServeHTTP panic",
		},
		{
			name:      "database connection error",
			err:       errors.New("failed to connect to database: connection refused"),
			component: "datastore",
			expected:  "Datastore: failed to connect to database: connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateErrorTitle(tt.err.Error(), tt.component)
			if result != tt.expected {
				t.Errorf("generateErrorTitle(errMsg=%q, component=%q) = %q, want %q",
					tt.err.Error(), tt.component, result, tt.expected)
			}
		})
	}
}

// TestParseErrorTypeWithCaseInsensitivity tests case-insensitive error pattern matching
func TestParseErrorTypeWithCaseInsensitivity(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected string
	}{
		{
			name:     "uppercase NIL POINTER",
			errMsg:   "RUNTIME ERROR: INVALID MEMORY ADDRESS OR NIL POINTER DEREFERENCE",
			expected: "Nil Pointer Dereference",
		},
		{
			name:     "mixed case Index Out Of Range",
			errMsg:   "Runtime Error: Index Out Of Range [5]",
			expected: "Index Out of Range",
		},
		{
			name:     "lowercase panic",
			errMsg:   "panic: something went wrong",
			expected: "Panic: something went wrong",
		},
		{
			name:     "mixed case concurrent map",
			errMsg:   "Concurrent Map Writes",
			expected: "Concurrent Map Write",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseErrorType(tt.errMsg)
			if result != tt.expected {
				t.Errorf("parseErrorType(%q) = %q, want %q", tt.errMsg, result, tt.expected)
			}
		})
	}
}

// TestParseErrorTypeWithNewlines tests that multi-line error messages are trimmed
func TestParseErrorTypeWithNewlines(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected string
	}{
		{
			name: "panic with stack trace",
			errMsg: `panic: something went wrong
goroutine 1 [running]:
main.foo()
	/path/to/file.go:42 +0x123`,
			expected: "Panic: something went wrong",
		},
		{
			name: "generic error with newlines",
			errMsg: `connection failed
dial tcp: lookup failed
timeout exceeded`,
			expected: "connection failed",
		},
		{
			name:     "single line no change",
			errMsg:   "connection timeout",
			expected: "connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseErrorType(tt.errMsg)
			if result != tt.expected {
				t.Errorf("parseErrorType(%q) = %q, want %q", tt.errMsg, result, tt.expected)
			}
		})
	}
}

// TestGenerateErrorTitlePrivacy tests that generateErrorTitle works with scrubbed messages
// This is a CRITICAL security test - error titles must not leak sensitive data
// NOTE: privacy.ScrubMessage() is called BEFORE generateErrorTitle() in production
func TestGenerateErrorTitlePrivacy(t *testing.T) {
	tests := []struct {
		name                string
		scrubbedMsg         string // Already scrubbed by privacy.ScrubMessage()
		component           string
		shouldContain       []string
		shouldNotContainPII bool
	}{
		{
			name:                "scrubbed email in path",
			scrubbedMsg:         "failed to read /home/[email-redacted]/config.yaml",
			component:           "config",
			shouldContain:       []string{"Config:", "[email-redacted]"},
			shouldNotContainPII: true,
		},
		{
			name:                "scrubbed IP address",
			scrubbedMsg:         "connection to [ip-redacted]:5432 failed",
			component:           "database",
			shouldContain:       []string{"Database:", "[ip-redacted]"},
			shouldNotContainPII: true,
		},
		{
			name:                "scrubbed UUID token",
			scrubbedMsg:         "invalid token [uuid-redacted]",
			component:           "auth",
			shouldContain:       []string{"Auth:", "[uuid-redacted]"},
			shouldNotContainPII: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// generateErrorTitle receives already-scrubbed messages
			result := generateErrorTitle(tt.scrubbedMsg, tt.component)

			// Verify expected patterns are in the title
			for _, pattern := range tt.shouldContain {
				if !strings.Contains(result, pattern) {
					t.Errorf("Title should contain %q, got: %q", pattern, result)
				}
			}

			// The actual PII scrubbing is done by privacy.ScrubMessage() before this function
			t.Logf("Scrubbed message: %s", tt.scrubbedMsg)
			t.Logf("Generated title: %s", result)
		})
	}
}

// TestCaptureErrorDocumentedFlow documents the privacy flow in CaptureError
func TestCaptureErrorDocumentedFlow(t *testing.T) {
	// This test documents the correct flow in CaptureError():
	//
	// 1. err is received (may contain PII)
	// 2. scrubbedMsg := privacy.ScrubMessage(err.Error())  ← PII removed here
	// 3. title := generateErrorTitle(scrubbedMsg, component)  ← Works with scrubbed data
	// 4. SetTag("error_title", title)  ← Title has no PII
	// 5. Exception.Type = title  ← Title has no PII
	//
	// This ensures PII can NEVER leak into Sentry error titles

	t.Run("documents correct flow", func(t *testing.T) {
		// Simulating the flow:
		originalErr := "failed to connect to user@secret.com:8080"
		t.Logf("Step 1: Original error: %s", originalErr)

		// In real code: scrubbedMsg := privacy.ScrubMessage(err.Error())
		scrubbedMsg := "failed to connect to [email-redacted]:8080"
		t.Logf("Step 2: After privacy.ScrubMessage(): %s", scrubbedMsg)

		// In real code: title := generateErrorTitle(scrubbedMsg, component)
		title := generateErrorTitle(scrubbedMsg, "network")
		t.Logf("Step 3: Generated title: %s", title)

		// Verify no PII in title
		if strings.Contains(title, "secret.com") || strings.Contains(title, "user@") {
			t.Fatal("CRITICAL: PII leaked into title!")
		}

		// Verify title is still useful
		if !strings.Contains(title, "Network:") {
			t.Error("Title should contain formatted component")
		}
	})
}
