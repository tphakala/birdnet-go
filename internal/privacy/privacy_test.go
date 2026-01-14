package privacy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBooleanTests runs table-driven tests for functions that take string input and return boolean
func runBooleanTests(t *testing.T, testFunc func(string) bool, tests []struct {
	name     string
	input    string
	expected bool
}) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := testFunc(tt.input)
			assert.Equal(t, tt.expected, result, "for input %q", tt.input)
		})
	}
}

// runScrubTests runs table-driven tests for scrubbing functions that take string input and return string
func runScrubTests(t *testing.T, testFunc func(string) string, tests []struct {
	name     string
	input    string
	expected string
}) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := testFunc(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScrubMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		contains    []string // strings that should be in the output
		notContains []string // strings that should NOT be in the output
	}{
		{
			name:        "Basic RTSP URL with credentials",
			input:       "Failed to connect to rtsp://admin:password@192.168.1.100:554/stream1",
			contains:    []string{"Failed to connect to url-"},
			notContains: []string{"admin", "password", "192.168.1.100"},
		},
		{
			name:        "HTTP URL with domain",
			input:       "Error fetching http://example.com/api/v1/data",
			contains:    []string{"Error fetching url-"},
			notContains: []string{"example.com"},
		},
		{
			name:        "Multiple URLs in message",
			input:       "Failed rtsp://user:pass@cam1.local/stream and https://api.service.com/upload",
			contains:    []string{"Failed url-", "and url-"},
			notContains: []string{"user", "pass", "cam1.local", "api.service.com"},
		},
		{
			name:        "Message with GPS coordinates",
			input:       "Weather fetch failed for location 60.1699,24.9384",
			contains:    []string{"Weather fetch failed for location [LAT],[LON]"},
			notContains: []string{"60.1699", "24.9384"},
		},
		{
			name:        "Message with API token",
			input:       "API call failed with token abc123XYZ789",
			contains:    []string{"API call failed with token [TOKEN]"},
			notContains: []string{"abc123XYZ789"},
		},
		{
			name:        "Complex message with multiple sensitive data",
			input:       "Failed to upload to rtsp://admin:pass@192.168.1.100:554/stream at coordinates 60.1699,24.9384",
			contains:    []string{"Failed to upload to url-", "[LAT],[LON]"},
			notContains: []string{"admin", "pass", "192.168.1.100", "60.1699", "24.9384"},
		},
		{
			name:        "Message without sensitive data",
			input:       "Simple error message without any sensitive information",
			contains:    []string{"Simple error message without any sensitive information"},
			notContains: []string{"url-", "[LAT],[LON]", "[TOKEN]"},
		},
		{
			name:        "Empty message",
			input:       "",
			contains:    []string{""},
			notContains: []string{"url-"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := ScrubMessage(tt.input)

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected, "Expected result to contain %q", expected)
			}

			for _, unexpected := range tt.notContains {
				assert.NotContains(t, result, unexpected, "Expected result to NOT contain %q", unexpected)
			}
		})
	}
}

func TestAnonymizeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		expectPrefix string
	}{
		{
			name:         "RTSP URL with credentials",
			input:        "rtsp://admin:password@192.168.1.100:554/stream1",
			expectPrefix: "url-",
		},
		{
			name:         "HTTP URL with domain",
			input:        "http://example.com/api/data",
			expectPrefix: "url-",
		},
		{
			name:         "HTTPS URL with port",
			input:        "https://secure.example.com:8443/secure/api",
			expectPrefix: "url-",
		},
		{
			name:         "Invalid URL",
			input:        "not-a-valid-url",
			expectPrefix: "url-",
		},
		{
			name:         "Localhost URL",
			input:        "http://localhost:8080/api",
			expectPrefix: "url-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := AnonymizeURL(tt.input)

			assert.True(t, strings.HasPrefix(result, tt.expectPrefix),
				"Expected result to start with %q, but got: %s", tt.expectPrefix, result)

			// Ensure consistent anonymization - same input should produce same output
			result2 := AnonymizeURL(tt.input)
			assert.Equal(t, result, result2, "Expected consistent anonymization")
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
			expected: "rtsp://192.168.1.100:554/stream1/channel1",
		},
		{
			name:     "RTSP URL without credentials but with path",
			input:    "rtsp://192.168.1.100:554/stream1",
			expected: "rtsp://192.168.1.100:554/stream1",
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
		{
			name:     "RTSP URL with query parameters",
			input:    "rtsp://user:pass@192.168.1.100:554/stream?resolution=1080p&bitrate=5000",
			expected: "rtsp://192.168.1.100:554/stream?resolution=1080p&bitrate=5000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := SanitizeRTSPUrl(tt.input)
			assert.Equal(t, tt.expected, result)
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
			expected: "Failed to connect to rtsp://192.168.1.100:554/stream1",
		},
		{
			name:     "Text with multiple RTSP URLs",
			input:    "Primary: rtsp://user:pass@cam1.local/stream Secondary: rtsp://admin:123@cam2.local:8554/live",
			expected: "Primary: rtsp://cam1.local/stream Secondary: rtsp://cam2.local:8554/live",
		},
		{
			name:     "Text with RTSP URL without credentials",
			input:    "Stream available at rtsp://192.168.1.50:554/stream",
			expected: "Stream available at rtsp://192.168.1.50:554/stream",
		},
		{
			name:     "Text without RTSP URLs",
			input:    "No RTSP streams found in the network",
			expected: "No RTSP streams found in the network",
		},
		{
			name:     "RTSP URL with IPv6 address",
			input:    "Connect to rtsp://user:pass@[2001:db8::1]:554/stream",
			expected: "Connect to rtsp://[2001:db8::1]:554/stream",
		},
		{
			name:     "Mixed content with RTSP and HTTP URLs",
			input:    "RTSP: rtsp://admin:pass@cam.local/live HTTP: http://example.com/test",
			expected: "RTSP: rtsp://cam.local/live HTTP: http://example.com/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := SanitizeRTSPUrls(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateSystemID(t *testing.T) {
	t.Parallel()

	// Test multiple generations to ensure they're unique
	ids := make(map[string]bool)
	for range 100 {
		id, err := GenerateSystemID()
		require.NoError(t, err, "GenerateSystemID failed")

		// Check format
		assert.True(t, IsValidSystemID(id), "Generated ID %q is not valid", id)

		// Check uniqueness
		assert.False(t, ids[id], "Duplicate ID generated: %q", id)
		ids[id] = true

		// Check format manually as well
		assert.Len(t, id, 14, "Expected ID length 14 for ID: %q", id)

		assert.Equal(t, byte('-'), id[4], "Expected hyphen at position 4, got: %q", id)
		assert.Equal(t, byte('-'), id[9], "Expected hyphen at position 9, got: %q", id)
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
			assert.Equal(t, tt.valid, result, "IsValidSystemID(%q)", tt.input)
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
			assert.Equal(t, tt.expected, result, "categorizeHost(%q)", tt.input)
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
			assert.Equal(t, tt.expected, result, "IsPrivateIP(%q)", tt.input)
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
			assert.Equal(t, tt.expected, result, "isIPAddress(%q)", tt.input)
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

	runBooleanTests(t, isCommonStreamName, tests)
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

	runBooleanTests(t, isNumeric, tests)
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
			assert.Equal(t, tt.expected, result, "isHexChar(%q)", tt.input)
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
			assert.Equal(t, tt.expected, result, "categorizeDomain(%q)", tt.input)
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

	runScrubTests(t, ScrubCoordinates, tests)
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

	runScrubTests(t, ScrubAPITokens, tests)
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
			assert.Equal(t, tt.expected, result)
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
			assert.Equal(t, tt.expected, result)
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
				assert.Contains(t, result, expected, "Expected result to contain %q", expected)
			}

			for _, unexpected := range tt.notContains {
				assert.NotContains(t, result, unexpected, "Expected result to NOT contain %q", unexpected)
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
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRedactUserAgent_EmptyInput(t *testing.T) {
	t.Parallel()
	assert.Empty(t, RedactUserAgent(""), "Empty input should return empty string")
}

func TestRedactUserAgent_UnknownUserAgent(t *testing.T) {
	t.Parallel()
	result := RedactUserAgent("SomeRandomUserAgent/1.0")
	assert.True(t, strings.HasPrefix(result, "ua-"),
		"Unknown user agent should return hash prefixed with 'ua-', got %q", result)
}

func TestRedactUserAgent_BrowserDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		contains []string
	}{
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
			name:     "Opera browser",
			input:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 OPR/77.0.4054.203",
			contains: []string{"Opera", "Windows"},
		},
		{
			name:     "Linux Firefox",
			input:    "Mozilla/5.0 (X11; Linux x86_64; rv:89.0) Gecko/20100101 Firefox/89.0",
			contains: []string{"Firefox", "Linux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := RedactUserAgent(tt.input)

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected,
					"Result should contain %q for browser/OS detection", expected)
			}
		})
	}
}

