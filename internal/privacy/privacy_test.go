package privacy

import (
	"strings"
	"testing"
)

func TestScrubMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		contains []string // strings that should be in the output
		notContains []string // strings that should NOT be in the output
	}{
		{
			name:     "Basic RTSP URL with credentials",
			input:    "Failed to connect to rtsp://admin:password@192.168.1.100:554/stream1",
			contains: []string{"Failed to connect to url-"},
			notContains: []string{"admin", "password", "192.168.1.100"},
		},
		{
			name:     "HTTP URL with domain",
			input:    "Error fetching http://example.com/api/v1/data",
			contains: []string{"Error fetching url-"},
			notContains: []string{"example.com"},
		},
		{
			name:     "Multiple URLs in message",
			input:    "Failed rtsp://user:pass@cam1.local/stream and https://api.service.com/upload",
			contains: []string{"Failed url-", "and url-"},
			notContains: []string{"user", "pass", "cam1.local", "api.service.com"},
		},
		{
			name:     "Message without URLs",
			input:    "Simple error message without any URLs",
			contains: []string{"Simple error message without any URLs"},
			notContains: []string{"url-"},
		},
		{
			name:     "Empty message",
			input:    "",
			contains: []string{""},
			notContains: []string{"url-"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubMessage(tt.input)
			
			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("Expected result to contain %q, but got: %s", expected, result)
				}
			}
			
			for _, unexpected := range tt.notContains {
				if strings.Contains(result, unexpected) {
					t.Errorf("Expected result to NOT contain %q, but got: %s", unexpected, result)
				}
			}
		})
	}
}

func TestAnonymizeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		expectPrefix string
	}{
		{
			name:      "RTSP URL with credentials",
			input:     "rtsp://admin:password@192.168.1.100:554/stream1",
			expectPrefix: "url-",
		},
		{
			name:      "HTTP URL with domain",
			input:     "http://example.com/api/data",
			expectPrefix: "url-",
		},
		{
			name:      "HTTPS URL with port",
			input:     "https://secure.example.com:8443/secure/api",
			expectPrefix: "url-",
		},
		{
			name:      "Invalid URL",
			input:     "not-a-valid-url",
			expectPrefix: "url-",
		},
		{
			name:      "Localhost URL",
			input:     "http://localhost:8080/api",
			expectPrefix: "url-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := AnonymizeURL(tt.input)
			
			if !strings.HasPrefix(result, tt.expectPrefix) {
				t.Errorf("Expected result to start with %q, but got: %s", tt.expectPrefix, result)
			}
			
			// Ensure consistent anonymization - same input should produce same output
			result2 := AnonymizeURL(tt.input)
			if result != result2 {
				t.Errorf("Expected consistent anonymization, but got different results: %s vs %s", result, result2)
			}
		})
	}
}

func TestSanitizeRTSPUrl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "RTSP URL with credentials and path",
			input:    "rtsp://admin:password@192.168.1.100:554/stream1/channel1",
			expected: "rtsp://192.168.1.100:554",
		},
		{
			name:     "RTSP URL without credentials but with path",
			input:    "rtsp://192.168.1.100:554/stream1",
			expected: "rtsp://192.168.1.100:554",
		},
		{
			name:     "RTSP URL without credentials and path",
			input:    "rtsp://192.168.1.100:554",
			expected: "rtsp://192.168.1.100:554",
		},
		{
			name:     "Non-RTSP URL should remain unchanged",
			input:    "http://example.com/api",
			expected: "http://example.com/api",
		},
		{
			name:     "RTSP URL with only credentials",
			input:    "rtsp://user:pass@camera.local",
			expected: "rtsp://camera.local",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := SanitizeRTSPUrl(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, but got %q", tt.expected, result)
			}
		})
	}
}

