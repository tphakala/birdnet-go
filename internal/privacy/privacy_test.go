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
			name:     "Message with GPS coordinates",
			input:    "Weather fetch failed for location 60.1699,24.9384",
			contains: []string{"Weather fetch failed for location [LAT],[LON]"},
			notContains: []string{"60.1699", "24.9384"},
		},
		{
			name:     "Message with API token",
			input:    "API call failed with token abc123XYZ789",
			contains: []string{"API call failed with token [TOKEN]"},
			notContains: []string{"abc123XYZ789"},
		},
		{
			name:     "Complex message with multiple sensitive data",
			input:    "Failed to upload to rtsp://admin:pass@192.168.1.100:554/stream at coordinates 60.1699,24.9384",
			contains: []string{"Failed to upload to url-", "[LAT],[LON]"},
			notContains: []string{"admin", "pass", "192.168.1.100", "60.1699", "24.9384"},
		},
		{
			name:     "Message without sensitive data",
			input:    "Simple error message without any sensitive information",
			contains: []string{"Simple error message without any sensitive information"},
			notContains: []string{"url-", "[LAT],[LON]", "[TOKEN]"},
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

func TestSanitizeRTSPUrls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Text with single RTSP URL with credentials",
			input:    "Failed to connect to rtsp://admin:password@192.168.1.100:554/stream1",
			expected: "Failed to connect to rtsp://192.168.1.100:554",
		},
		{
			name:     "Text with multiple RTSP URLs",
			input:    "Primary: rtsp://user:pass@cam1.local/stream Secondary: rtsp://admin:123@cam2.local:8554/live",
			expected: "Primary: rtsp://cam1.local Secondary: rtsp://cam2.local:8554",
		},
		{
			name:     "Text with RTSP URL without credentials",
			input:    "Stream available at rtsp://192.168.1.50:554/stream",
			expected: "Stream available at rtsp://192.168.1.50:554",
		},
		{
			name:     "Text without RTSP URLs",
			input:    "No RTSP streams found in the network",
			expected: "No RTSP streams found in the network",
		},
		{
			name:     "RTSP URL with IPv6 address",
			input:    "Connect to rtsp://user:pass@[2001:db8::1]:554/stream",
			expected: "Connect to rtsp://[2001:db8::1]:554",
		},
		{
			name:     "Mixed content with RTSP and HTTP URLs",
			input:    "RTSP: rtsp://admin:pass@cam.local/live HTTP: http://example.com/test",
			expected: "RTSP: rtsp://cam.local HTTP: http://example.com/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := SanitizeRTSPUrls(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeRTSPUrls() = %q, want %q", result, tt.expected)
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
			name:     "Two-part TLD .co.uk",
			input:    "example.co.uk",
			expected: "domain-co.uk",
		},
		{
			name:     "Subdomain with two-part TLD",
			input:    "sub.example.co.uk",
			expected: "domain-co.uk",
		},
		{
			name:     "Australian domain",
			input:    "test.com.au",
			expected: "domain-com.au",
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
			
			result := IsPrivateIP(tt.input)
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
			expected: false, // net.ParseIP validates ranges correctly
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

func TestCategorizeDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple .com domain",
			input:    "example.com",
			expected: "domain-com",
		},
		{
			name:     "Two-part TLD .co.uk",
			input:    "example.co.uk",
			expected: "domain-co.uk",
		},
		{
			name:     "Government domain .gov.uk",
			input:    "cabinet-office.gov.uk",
			expected: "domain-gov.uk",
		},
		{
			name:     "Australian .com.au",
			input:    "company.com.au",
			expected: "domain-com.au",
		},
		{
			name:     "Academic .ac.uk",
			input:    "university.ac.uk",
			expected: "domain-ac.uk",
		},
		{
			name:     "Subdomain with two-part TLD",
			input:    "sub.example.co.uk",
			expected: "domain-co.uk",
		},
		{
			name:     "Regular .org domain",
			input:    "nonprofit.org",
			expected: "domain-org",
		},
		{
			name:     "Single part (invalid)",
			input:    "localhost",
			expected: "unknown-host",
		},
		{
			name:     "Japanese .co.jp",
			input:    "company.co.jp",
			expected: "domain-co.jp",
		},
		{
			name:     "New Zealand .co.nz",
			input:    "business.co.nz",
			expected: "domain-co.nz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := categorizeDomain(tt.input)
			if result != tt.expected {
				t.Errorf("Expected categorizeDomain(%q) = %q, got %q", tt.input, tt.expected, result)
			}
		})
	}
}