func TestRedactUserAgent_BotDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "Googlebot",
			input: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		},
		{
			name:  "Bingbot",
			input: "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := RedactUserAgent(tt.input)
			assert.Contains(t, result, "Bot", "Bot user agents should be identified as Bot")
		})
	}
}

func TestRedactUserAgent_RemovesVersionInfo(t *testing.T) {
	t.Parallel()

	// Test that version information is stripped from the output
	input := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/91.0.4472.124"
	result := RedactUserAgent(input)

	versionPatterns := []string{"10.0", "91.0", "537.36", "4472.124"}
	for _, pattern := range versionPatterns {
		assert.NotContains(t, result, pattern,
			"Result should not contain version info %q", pattern)
	}
}

func TestSanitizeFFmpegError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "FFmpeg error with memory address prefix",
			input:    "[rtsp @ 0x55a57dbe9980] method DESCRIBE failed: 404 Not Found",
			expected: "method DESCRIBE failed: 404 Not Found",
		},
		{
			name:     "FFmpeg error with different memory address",
			input:    "[rtsp @ 0x55c4076ab980] method DESCRIBE failed: 404 Not Found",
			expected: "method DESCRIBE failed: 404 Not Found",
		},
		{
			name:     "FFmpeg error with RTSP URL and memory address",
			input:    "[rtsp @ 0x55d4a4808980] method DESCRIBE failed: 404 Not Found rtsp://localhost:8554/mystream: Server returned 404 Not Found",
			expected: "method DESCRIBE failed: 404 Not Found rtsp://localhost:8554/mystream: Server returned 404 Not Found",
		},
		{
			name:     "FFmpeg error with credentials in URL",
			input:    "[rtsp @ 0x55d4a4808980] Failed to connect to rtsp://admin:password@192.168.1.100:554/stream1",
			expected: "Failed to connect to rtsp://192.168.1.100:554/stream1",
		},
		{
			name:     "Multiple FFmpeg prefixes in same error",
			input:    "[rtsp @ 0x55a57dbe9980] [tcp @ 0x55a57dbea100] Connection refused",
			expected: "Connection refused",
		},
		{
			name:     "FFmpeg error without memory address",
			input:    "Connection timeout occurred",
			expected: "Connection timeout occurred",
		},
		{
			name:     "FFmpeg error with other protocol prefix",
			input:    "[http @ 0x7f8b2c001200] HTTP error 403 Forbidden",
			expected: "HTTP error 403 Forbidden",
		},
		{
			name:     "Complex FFmpeg error with multiple issues",
			input:    "[rtsp @ 0x55a57dbe9980] method DESCRIBE failed: 404 Not Found\nrtsp://user:pass@camera.local:554/stream1: Server returned 404 Not Found",
			expected: "method DESCRIBE failed: 404 Not Found\nrtsp://camera.local:554/stream1: Server returned 404 Not Found",
		},
	}

	runScrubTests(t, SanitizeFFmpegError, tests)
}

func TestAnonymizePath_EmptyInput(t *testing.T) {
	t.Parallel()
	assert.Empty(t, AnonymizePath(""), "Empty input should return empty string")
}

func TestAnonymizePath_RootPath(t *testing.T) {
	t.Parallel()
	result := AnonymizePath("/")
	assert.Equal(t, "empty-path", result, "Root path should return 'empty-path'")
}

func TestAnonymizePath_Consistency(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/home/user/file.txt",
		"documents/file.txt",
		"C:\\Users\\file.txt",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			result1 := AnonymizePath(path)
			result2 := AnonymizePath(path)
			assert.Equal(t, result1, result2, "AnonymizePath should produce consistent results")
		})
	}
}