func TestGenerateSystemID(t *testing.T) {
	t.Parallel()

	// Test multiple generations to ensure they're unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := GenerateSystemID()
		if err != nil {
			t.Fatalf("GenerateSystemID failed: %v", err)
		}
		
		// Check format
		if !IsValidSystemID(id) {
			t.Errorf("Generated ID %q is not valid", id)
		}
		
		// Check uniqueness
		if ids[id] {
			t.Errorf("Duplicate ID generated: %q", id)
		}
		ids[id] = true
		
		// Check format manually as well
		if len(id) != 14 {
			t.Errorf("Expected ID length 14, got %d for ID: %q", len(id), id)
		}
		
		if id[4] != '-' || id[9] != '-' {
			t.Errorf("Expected hyphens at positions 4 and 9, got: %q", id)
		}
	}
}

func TestIsValidSystemID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{
			name:  "Valid uppercase ID",
			input: "A1B2-C3D4-E5F6",
			valid: true,
		},
		{
			name:  "Valid lowercase ID",
			input: "a1b2-c3d4-e5f6",
			valid: true,
		},
		{
			name:  "Valid mixed case ID",
			input: "A1b2-C3d4-E5f6",
			valid: true,
		},
		{
			name:  "Too short",
			input: "A1B2-C3D4",
			valid: false,
		},
		{
			name:  "Too long",
			input: "A1B2-C3D4-E5F6-G7H8",
			valid: false,
		},
		{
			name:  "Missing hyphens",
			input: "A1B2C3D4E5F6",
			valid: false,
		},
		{
			name:  "Wrong hyphen positions",
			input: "A1B-2C3D4-E5F6",
			valid: false,
		},
		{
			name:  "Invalid characters",
			input: "A1B2-C3G4-E5F6", // G is not hex
			valid: false,
		},
		{
			name:  "Empty string",
			input: "",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := IsValidSystemID(tt.input)
			if result != tt.valid {
				t.Errorf("Expected IsValidSystemID(%q) = %v, got %v", tt.input, tt.valid, result)
			}
		})
	}
}

func TestCategorizeHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "localhost",
			input:    "localhost",
			expected: "localhost",
		},
		{
			name:     "127.0.0.1",
			input:    "127.0.0.1",
			expected: "localhost",
		},
		{
			name:     "IPv6 localhost",
			input:    "::1",
			expected: "localhost",
		},
		{
			name:     "Private IP 192.168.x.x",
			input:    "192.168.1.100",
			expected: "private-ip",
		},
		{
			name:     "Private IP 10.x.x.x",
			input:    "10.0.0.1",
			expected: "private-ip",
		},
		{
			name:     "Private IP 172.16.x.x",
			input:    "172.16.1.1",
			expected: "private-ip",
		},
		{
			name:     "Public IP",
			input:    "8.8.8.8",
			expected: "public-ip",
		},
		{
			name:     "Domain with .com TLD",
			input:    "example.com",
			expected: "domain-com",
		},
		{
			name:     "Domain with .org TLD",
			input:    "test.org",
			expected: "domain-org",
		},
		{
			name:     "Subdomain",
			input:    "sub.example.com",
			expected: "domain-com",
		},
		{
			name:     "Unknown host",
			input:    "unknown",
			expected: "unknown-host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := categorizeHost(tt.input)
			if result != tt.expected {
				t.Errorf("Expected categorizeHost(%q) = %q, got %q", tt.input, tt.expected, result)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Private 192.168.x.x",
			input:    "192.168.1.100",
			expected: true,
		},
		{
			name:     "Private 10.x.x.x",
			input:    "10.0.0.1",
			expected: true,
		},
		{
			name:     "Private 172.16.x.x",
			input:    "172.16.1.1",
			expected: true,
		},
		{
			name:     "Private 172.31.x.x",
			input:    "172.31.255.255",
			expected: true,
		},
		{
			name:     "Link-local 169.254.x.x",
			input:    "169.254.1.1",
			expected: true,
		},
		{
			name:     "IPv6 localhost",
			input:    "::1",
			expected: true,
		},
		{
			name:     "IPv6 unique local fc00:",
			input:    "fc00::1",
			expected: true,
		},
		{
			name:     "IPv6 link-local fe80:",
			input:    "fe80::1",
			expected: true,
		},
		{
			name:     "Public IP",
			input:    "8.8.8.8",
			expected: false,
		},
		{
			name:     "Public IP Google DNS",
			input:    "8.8.4.4",
			expected: false,
		},
		{
			name:     "Not an IP",
			input:    "example.com",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := isPrivateIP(tt.input)
			if result != tt.expected {
				t.Errorf("Expected isPrivateIP(%q) = %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}

func TestIsIPAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Valid IPv4",
			input:    "192.168.1.1",
			expected: true,
		},
		{
			name:     "Valid IPv4 public",
			input:    "8.8.8.8",
			expected: true,
		},
		{
			name:     "IPv6 with colons",
			input:    "2001:db8::1",
			expected: true,
		},
		{
			name:     "IPv6 localhost",
			input:    "::1",
			expected: true,
		},
		{
			name:     "Domain name",
			input:    "example.com",
			expected: false,
		},
		{
			name:     "Invalid IPv4 format",
			input:    "999.999.999.999",
			expected: true, // Our function doesn't validate ranges, just format
		},
		{
			name:     "Not an IP",
			input:    "localhost",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := isIPAddress(tt.input)
			if result != tt.expected {
				t.Errorf("Expected isIPAddress(%q) = %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}

func TestIsCommonStreamName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "stream",
			input:    "stream",
			expected: true,
		},
		{
			name:     "live",
			input:    "live",
			expected: true,
		},
		{
			name:     "camera1",
			input:    "camera1",
			expected: true,
		},
		{
			name:     "video_feed",
			input:    "video_feed",
			expected: true,
		},
		{
			name:     "RTSP_STREAM",
			input:    "RTSP_STREAM",
			expected: true,
		},
		{
			name:     "random_string",
			input:    "random_string",
			expected: false,
		},
		{
			name:     "sensitive_path",
			input:    "sensitive_path",
			expected: false,
		},
		{
			name:     "empty",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := isCommonStreamName(tt.input)
			if result != tt.expected {
				t.Errorf("Expected isCommonStreamName(%q) = %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "single digit",
			input:    "1",
			expected: true,
		},
		{
			name:     "multiple digits",
			input:    "12345",
			expected: true,
		},
		{
			name:     "zero",
			input:    "0",
			expected: true,
		},
		{
			name:     "with letter",
			input:    "123a",
			expected: false,
		},
		{
			name:     "with space",
			input:    "123 456",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "negative number",
			input:    "-123",
			expected: false,
		},
		{
			name:     "decimal number",
			input:    "12.34",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := isNumeric(tt.input)
			if result != tt.expected {
				t.Errorf("Expected isNumeric(%q) = %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}

func TestIsHexChar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    rune
		expected bool
	}{
		{
			name:     "digit 0",
			input:    '0',
			expected: true,
		},
		{
			name:     "digit 9",
			input:    '9',
			expected: true,
		},
		{
			name:     "uppercase A",
			input:    'A',
			expected: true,
		},
		{
			name:     "uppercase F",
			input:    'F',
			expected: true,
		},
		{
			name:     "lowercase a",
			input:    'a',
			expected: true,
		},
		{
			name:     "lowercase f",
			input:    'f',
			expected: true,
		},
		{
			name:     "invalid G",
			input:    'G',
			expected: false,
		},
		{
			name:     "invalid g",
			input:    'g',
			expected: false,
		},
		{
			name:     "space",
			input:    ' ',
			expected: false,
		},
		{
			name:     "hyphen",
			input:    '-',
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := isHexChar(tt.input)
			if result != tt.expected {
				t.Errorf("Expected isHexChar(%q) = %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}