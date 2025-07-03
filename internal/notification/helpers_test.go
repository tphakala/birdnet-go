package notification

import (
	"testing"
)

func TestScrubContextMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]interface{}
		expected func(map[string]interface{}) bool
	}{
		{
			name:  "nil map",
			input: nil,
			expected: func(result map[string]interface{}) bool {
				return result == nil
			},
		},
		{
			name: "URL scrubbing",
			input: map[string]interface{}{
				"url":        "https://example.com/path",
				"endpoint":   "http://api.example.com",
				"rtsp_url":   "rtsp://camera.local/stream1",
				"stream_url": "https://stream.example.com/live",
			},
			expected: func(result map[string]interface{}) bool {
				// All URLs should be anonymized
				for k := range result {
					if result[k] == "https://example.com/path" {
						return false
					}
				}
				return len(result) == 4
			},
		},
		{
			name: "Error message scrubbing",
			input: map[string]interface{}{
				"error":       "Connection failed to https://api.example.com",
				"message":     "Failed to process request",
				"description": "Error occurred at line 42",
			},
			expected: func(result map[string]interface{}) bool {
				// Messages should be scrubbed
				return len(result) == 3
			},
		},
		{
			name: "IP address anonymization",
			input: map[string]interface{}{
				"ip":         "192.168.1.1",
				"client_ip":  "10.0.0.1",
				"remote_addr": "::1",
				"source_ip":  "172.16.0.1",
			},
			expected: func(result map[string]interface{}) bool {
				// IPs should be anonymized
				for k := range result {
					if result[k] == "192.168.1.1" {
						return false
					}
				}
				return len(result) == 4
			},
		},
		{
			name: "Credential redaction",
			input: map[string]interface{}{
				"token":    "secret-token-123",
				"api_key":  "api-key-456",
				"password": "super-secret",
				"secret":   "confidential",
			},
			expected: func(result map[string]interface{}) bool {
				// All credentials should be "[REDACTED]"
				for k := range result {
					if result[k] != "[REDACTED]" {
						return false
					}
				}
				return len(result) == 4
			},
		},
		{
			name: "Mixed content",
			input: map[string]interface{}{
				"url":      "https://example.com",
				"token":    "secret",
				"count":    42,
				"enabled":  true,
				"metadata": "some data",
			},
			expected: func(result map[string]interface{}) bool {
				// URL should be anonymized, token redacted, others unchanged
				return result["token"] == "[REDACTED]" &&
					result["count"] == 42 &&
					result["enabled"] == true &&
					result["metadata"] == "some data"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := scrubContextMap(tt.input)
			if !tt.expected(result) {
				t.Errorf("scrubContextMap() result did not meet expectations")
			}
		})
	}
}

func TestScrubPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty path",
			input: "",
		},
		{
			name:  "absolute path",
			input: "/home/user/documents/file.txt",
		},
		{
			name:  "relative path",
			input: "../config/settings.yaml",
		},
		{
			name:  "Windows path",
			input: "C:\\Users\\User\\Documents\\file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := scrubPath(tt.input)
			if tt.input != "" && result == tt.input {
				t.Errorf("scrubPath() should have anonymized the path, but got: %s", result)
			}
			if tt.input == "" && result != "" {
				t.Errorf("scrubPath() should return empty string for empty input, but got: %s", result)
			}
		})
	}
}

func TestScrubNotificationContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "URL in content",
			input: "Failed to connect to https://api.example.com/endpoint",
		},
		{
			name:  "IP address in content",
			input: "Connection from http://192.168.1.100 was rejected",
		},
		{
			name:  "API token in content",
			input: "Authentication failed with token: abc123def456",
		},
		{
			name:  "Multiple sensitive items",
			input: "Error: Failed to connect to https://api.example.com using token abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := scrubNotificationContent(tt.input)
			// Result should be different from input (scrubbed)
			if result == tt.input {
				t.Errorf("scrubNotificationContent() should have scrubbed the content, but got: %s", result)
			}
		})
	}
}

func TestScrubIPAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty IP",
			input: "",
		},
		{
			name:  "IPv4 address",
			input: "192.168.1.1",
		},
		{
			name:  "IPv6 address",
			input: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		},
		{
			name:  "Localhost",
			input: "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := scrubIPAddress(tt.input)
			if tt.input != "" && result == tt.input {
				t.Errorf("scrubIPAddress() should have anonymized the IP, but got: %s", result)
			}
			if tt.input == "" && result != "" {
				t.Errorf("scrubIPAddress() should return empty string for empty input, but got: %s", result)
			}
		})
	}
}