func TestScrubCoordinates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Decimal coordinates with comma",
			input:    "Location 60.1699,24.9384",
			expected: "Location [LAT],[LON]",
		},
		{
			name:     "Coordinates with lat/lng labels",
			input:    "lat=60.1699 lng=24.9384",
			expected: "[LAT],[LON]",
		},
		{
			name:     "Coordinates with latitude/longitude labels",
			input:    "latitude: 60.1699, longitude: 24.9384",
			expected: "[LAT],[LON]",
		},
		{
			name:     "Negative coordinates",
			input:    "Position -45.123,122.456",
			expected: "Position [LAT],[LON]",
		},
		{
			name:     "Coordinates with spaces",
			input:    "GPS 60.1699 24.9384",
			expected: "GPS [LAT],[LON]",
		},
		{
			name:     "Multiple coordinate pairs",
			input:    "From 60.1699,24.9384 to 61.4981,23.7610",
			expected: "From [LAT],[LON] to [LAT],[LON]",
		},
		{
			name:     "No coordinates",
			input:    "Normal message without coordinates",
			expected: "Normal message without coordinates",
		},
		{
			name:     "Integer coordinates",
			input:    "Location 60,24",
			expected: "Location [LAT],[LON]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubCoordinates(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, but got %q", tt.expected, result)
			}
		})
	}
}

func TestScrubAPITokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "API key with colon",
			input:    "api_key: abc123XYZ789",
			expected: "api_key: [TOKEN]",
		},
		{
			name:     "Token with equals",
			input:    "token=abc123XYZ789+/==",
			expected: "token: [TOKEN]",
		},
		{
			name:     "API-key with hyphen (no space)",
			input:    "api-key abc123XYZ789",
			expected: "api-key abc123XYZ789",
		},
		{
			name:     "Secret token",
			input:    "secret: abc123XYZ789+/",
			expected: "secret: [TOKEN]",
		},
		{
			name:     "Auth token",
			input:    "auth=abc123XYZ789",
			expected: "auth: [TOKEN]",
		},
		{
			name:     "Multiple tokens",
			input:    "Using api_key: abc123token and token=xyz789token",
			expected: "Using api_key: [TOKEN] and token: [TOKEN]",
		},
		{
			name:     "No tokens",
			input:    "Normal message without tokens",
			expected: "Normal message without tokens",
		},
		{
			name:     "Short token (should not match)",
			input:    "api_key: abc123",
			expected: "api_key: abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubAPITokens(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, but got %q", tt.expected, result)
			}
		})
	}
}



func TestScrubEmails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple email",
			input:    "Contact user@example.com for help",
			expected: "Contact [EMAIL] for help",
		},
		{
			name:     "Multiple emails",
			input:    "Send from admin@company.com to support@customer.org",
			expected: "Send from [EMAIL] to [EMAIL]",
		},
		{
			name:     "Email with dots",
			input:    "john.doe@example.co.uk is the contact",
			expected: "[EMAIL] is the contact",
		},
		{
			name:     "No email",
			input:    "No email address here",
			expected: "No email address here",
		},
		{
			name:     "Email with numbers",
			input:    "user123@test456.com sent message",
			expected: "[EMAIL] sent message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubEmails(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, but got %q", tt.expected, result)
			}
		})
	}
}

func TestScrubUUIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard UUID",
			input:    "Request ID: 550e8400-e29b-41d4-a716-446655440000",
			expected: "Request ID: [UUID]",
		},
		{
			name:     "UUID in URL",
			input:    "Access /api/v1/users/123e4567-e89b-12d3-a456-426614174000/profile",
			expected: "Access /api/v1/users/[UUID]/profile",
		},
		{
			name:     "Multiple UUIDs",
			input:    "From a1b2c3d4-e5f6-47a8-b9c0-d1e2f3a4b5c6 to 98765432-1234-5678-9abc-def012345678",
			expected: "From [UUID] to [UUID]",
		},
		{
			name:     "No UUID",
			input:    "No identifiers in this message",
			expected: "No identifiers in this message",
		},
		{
			name:     "Uppercase UUID",
			input:    "ID: 550E8400-E29B-41D4-A716-446655440000",
			expected: "ID: [UUID]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubUUIDs(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, but got %q", tt.expected, result)
			}
		})
	}
}

