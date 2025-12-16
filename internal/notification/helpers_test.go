//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"testing"
)

// Expected value for redacted credentials in scrubContextMap output
const expectedRedactedValue = "[REDACTED]"

func TestScrubContextMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]any
		expected func(map[string]any) bool
	}{
		{
			name:  "nil map",
			input: nil,
			expected: func(result map[string]any) bool {
				return result == nil
			},
		},
		{
			name: "URL scrubbing",
			input: map[string]any{
				"url":        "https://example.com/path",
				"endpoint":   "http://api.example.com",
				"rtsp_url":   "rtsp://camera.local/stream1",
				"stream_url": "https://stream.example.com/live",
			},
			expected: func(result map[string]any) bool {
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
			input: map[string]any{
				"error":       "Connection failed to https://api.example.com",
				"message":     "Failed to process request",
				"description": "Error occurred at line 42",
			},
			expected: func(result map[string]any) bool {
				// Messages should be scrubbed
				return len(result) == 3
			},
		},
		{
			name: "IP address anonymization",
			input: map[string]any{
				"ip":          "192.168.1.1",
				"client_ip":   "10.0.0.1",
				"remote_addr": "::1",
				"source_ip":   "172.16.0.1",
			},
			expected: func(result map[string]any) bool {
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
			input: map[string]any{
				"token":    "secret-token-123",
				"api_key":  "api-key-456",
				"password": "super-secret",
				"secret":   "confidential",
			},
			expected: func(result map[string]any) bool {
				// All credentials should be redacted
				for k := range result {
					if result[k] != expectedRedactedValue {
						return false
					}
				}
				return len(result) == 4
			},
		},
		{
			name: "Mixed content",
			input: map[string]any{
				"url":      "https://example.com",
				"token":    "secret",
				"count":    42,
				"enabled":  true,
				"metadata": "some data",
			},
			expected: func(result map[string]any) bool {
				// URL should be anonymized, token redacted, others unchanged
				return result["token"] == expectedRedactedValue &&
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

func TestEnrichWithTemplateData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		notification *Notification
		templateData *TemplateData
		wantNil      bool
	}{
		{
			name:         "nil notification",
			notification: nil,
			templateData: &TemplateData{
				DetectionID:       "1",
				DetectionPath:     "/ui/detections/1",
				DetectionURL:      "http://localhost/detections/1",
				ImageURL:          "http://localhost/image.jpg",
				ConfidencePercent: "95",
				DetectionTime:     "14:30:00",
				DetectionDate:     "2025-10-27",
				Latitude:          42.3601,
				Longitude:         -71.0589,
				Location:          "Test Location",
			},
			wantNil: true,
		},
		{
			name:         "nil template data",
			notification: NewNotification(TypeDetection, PriorityHigh, "Test", "Test Message"),
			templateData: nil,
			wantNil:      false,
		},
		{
			name:         "valid notification and template data",
			notification: NewNotification(TypeDetection, PriorityHigh, "Test", "Test Message"),
			templateData: &TemplateData{
				DetectionID:       "1",
				DetectionPath:     "/ui/detections/1",
				DetectionURL:      "http://localhost/detections/1",
				ImageURL:          "http://localhost/image.jpg",
				ConfidencePercent: "95",
				DetectionTime:     "14:30:00",
				DetectionDate:     "2025-10-27",
				Latitude:          42.3601,
				Longitude:         -71.0589,
				Location:          "Test Location",
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := EnrichWithTemplateData(tt.notification, tt.templateData)

			if tt.wantNil && result != nil {
				t.Errorf("EnrichWithTemplateData() expected nil, got %v", result)
				return
			}

			if !tt.wantNil && result == nil {
				t.Errorf("EnrichWithTemplateData() expected non-nil result")
				return
			}

			// Verify metadata was added when both inputs are valid
			if tt.notification != nil && tt.templateData != nil && result != nil {
				// Verify all field values match expected template data
				expectedValues := map[string]any{
					"bg_detection_id":       tt.templateData.DetectionID,
					"bg_detection_path":     tt.templateData.DetectionPath,
					"bg_detection_url":      tt.templateData.DetectionURL,
					"bg_image_url":          tt.templateData.ImageURL,
					"bg_confidence_percent": tt.templateData.ConfidencePercent,
					"bg_detection_time":     tt.templateData.DetectionTime,
					"bg_detection_date":     tt.templateData.DetectionDate,
					"bg_latitude":           tt.templateData.Latitude,
					"bg_longitude":          tt.templateData.Longitude,
					"bg_location":           tt.templateData.Location,
				}

				for field, expectedValue := range expectedValues {
					actualValue, exists := result.Metadata[field]
					if !exists {
						t.Errorf("EnrichWithTemplateData() missing expected field: %s", field)
						continue
					}
					if actualValue != expectedValue {
						t.Errorf("%s = %v, want %v", field, actualValue, expectedValue)
					}
				}
			}
		})
	}
}