func TestAnonymizePath_PathTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		expectPrefix   string
		expectContains []string
	}{
		{
			name:           "Unix absolute path",
			input:          "/home/user/documents/file.txt",
			expectPrefix:   "/",
			expectContains: []string{"path-", ".txt"},
		},
		{
			name:           "Unix relative path",
			input:          "documents/file.txt",
			expectContains: []string{"path-", ".txt"},
		},
		{
			name:           "Windows absolute path",
			input:          "C:\\Users\\John\\Documents\\file.txt",
			expectContains: []string{"path-", ".txt", "\\"},
		},
		{
			name:           "Path with multiple extensions",
			input:          "/var/log/app.log.gz",
			expectPrefix:   "/",
			expectContains: []string{"path-", ".gz"},
		},
		{
			name:           "Path without extension",
			input:          "/usr/bin/executable",
			expectPrefix:   "/",
			expectContains: []string{"path-"},
		},
		{
			name:           "Single segment path",
			input:          "filename.txt",
			expectContains: []string{"path-", ".txt"},
		},
		{
			name:           "Deep nested path",
			input:          "/alpha/beta/gamma/delta/file.go",
			expectPrefix:   "/",
			expectContains: []string{"path-", ".go"},
		},
		{
			name:           "Path with spaces in name",
			input:          "/home/user/my documents/my file.pdf",
			expectPrefix:   "/",
			expectContains: []string{"path-", ".pdf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := AnonymizePath(tt.input)
			require.NotEmpty(t, result, "Result should not be empty")

			if tt.expectPrefix != "" {
				assert.True(t, strings.HasPrefix(result, tt.expectPrefix),
					"Expected result to start with %q, got %q", tt.expectPrefix, result)
			}

			for _, expected := range tt.expectContains {
				assert.Contains(t, result, expected,
					"Expected result to contain %q", expected)
			}
		})
	}
}

func TestAnonymizePath_AnonymizesSegments(t *testing.T) {
	t.Parallel()

	input := "/home/username/documents/secret_file.txt"
	result := AnonymizePath(input)

	// Original segment names should not appear in output
	assert.NotContains(t, result, "home")
	assert.NotContains(t, result, "username")
	assert.NotContains(t, result, "documents")
	assert.NotContains(t, result, "secret_file")
	// Extension should be preserved
	assert.Contains(t, result, ".txt")
}

func TestAnonymizePath_PreservesFileExtensions(t *testing.T) {
	t.Parallel()

	extensions := []string{".txt", ".go", ".py", ".jpg", ".mp3", ".wav", ".json", ".yaml"}

	for _, ext := range extensions {
		t.Run("extension"+ext, func(t *testing.T) {
			t.Parallel()

			input := "/some/path/file" + ext
			result := AnonymizePath(input)

			assert.Contains(t, result, ext,
				"Extension %q should be preserved in result %q", ext, result)
		})
	}
}

func TestAnonymizePath_PreservesPathSeparators(t *testing.T) {
	t.Parallel()

	t.Run("Unix separator", func(t *testing.T) {
		t.Parallel()

		input := "/home/user/file.txt"
		result := AnonymizePath(input)

		assert.Contains(t, result, "/", "Unix separator should be preserved")
		assert.NotContains(t, result, "\\", "Windows separator should not appear in Unix path")
	})

	t.Run("Windows separator", func(t *testing.T) {
		t.Parallel()

		input := "C:\\Users\\file.txt"
		result := AnonymizePath(input)

		assert.Contains(t, result, "\\", "Windows separator should be preserved")
	})
}

// Tests for helper functions

func TestIsAbsolutePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Unix absolute path",
			input:    "/home/user/file.txt",
			expected: true,
		},
		{
			name:     "Unix root",
			input:    "/",
			expected: true,
		},
		{
			name:     "Windows absolute path with drive letter",
			input:    "C:\\Users\\file.txt",
			expected: true,
		},
		{
			name:     "Windows drive letter lowercase",
			input:    "d:\\data",
			expected: true,
		},
		{
			name:     "Relative path",
			input:    "documents/file.txt",
			expected: false,
		},
		{
			name:     "Single filename",
			input:    "file.txt",
			expected: false,
		},
		{
			name:     "Empty path",
			input:    "",
			expected: false,
		},
		{
			name:     "Dot relative path",
			input:    "./file.txt",
			expected: false,
		},
		{
			name:     "Parent relative path",
			input:    "../file.txt",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isAbsolutePath(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetPathSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Unix path",
			input:    "/home/user/file.txt",
			expected: "/",
		},
		{
			name:     "Windows path",
			input:    "C:\\Users\\file.txt",
			expected: "\\",
		},
		{
			name:     "Mixed separators prefers backslash",
			input:    "C:\\Users/mixed/path",
			expected: "\\",
		},
		{
			name:     "No separator defaults to forward slash",
			input:    "filename.txt",
			expected: "/",
		},
		{
			name:     "Empty path defaults to forward slash",
			input:    "",
			expected: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := getPathSeparator(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAnonymizePathSegment(t *testing.T) {
	t.Parallel()

	t.Run("Empty segment returns empty", func(t *testing.T) {
		t.Parallel()
		result := anonymizePathSegment("", false)
		assert.Empty(t, result)
	})

	t.Run("Non-last segment without extension", func(t *testing.T) {
		t.Parallel()
		result := anonymizePathSegment("documents", false)
		assert.True(t, strings.HasPrefix(result, "path-"),
			"Should start with 'path-', got %q", result)
		assert.NotContains(t, result, "documents")
	})

	t.Run("Last segment preserves extension", func(t *testing.T) {
		t.Parallel()
		result := anonymizePathSegment("file.txt", true)
		assert.True(t, strings.HasPrefix(result, "path-"),
			"Should start with 'path-', got %q", result)
		assert.Contains(t, result, ".txt")
		assert.NotContains(t, result, "file")
	})

	t.Run("Non-last segment ignores extension", func(t *testing.T) {
		t.Parallel()
		result := anonymizePathSegment("folder.backup", false)
		// When not last segment, extension is part of the hash
		assert.True(t, strings.HasPrefix(result, "path-"))
		assert.NotContains(t, result, ".backup")
	})

	t.Run("Consistent hashing", func(t *testing.T) {
		t.Parallel()
		result1 := anonymizePathSegment("secret", false)
		result2 := anonymizePathSegment("secret", false)
		assert.Equal(t, result1, result2, "Same input should produce same hash")
	})

	t.Run("Different inputs produce different hashes", func(t *testing.T) {
		t.Parallel()
		result1 := anonymizePathSegment("folder1", false)
		result2 := anonymizePathSegment("folder2", false)
		assert.NotEqual(t, result1, result2, "Different inputs should produce different hashes")
	})
}

func TestIsBot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Googlebot",
			input:    "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			expected: true,
		},
		{
			name:     "Bingbot",
			input:    "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
			expected: true,
		},
		{
			name:     "Crawler",
			input:    "Mozilla/5.0 (compatible; crawler/1.0)",
			expected: true,
		},
		{
			name:     "Spider",
			input:    "SomeSpider/1.0",
			expected: true,
		},
		{
			name:     "Normal Chrome browser",
			input:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/91.0.4472.124",
			expected: false,
		},
		{
			name:     "Normal Firefox browser",
			input:    "Mozilla/5.0 (X11; Linux x86_64; rv:89.0) Gecko/20100101 Firefox/89.0",
			expected: false,
		},
		{
			name:     "Empty user agent",
			input:    "",
			expected: false,
		},
		{
			name:     "Case insensitive BOT",
			input:    "SomeBOT/1.0",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isBot(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractUserAgentComponents(t *testing.T) {
	t.Parallel()

	t.Run("Chrome on Windows without bot", func(t *testing.T) {
		t.Parallel()
		ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/91.0.4472.124"
		result := extractUserAgentComponents(ua, false)

		require.Len(t, result, 2, "Should find browser and OS")
		assert.Contains(t, result, "Chrome")
		assert.Contains(t, result, "Windows")
	})

	t.Run("Firefox on Linux without bot", func(t *testing.T) {
		t.Parallel()
		ua := "Mozilla/5.0 (X11; Linux x86_64; rv:89.0) Gecko/20100101 Firefox/89.0"
		result := extractUserAgentComponents(ua, false)

		require.Len(t, result, 2, "Should find browser and OS")
		assert.Contains(t, result, "Firefox")
		assert.Contains(t, result, "Linux")
	})

	t.Run("With bot prefix", func(t *testing.T) {
		t.Parallel()
		ua := "Mozilla/5.0 (Windows NT 10.0) Chrome/91.0"
		result := extractUserAgentComponents(ua, true)

		require.GreaterOrEqual(t, len(result), 1, "Should have at least Bot")
		assert.Equal(t, "Bot", result[0], "First element should be Bot")
		// Browser should not be added since foundBrowser starts as true
		assert.NotContains(t, result, "Chrome")
	})

	t.Run("Edge prioritized over Chrome", func(t *testing.T) {
		t.Parallel()
		// Edge user agents contain both Chrome and Edge identifiers
		ua := "Mozilla/5.0 (Windows NT 10.0) Chrome/91.0.4472.124 Edg/91.0.864.59"
		result := extractUserAgentComponents(ua, false)

		require.GreaterOrEqual(t, len(result), 1)
		assert.Contains(t, result, "Edge", "Edge should be detected over Chrome")
		assert.NotContains(t, result, "Chrome", "Chrome should not be detected when Edge is present")
	})

	t.Run("Unknown user agent returns empty", func(t *testing.T) {
		t.Parallel()
		ua := "SomeRandomUserAgent/1.0"
		result := extractUserAgentComponents(ua, false)

		assert.Empty(t, result, "Unknown user agent should return empty components")
	})

	t.Run("Safari on iOS", func(t *testing.T) {
		t.Parallel()
		ua := "Mozilla/5.0 (iPhone; CPU iPhone OS 14_6 like Mac OS X) Safari/604.1"
		result := extractUserAgentComponents(ua, false)

		require.Len(t, result, 2)
		assert.Contains(t, result, "Safari")
		assert.Contains(t, result, "iOS")
	})

	t.Run("Opera on Windows", func(t *testing.T) {
		t.Parallel()
		ua := "Mozilla/5.0 (Windows NT 10.0) OPR/77.0.4054.203"
		result := extractUserAgentComponents(ua, false)

		require.Len(t, result, 2)
		assert.Contains(t, result, "Opera")
		assert.Contains(t, result, "Windows")
	})

	t.Run("Android Chrome", func(t *testing.T) {
		t.Parallel()
		ua := "Mozilla/5.0 (Linux; Android 11; SM-G991B) Chrome/91.0.4472.120"
		result := extractUserAgentComponents(ua, false)

		require.Len(t, result, 2)
		assert.Contains(t, result, "Chrome")
		assert.Contains(t, result, "Android")
	})
}

// TestScrubUsername tests the username anonymization function
func TestScrubUsername(t *testing.T) {
	t.Parallel()

	t.Run("empty username returns marker", func(t *testing.T) {
		t.Parallel()
		result := ScrubUsername("")
		assert.Equal(t, EmptyUserMarker, result)
	})

	t.Run("normal username returns hash prefix", func(t *testing.T) {
		t.Parallel()
		result := ScrubUsername("admin")
		assert.True(t, strings.HasPrefix(result, "user-"), "expected prefix 'user-', got: %s", result)
		assert.Len(t, result, 13) // "user-" (5) + 8 hex chars
	})

	t.Run("same username produces same hash", func(t *testing.T) {
		t.Parallel()
		result1 := ScrubUsername("testuser")
		result2 := ScrubUsername("testuser")
		assert.Equal(t, result1, result2, "same username should produce same hash")
	})

	t.Run("different usernames produce different hashes", func(t *testing.T) {
		t.Parallel()
		result1 := ScrubUsername("user1")
		result2 := ScrubUsername("user2")
		assert.NotEqual(t, result1, result2, "different usernames should produce different hashes")
	})

	t.Run("special characters in username", func(t *testing.T) {
		t.Parallel()
		result := ScrubUsername("user@domain.com")
		assert.True(t, strings.HasPrefix(result, "user-"), "expected prefix 'user-', got: %s", result)
	})
}

// TestScrubPassword tests the password redaction function
func TestScrubPassword(t *testing.T) {
	t.Parallel()

	t.Run("empty password returns empty marker", func(t *testing.T) {
		t.Parallel()
		result := ScrubPassword("")
		assert.Equal(t, EmptyPasswordMarker, result)
	})

	t.Run("non-empty password returns redacted marker", func(t *testing.T) {
		t.Parallel()
		result := ScrubPassword("supersecret123")
		assert.Equal(t, RedactedMarker, result)
	})

	t.Run("short password returns redacted marker", func(t *testing.T) {
		t.Parallel()
		result := ScrubPassword("x")
		assert.Equal(t, RedactedMarker, result)
	})
}

// TestScrubToken tests the token redaction function
func TestScrubToken(t *testing.T) {
	t.Parallel()

	t.Run("empty token returns empty marker", func(t *testing.T) {
		t.Parallel()
		result := ScrubToken("")
		assert.Equal(t, EmptyTokenMarker, result)
	})

	t.Run("short token shows length", func(t *testing.T) {
		t.Parallel()
		result := ScrubToken("abc123")
		assert.Equal(t, "[TOKEN:len=6]", result)
	})

	t.Run("long token shows length", func(t *testing.T) {
		t.Parallel()
		longToken := "test_token_abcdefghij1234567890abcdefghij123456"
		result := ScrubToken(longToken)
		assert.Equal(t, "[TOKEN:len=47]", result)
	})
}

// TestScrubCredentialURL tests URL credential scrubbing
func TestScrubCredentialURL(t *testing.T) {
	t.Parallel()

	t.Run("empty URL returns empty string", func(t *testing.T) {
		t.Parallel()
		result := ScrubCredentialURL("")
		assert.Empty(t, result)
	})

	t.Run("URL with userinfo is scrubbed", func(t *testing.T) {
		t.Parallel()
		result := ScrubCredentialURL("https://user:password@example.com/path")
		// URL encoding converts [REDACTED] to %5BREDACTED%5D
		assert.True(t, strings.Contains(result, "[REDACTED]") || strings.Contains(result, "%5BREDACTED%5D"),
			"expected [REDACTED] or URL-encoded version, got: %s", result)
		assert.NotContains(t, result, "password")
	})

	t.Run("telegram bot token is scrubbed", func(t *testing.T) {
		t.Parallel()
		result := ScrubCredentialURL("telegram://api.telegram.org/bot123456789:ABCdefGHI-jklMNOpqrSTUvwxYZ/sendMessage")
		assert.Contains(t, result, "/bot[TOKEN]/")
		assert.NotContains(t, result, "123456789:ABCdefGHI")
	})

	t.Run("discord webhook is scrubbed", func(t *testing.T) {
		t.Parallel()
		result := ScrubCredentialURL("https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ01234567890")
		assert.Contains(t, result, "[WEBHOOK_ID]")
		assert.Contains(t, result, "[TOKEN]")
	})

	t.Run("simple URL without credentials unchanged", func(t *testing.T) {
		t.Parallel()
		result := ScrubCredentialURL("https://example.com/api/v1")
		assert.Equal(t, "https://example.com/api/v1", result)
	})
}

// TestSanitizeStreamUrl tests the multi-protocol URL sanitization function
func TestSanitizeStreamUrl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// RTSP protocol
		{
			name:     "RTSP with credentials",
			input:    "rtsp://admin:password@192.168.1.100:554/stream",
			expected: "rtsp://192.168.1.100:554/stream",
		},
		{
			name:     "RTSPS with credentials",
			input:    "rtsps://user:secret@secure.camera.local/stream",
			expected: "rtsps://secure.camera.local/stream",
		},
		{
			name:     "RTSP without credentials",
			input:    "rtsp://192.168.1.100:554/stream",
			expected: "rtsp://192.168.1.100:554/stream",
		},

		// HTTP protocol
		{
			name:     "HTTP with basic auth",
			input:    "http://user:pass@stream.example.com/live",
			expected: "http://stream.example.com/live",
		},
		{
			name:     "HTTPS with basic auth",
			input:    "https://apiuser:apikey@api.example.com/stream",
			expected: "https://api.example.com/stream",
		},
		{
			name:     "HTTP without credentials",
			input:    "http://stream.example.com/live",
			expected: "http://stream.example.com/live",
		},

		// RTMP protocol
		{
			name:     "RTMP with credentials",
			input:    "rtmp://broadcaster:streamkey@live.example.com/app/stream",
			expected: "rtmp://live.example.com/app/stream",
		},
		{
			name:     "RTMPS with credentials",
			input:    "rtmps://user:key@secure.live.com/app/stream",
			expected: "rtmps://secure.live.com/app/stream",
		},
		{
			name:     "RTMP without credentials",
			input:    "rtmp://live.example.com/app/stream",
			expected: "rtmp://live.example.com/app/stream",
		},

		// UDP protocol
		{
			name:     "UDP multicast no credentials",
			input:    "udp://239.0.0.1:1234",
			expected: "udp://239.0.0.1:1234",
		},
		{
			name:     "UDP with options",
			input:    "udp://@239.0.0.1:1234?pkt_size=1316",
			expected: "udp://239.0.0.1:1234?pkt_size=1316", // @ is stripped as it's parsed as empty username
		},
		{
			name:     "RTP stream",
			input:    "rtp://239.0.0.1:5004",
			expected: "rtp://239.0.0.1:5004",
		},

		// HLS protocol (typically uses query params for auth)
		{
			name:     "HLS with auth token in URL credentials",
			input:    "https://apikey:secret@cdn.example.com/playlist.m3u8",
			expected: "https://cdn.example.com/playlist.m3u8",
		},
		{
			name:     "HLS without credentials",
			input:    "https://cdn.example.com/playlist.m3u8?token=abc123",
			expected: "https://cdn.example.com/playlist.m3u8?token=abc123",
		},

		// Edge cases
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Non-URL string",
			input:    "not a url",
			expected: "not a url",
		},
		{
			name:     "URL with special characters in password",
			input:    "rtsp://admin:p%40ss%2Fw0rd@192.168.1.100/stream",
			expected: "rtsp://192.168.1.100/stream",
		},
		{
			name:     "URL with port and path",
			input:    "http://user:pass@example.com:8080/path/to/stream",
			expected: "http://example.com:8080/path/to/stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := SanitizeStreamUrl(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSanitizeStreamUrl_CredentialRemoval verifies credentials are properly removed
func TestSanitizeStreamUrl_CredentialRemoval(t *testing.T) {
	t.Parallel()

	sensitiveTests := []struct {
		name       string
		input      string
		mustRemove []string // These strings must NOT appear in output
	}{
		{
			name:       "RTSP admin credentials",
			input:      "rtsp://admin:SuperSecret123@192.168.1.100/stream",
			mustRemove: []string{"admin", "SuperSecret123", "admin:SuperSecret123"},
		},
		{
			name:       "HTTP API key",
			input:      "http://apiuser:sk-abcdef123456@api.service.com/stream",
			mustRemove: []string{"apiuser", "sk-abcdef123456"},
		},
		{
			name:       "RTMP stream key",
			input:      "rtmp://streamer:live_key_xyz789@ingest.stream.com/live/mystream",
			mustRemove: []string{"streamer", "live_key_xyz789"},
		},
		{
			name:       "Complex password with symbols",
			input:      "rtsp://user:P@$$w0rd!#$@camera.local/stream",
			mustRemove: []string{"user:", "P@$$w0rd!#$"},
		},
	}

	for _, tt := range sensitiveTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := SanitizeStreamUrl(tt.input)

			for _, sensitive := range tt.mustRemove {
				assert.NotContains(t, result, sensitive,
					"Sanitized URL should not contain '%s'", sensitive)
			}
		})
	}
}

// TestSanitizeStreamUrlBackwardCompatibility verifies SanitizeRTSPUrl works as wrapper
func TestSanitizeStreamUrlBackwardCompatibility(t *testing.T) {
	t.Parallel()

	testCases := []string{
		"rtsp://admin:pass@192.168.1.100/stream",
		"http://user:pass@example.com/live",
		"rtmp://key:secret@live.com/app/stream",
		"https://api:token@cdn.example.com/playlist.m3u8",
	}

	for _, input := range testCases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			// Both functions should return the same result
			rtspResult := SanitizeRTSPUrl(input)
			streamResult := SanitizeStreamUrl(input)

			assert.Equal(t, streamResult, rtspResult,
				"SanitizeRTSPUrl should return same result as SanitizeStreamUrl")
		})
	}
}