func TestScrubStandaloneIPs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		contains    []string
		notContains []string
	}{
		{
			name:        "Standalone IPv4",
			input:       "Server at 192.168.1.100 is down",
			contains:    []string{"Server at", "is down"},
			notContains: []string{"192.168.1.100"},
		},
		{
			name:        "IP in URL should not be replaced",
			input:       "Connect to https://192.168.1.100:8080/api",
			contains:    []string{"Connect to", "192.168.1.100"},
			notContains: []string{"private-ip"},
		},
		{
			name:        "Multiple standalone IPs",
			input:       "From 10.0.1.50 to 10.0.1.60",
			contains:    []string{"From", "to", "private-ip-"},
			notContains: []string{"10.0.1.50", "10.0.1.60"},
		},
		{
			name:        "IPv6 address",
			input:       "IPv6 address 2001:db8::1 is reachable",
			contains:    []string{"IPv6 address", "is reachable", "public-ip-"},
			notContains: []string{"2001:db8::1"},
		},
		{
			name:        "Mixed IPs and URLs",
			input:       "Server 192.168.1.1 connects to http://10.0.0.1:80/status",
			contains:    []string{"Server", "private-ip-", "connects to", "10.0.0.1"},
			notContains: []string{"192.168.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubStandaloneIPs(tt.input)
			
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

func TestBearerTokenScrubbing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bearer token",
			input:    "Authorization: Bearer abc123XYZ789",
			expected: "Authorization: Bearer [TOKEN]",
		},
		{
			name:     "Bearer lowercase",
			input:    "Using bearer token abc123XYZ789 for auth",
			expected: "Using Bearer [TOKEN] for auth",
		},
		{
			name:     "Mixed case Bearer",
			input:    "BEARER=abc123XYZ789 in header",
			expected: "Bearer [TOKEN] in header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			result := ScrubAPITokens(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, but got %q", tt.expected, result)
			}
		})
	}
}

func TestRedactUserAgent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		input    string
		expected string
		contains []string // Elements that should be in the output
	}{
		{
			name:     "Empty user agent",
			input:    "",
			expected: "",
		},
		{
			name:     "Chrome on Windows",
			input:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
			contains: []string{"Chrome", "Windows"},
		},
		{
			name:     "Firefox on Mac",
			input:    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Safari/605.1.15 Firefox/89.0",
			contains: []string{"Firefox", "Mac"},
		},
		{
			name:     "Safari on iOS",
			input:    "Mozilla/5.0 (iPhone; CPU iPhone OS 14_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/604.1",
			contains: []string{"Safari", "iOS"},
		},
		{
			name:     "Edge on Windows",
			input:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 Edg/91.0.864.59",
			contains: []string{"Edge", "Windows"},
		},
		{
			name:     "Android Chrome",
			input:    "Mozilla/5.0 (Linux; Android 11; SM-G991B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.120 Mobile Safari/537.36",
			contains: []string{"Chrome", "Android"},
		},
		{
			name:     "Bot user agent",
			input:    "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			contains: []string{"Bot"},
		},
		{
			name:     "Unknown user agent",
			input:    "SomeRandomUserAgent/1.0",
			expected: "ua-", // Should start with ua- followed by hash
		},
		{
			name:     "Opera browser",
			input:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 OPR/77.0.4054.203",
			contains: []string{"Opera", "Windows"},
		},
		{
			name:     "Linux Firefox",
			input:    "Mozilla/5.0 (X11; Linux x86_64; rv:89.0) Gecko/20100101 Firefox/89.0",
			contains: []string{"Firefox", "Linux"},
		},
		{
			name:     "Crawler",
			input:    "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
			contains: []string{"Bot"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			
			result := RedactUserAgent(tc.input)
			
			// Check exact match if expected is specified
			if tc.expected != "" {
				if tc.expected == "ua-" {
					// For unknown user agents, just check the prefix
					if !strings.HasPrefix(result, tc.expected) {
						t.Errorf("Expected result to start with %q, got %q", tc.expected, result)
					}
				} else if result != tc.expected {
					t.Errorf("Expected %q, got %q", tc.expected, result)
				}
			}
			
			// Check that expected components are present
			for _, component := range tc.contains {
				if !strings.Contains(result, component) {
					t.Errorf("Expected result to contain %q, got %q", component, result)
				}
			}
			
			// Ensure no version numbers are present
			if tc.input != "" && tc.expected != "" {
				// Check that common version patterns are removed
				versionPatterns := []string{
					"10.0", "91.0", "14.6", "11", "89.0", "77.0",
					"Windows NT", "Mac OS X", "Android",
					"AppleWebKit", "Gecko", "KHTML",
				}
				for _, pattern := range versionPatterns {
					if strings.Contains(tc.input, pattern) && strings.Contains(result, pattern) {
						t.Errorf("Result should not contain version info %q, got %q", pattern, result)
					}
				}
			}
		})
	}
}
