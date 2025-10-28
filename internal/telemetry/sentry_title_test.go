package telemetry

import (
	"errors"
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
			result := generateErrorTitle(tt.err, tt.component)
			if result != tt.expected {
				t.Errorf("generateErrorTitle(err=%q, component=%q) = %q, want %q",
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
			result := generateErrorTitle(tt.err, tt.component)
			if result != tt.expected {
				t.Errorf("generateErrorTitle(err=%q, component=%q) = %q, want %q",
					tt.err.Error(), tt.component, result, tt.expected)
			}
		})
	}
}
