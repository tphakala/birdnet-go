package privacy

import (
	"strconv"
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

func TestScrubQueryString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rawQuery    string
		wantEmpty   bool
		contains    []string
		notContains []string
	}{
		{
			name:      "empty query stays empty",
			rawQuery:  "",
			wantEmpty: true,
		},
		{
			name:        "plain token value is redacted",
			rawQuery:    "token=secretvalue123&foo=bar",
			contains:    []string{"[TOKEN]", "foo=bar"},
			notContains: []string{"secretvalue123"},
		},
		{
			name:        "percent-encoded token value is redacted after decoding",
			rawQuery:    "token=ab%2Bcd1234567890",
			contains:    []string{"[TOKEN]"},
			notContains: []string{"ab%2Bcd1234567890", "ab+cd1234567890", "1234567890"},
		},
		{
			// A literal '+' in a base64 token must stay part of the token value:
			// url.PathUnescape preserves it (QueryUnescape would turn it into a space
			// and split the value, leaking the tail past the scrubber).
			name:        "literal plus in token value is redacted not split on a space",
			rawQuery:    "token=ab+cd1234567890",
			contains:    []string{"[TOKEN]"},
			notContains: []string{"ab+cd1234567890", "ab cd1234567890", "1234567890"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ScrubQueryString(tt.rawQuery)
			if tt.wantEmpty {
				assert.Empty(t, got)
				return
			}
			for _, expected := range tt.contains {
				assert.Contains(t, got, expected)
			}
			for _, unexpected := range tt.notContains {
				assert.NotContainsf(t, got, unexpected, "secret %q must be scrubbed", unexpected)
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

func TestScrubCoordinates_KeepsALSASelectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"hw selector in a device name", "Blue Yeti Stereo Microphone: USB Audio (hw:1,0)", "Blue Yeti Stereo Microphone: USB Audio (hw:1,0)"},
		{"plughw selector", "plughw:1,0", "plughw:1,0"},
		{"dsnoop selector", "dsnoop:1,0", "dsnoop:1,0"},
		{"dmix selector", "dmix:1,0", "dmix:1,0"},
		{"two digit card and device", "hw:10,12", "hw:10,12"},
		{"selector with subdevice", "hw:1,0,0", "hw:1,0,0"},
		{"pair after a bare colon", "device :1,0", "device :[LAT],[LON]"},
		{"whole degree pair after a bare colon", "pos :60,24", "pos :[LAT],[LON]"},
		{"quoted bare colon value", `"device":":1,0"`, `"device":":[LAT],[LON]"`},
		{"selector then coordinates", "hw:1,0 at 60.1699,24.9384", "hw:1,0 at [LAT],[LON]"},
		{"decimal pair after hw", "hw:60.1699,24.9384", "hw:[LAT],[LON]"},
		{"decimal second half after hw", "hw:1,24.9384", "hw:[LAT],[LON]"},
		{"space before second half after hw", "hw:1, 24.9384", "hw:[LAT],[LON]"},
		{"signed second half after hw", "hw:1,-2", "hw:[LAT],[LON]"},
		{"signed decimal pair after a colon", ":45.5,-120.5", ":[LAT],[LON]"},
		{"labelled integer pair", "Location:60,24", "Location:[LAT],[LON]"},
		{"quoted key before the colon", `{"lat":60,24}`, `{"lat":[LAT],[LON]}`},
		{"hw prefix inside a word", "myhw:60,24", "myhw:[LAT],[LON]"},
	}

	runScrubTests(t, ScrubCoordinates, tests)
}

func TestScrubMessage_KeepsDatesAndTimes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"journal date time", "2026-10-16 00:12:34"},
		{"date time with fraction", "2026-10-16 00:12:34.015573"},
		{"syslog prefix", "Oct 16 00:12:34 host birdnet-go[1]: started"},
		{"date time in a sentence", "restart at 2026-10-16 23:59:01 after crash"},
		{"clock times", "from 10:15:30 to 11:45:00"},
		{"dotted date time", "16.10.2026 00:12 started"},
		{"date time with T", "2026-10-16T00:12:34 started"},
		{"US slash date time", "10/16/2026 00:12:34 started"},
		{"European slash date time", "16/10/2026 00:12:34"},
		{"year first slash date time", "2026/10/16 00:12:34"},
		{"two digit year slash date time", "10/16/26 00:12:34"},
		{"slash date time with T and Z", "10/16/2026T00:12:34Z"},
		{"slash date time with fraction", "10/16/2026 12:34:56.123"},
		{"slash date in a sentence", "see 10/16/2026"},
		{"year first slash date alone", "2026/10/16 started"},
		{"slash date time after comma", "10/16/2026, 00:12:34 x"},
		{"slash date before a count", "2026/10/16 50 birds"},
		{"slash date ending a sentence", "backup done 2026/10/16 started. Next run tomorrow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.input, ScrubMessage(tt.input))
		})
	}
}

func TestScrubCoordinates_StillScrubsWithNeighbors(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"coords:60.1699,24.9384",
		"Lat:60.1699,24.9384",
		"error at 60.1699,24.9384: timeout",
		"60.1699,24.9384N",
		"60.1699, 24.9384m away",
		"pos=60.1699,24.9384_x",
		"user_lat=60.1699 lon=24.9384",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got := ScrubCoordinates(in)
			assert.NotContains(t, got, "60.1699")
			assert.NotContains(t, got, "24.9384")
		})
	}
}

func TestScrubCoordinates_ScrubsPairAfterALeadingNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"count and comma", "count 5, 60.1699,24.9384", "count [LAT],[LON]"},
		{"bare run", "5, 60.1699, 24.9384", "[LAT],[LON]"},
		{"glued integer", "x 1,60.1699,24.9384", "x [LAT],[LON]"},
		{"count and space", "count 5 60.1699,24.9384", "count [LAT],[LON]"},
		{"space only", "count 5 60.1699 24.9384", "count [LAT],[LON]"},
		{"negative values", "5,-33.86,151.21", "[LAT],[LON]"},
		{"decimal first", "0.5, 60.1699, 24.9384", "[LAT],[LON]"},
		{"listed pairs are not merged", "60.1699,24.9384, 61.4981,23.7610", "[LAT],[LON], [LAT],[LON]"},
		{"long tail number is consumed whole", "60.1699, 24.9384, 1000.5", "[LAT],[LON]"},
		{"kept selector, then a pair", "hw:1,0,0 60.1699,24.9384", "hw:1,0,[LAT],[LON]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ScrubCoordinates(tt.input))
		})
	}

	// No digit of a number run survives, whatever the run length and separator.
	numbers := []string{"5", "60.1699", "-24.9384", "0.5", "151", "-33.86"}
	for runLen := 2; runLen <= len(numbers); runLen++ {
		for _, sep := range []string{",", ", ", " "} {
			input := "count " + strings.Join(numbers[:runLen], sep) + " end"
			assert.NotRegexp(t, `\d`, ScrubCoordinates(input), "input %q", input)
		}
	}
}

func TestScrubCoordinates_ScrubsPairRightAfterTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"date time then pair", "2026-10-16 00:12:34 60.1699,24.9384", "2026-10-16 00:12:34 [LAT],[LON]"},
		{"syslog then pair", "Oct 16 00:12:34 60.1699,24.9384", "Oct 16 00:12:34 [LAT],[LON]"},
		{"fractional seconds", "2026-10-16 00:12:34.015573 60.1699,24.9384", "2026-10-16 00:12:34.015573 [LAT],[LON]"},
		{"comma milliseconds", "2026-10-16 00:12:34,123 60.1699,24.9384", "2026-10-16 00:12:34,123 [LAT],[LON]"},
		{"syslog fraction", "Oct 16 00:12:34.567 60.1699,24.9384", "Oct 16 00:12:34.567 [LAT],[LON]"},
		{"RFC3339 offset", "2026-10-16T00:12:34+02:00 60.1699,24.9384", "2026-10-16T00:12:34+02:00 [LAT],[LON]"},
		{"spaced numeric offset", "2026-10-16 00:12:34 +0200 60.1699,24.9384", "2026-10-16 00:12:34 +0200 [LAT],[LON]"},
		{"spaced negative offset", "2026-10-16 00:12:34 -0500 60.1699,24.9384", "2026-10-16 00:12:34 -0500 [LAT],[LON]"},
		{"comma pair right after seconds", "2026-10-16 00:12:34,60.1699 24.9384", "2026-10-16 00:12:34,[LAT],[LON]"},
		{"long comma fraction", "2026-10-16 00:12:34,015573 60.1699,24.9384", "2026-10-16 00:12:34,015573 [LAT],[LON]"},
		{"negative latitude glued to time", "2026-10-16T00:12:34-45.12,122.4", "2026-10-16T00:12:34[LAT],[LON]"},
		{"longitude first right after time", "2026-10-16 00:12:34,151.2093,-33.8688", "2026-10-16 00:12:34,[LAT],[LON]"},
		{"zero padded pair right after time", "2026-10-16 00:12:34,060.1699,24.9384", "2026-10-16 00:12:34,[LAT],[LON]"},
		{"syslog longitude first", "Oct 16 00:12:34,151.2093,-33.8688", "Oct 16 00:12:34,[LAT],[LON]"},
		{"sentence period after fraction", "2026-10-16 00:12:34,123. 50 birds", "2026-10-16 00:12:34,123. 50 birds"},
		{"Z suffix", "2026-10-16T00:12:34Z 60.1699,24.9384", "2026-10-16T00:12:34Z [LAT],[LON]"},
		{"negative pair after time", "2026-10-16 00:12:34 -45.123,122.456", "2026-10-16 00:12:34 [LAT],[LON]"},
		{"comma before time", "10/16/2026, 00:12:34 60.1699,24.9384", "10/16/2026, 00:12:34 [LAT],[LON]"},
		{"pair after date shaped prefix", "1/2/60.1699,24.9384", "1/2/[LAT],[LON]"},
		{"whole degree latitude glued to a date shape", "10/16/60,24.9384", "10/16/[LAT],[LON]"},
		{"pair after slash date", "10/16/2026, 60.1699,24.9384", "10/16/2026, [LAT],[LON]"},
		{"pair between timestamps", "2026-10-16 00:12:34 at 60.1699,24.9384 until 2026-10-16 00:13:00", "2026-10-16 00:12:34 at [LAT],[LON] until 2026-10-16 00:13:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ScrubCoordinates(tt.input))
		})
	}
}

func TestTrimGluedCoordinate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		start   int
		end     int
		want    int
	}{
		{"comma fraction before a dot", "00:12:34,151.2", 0, 12, 8},
		{"comma fraction before a sentence period", "00:12:34,123. 50 birds", 0, 12, 12},
		{"comma fraction before a final period", "00:12:34,123.", 0, 12, 12},
		{"comma fraction at end of text", "00:12:34,123", 0, 12, 12},
		{"dot fraction before a dot", "00:12:34.5.7", 0, 10, 10},
		{"non-digit after the comma", "00:12:34,123Z.5", 0, 13, 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, trimGluedCoordinate(tt.message, tt.start, tt.end))
		})
	}
}

func TestProtectedDateTimeSpans_MergesTouchingSpans(t *testing.T) {
	t.Parallel()
	// The date-time ends exactly where the syslog prefix starts.
	in := "2026-10-16 00:12:34Oct 16 00:12:35"
	assert.Equal(t, [][]int{{0, len(in)}}, protectedDateTimeSpans(in))
	assert.Equal(t, in, ScrubCoordinates(in))
}

func TestScrubCoordinates_LabelIsNotASyslogMonth(t *testing.T) {
	t.Parallel()
	got := ScrubCoordinates("lat 60 12:34")
	assert.NotContains(t, got, "lat 60")
}

func TestScrubCoordinates_StillScrubsBesideTimestamps(t *testing.T) {
	t.Parallel()
	got := ScrubCoordinates("2026-10-16 00:12:34 weather for 60.1699,24.9384 failed")
	assert.Equal(t, "2026-10-16 00:12:34 weather for [LAT],[LON] failed", got)
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

func TestScrubMessage_ScrubbsFilePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unix absolute path with username",
			input: "failed to open /home/john/.config/birdnet-go/birdnet.db: permission denied",
		},
		{
			name:  "windows path with username",
			input: `error reading C:\Users\john\AppData\birdnet-go\data.db`,
		},
		{
			name:  "windows path with lowercase drive letter",
			input: `error reading c:\Users\john\AppData\birdnet-go\data.db`,
		},
		{
			name:  "unix path in middle of message",
			input: "database error at /var/lib/birdnet-go/data.db: disk full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := ScrubMessage(tt.input)
			// The original username should not appear in the scrubbed output
			assert.NotContains(t, result, "john")
			// The scrubbed result should still contain non-path parts of the message
			assert.NotEmpty(t, result)
		})
	}
}

func TestAnonymizeStacktracePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty path",
			input:    "",
			expected: "",
		},
		{
			name:     "short path with one segment",
			input:    "sentry.go",
			expected: "sentry.go",
		},
		{
			name:     "short path with two segments",
			input:    "telemetry/sentry.go",
			expected: "telemetry/sentry.go",
		},
		{
			name:     "absolute Unix path",
			input:    "/home/user/go/src/birdnet-go/internal/telemetry/sentry.go",
			expected: "<redacted>/telemetry/sentry.go",
		},
		{
			name:     "three segments",
			input:    "internal/telemetry/sentry.go",
			expected: "<redacted>/telemetry/sentry.go",
		},
		{
			name:     "Windows path",
			input:    `C:\Users\John\go\src\birdnet-go\internal\telemetry\sentry.go`,
			expected: "<redacted>/telemetry/sentry.go",
		},
		{
			name:     "mixed separators",
			input:    `C:\Users\John/internal\telemetry/sentry.go`,
			expected: "<redacted>/telemetry/sentry.go",
		},
	}

	runScrubTests(t, AnonymizeStacktracePath, tests)
}

func TestScrubStandaloneIPs_LeavesTimestampsIntact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"RFC3339 with offset and fraction", "2026-10-16T00:12:34.015573-07:00"},
		{"RFC3339 Z", "2026-08-23T19:55:13Z"},
		{"JSON log line", `{"time":"2026-10-16T00:12:34.015573-07:00","msg":"x"}`},
		{"clock time in free text", "at 12:34:56 done"},
		{"journal text format", "2026-10-16 00:12:34"},
		{"syslog prefix", "Oct 16 00:12:34 host birdnet-go[1]: x"},
		{"duration", "dur 00:00:01 elapsed"},
		{"Go stack position", "main.go:123:45 panic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.input, ScrubStandaloneIPs(tt.input))
		})
	}
}

// ipv6Samples are IPv6 addresses the scrubber must still anonymize.
var ipv6Samples = []struct {
	name   string
	addr   string
	prefix string
}{
	{"compressed public", "2001:db8::1", "public-ip-"},
	{"full form", "2001:0db8:85a3:0000:0000:8a2e:0370:7334", "public-ip-"},
	{"link local", "fe80::1", "private-ip-"},
}

func TestScrubStandaloneIPs_StillAnonymizesIPv6(t *testing.T) {
	t.Parallel()

	for _, tt := range ipv6Samples {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ScrubStandaloneIPs("peer " + tt.addr + " up")
			assert.NotContains(t, got, tt.addr)
			assert.Contains(t, got, tt.prefix)
			assert.True(t, strings.HasPrefix(got, "peer ") && strings.HasSuffix(got, " up"), got)
		})
	}

	t.Run("zone suffix stays", func(t *testing.T) {
		t.Parallel()
		got := ScrubStandaloneIPs("addr fe80::1%eth0 up")
		assert.NotContains(t, got, "fe80::1")
		assert.Contains(t, got, "private-ip-")
		assert.Contains(t, got, "%eth0")
	})

	t.Run("trailing port stays", func(t *testing.T) {
		t.Parallel()
		got := ScrubStandaloneIPs("2001:db8:85a3:0:0:8a2e:370:7334:8080")
		assert.NotContains(t, got, "2001:db8")
		assert.True(t, strings.HasSuffix(got, ":8080"), got)
	})

	t.Run("timestamp next to address", func(t *testing.T) {
		t.Parallel()
		got := ScrubStandaloneIPs("2026-10-16T00:12:34Z peer 2001:db8::1")
		assert.True(t, strings.HasPrefix(got, "2026-10-16T00:12:34Z peer "), got)
		assert.NotContains(t, got, "2001:db8::1")
		assert.Contains(t, got, "public-ip-")
	})
}

func TestScrubStandaloneIPs_IPv6MatchesAnonymizeIP(t *testing.T) {
	t.Parallel()

	for _, tt := range ipv6Samples {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "x "+AnonymizeIP(tt.addr)+" y", ScrubStandaloneIPs("x "+tt.addr+" y"))
		})
	}
}

func TestScrubStandaloneIPs_IPv4MappedAnonymizesIPv4Part(t *testing.T) {
	t.Parallel()
	got := ScrubStandaloneIPs("peer ::ffff:192.0.2.1 up")
	assert.NotContains(t, got, "192.0.2.1")
}

func TestScrubStandaloneIPs_IPv6InURLUntouched(t *testing.T) {
	t.Parallel()
	in := "https://[2001:db8::1]:8443/x"
	assert.Equal(t, in, ScrubStandaloneIPs(in))
}

func TestScrubStandaloneIPs_NonTimeTokensStillHashed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		leaks string
	}{
		{"MAC with letters", "mac aa:bb:cc:dd:ee:ff seen", "aa:bb:cc:dd:ee:ff"},
		{"MAC of digits", "mac 00:11:22:33:44:55 seen", "00:11:22:33:44:55"},
		{"IPv6 CIDR prefix", "net 2001:db8:abcd:1234::/64 up", "2001:db8:abcd:1234"},
		{"short hex token", "id ab:cd:ef seen", "ab:cd:ef"},
		{"digit-only prefix", "net 2001:1234:5678:9012::/64 up", "2001:1234:5678:9012"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NotContains(t, ScrubStandaloneIPs(tt.input), tt.leaks)
		})
	}
}

func TestScrubStandaloneIPs_KeepsUSBIDTokens(t *testing.T) {
	t.Parallel()

	keep := []struct {
		name  string
		input string
	}{
		{"redacted serial", "usb-id:0d8c:0014:<redacted>"},
		{"empty serial at end of text", "usb-id:046d:0825:"},
		{"no serial", "capture on usb-id:046d:0825 failed"},
		{"bus path with hex letters", "usb-path:usb-0000:00:1d.0-1"},
		{"bus path of digits", "usb-path:usb-0000:00:14.0-3"},
	}
	for _, tt := range keep {
		t.Run("keeps "+tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.input, ScrubStandaloneIPs(tt.input))
		})
	}

	scrub := []struct {
		name     string
		input    string
		contains string
		leaks    string
	}{
		{"address after the token", "usb-id:0d8c:0014:<redacted> peer 2001:db8::1", "usb-id:0d8c:0014:<redacted> peer public-ip-", "2001:db8::1"},
		{"address glued after the prefix", "usb-id:2001:0db8::1", "", "2001:0db8"},
		{"address glued after the pair", "usb-id:0d8c:0014:2001:db8::1", "", "2001:db8"},
		{"address glued after usb-", "usb-path:usb-2001:db8::1.0-1", "", "2001:db8"},
		{"truncated address after usb-id", "usb-id:2001:db8", "", "2001:db8"},
		{"hex serial after the pair", "usb-id:046d:0825:ab12", "", "046d:0825"},
		{"no dot after the PCI address", "usb-path:usb-0000:00:1d-1", "", "0000:00:1d"},
		{"longer match after usb-", "usb-path:usb-0000:00:1d:ab.0-1", "", "0000:00:1d:ab"},
	}
	for _, tt := range scrub {
		t.Run("scrubs "+tt.name, func(t *testing.T) {
			t.Parallel()
			got := ScrubStandaloneIPs(tt.input)
			assert.Contains(t, got, tt.contains)
			assert.NotContains(t, got, tt.leaks)
		})
	}
}

func TestScrubMessage_ScrubsPathsAroundDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"dated directories stay one path", "/data/clips/2026/10/16/john.wav", AnonymizePath("/data/clips/2026/10/16/john.wav")},
		{"date shaped tail after a slash", "/data/clips/10/16/2026", AnonymizePath("/data/clips/10/16/2026")},
		{"path then date time", "path /var/log/john 10/16/2026 00:12:34", "path " + AnonymizePath("/var/log/john") + " 10/16/2026 00:12:34"},
		{"path then date with T time", "/home/john 10/16/2026T12:34:56", AnonymizePath("/home/john") + " 10/16/2026T12:34:56"},
		{"date glued to a path segment", "/home/john-10/16/2026", AnonymizePath("/home/john-10/16/2026")},
		{"single segment head stays hashed", "/john 10/16/2026", AnonymizePath("/john") + " 10/16/2026"},
		{"separators only head", "/ 10/16/2026", "/ 10/16/2026"},
		{"date time then path", "10/16/2026 00:12:34 open /home/john/x.db", "10/16/2026 00:12:34 open " + AnonymizePath("/home/john/x.db")},
	}
	runScrubTests(t, ScrubMessage, tests)

	leaks := []struct {
		name  string
		input string
	}{
		{"date then path segment", "10/16/2026/john.wav"},
		{"date then single segment path", "10/16/2026 /john.wav"},
		{"path then date then path", "/home/x 10/16/2026 john/secret"},
		{"path then date then word", "/home/x 10/16/2026 john"},
		{"date then word glued to it", "/home/x 10/16/2026john"},
		{"date followed by one more digit", "10/16/20261/john"},
		{"date then relative file name", "saved backup 2026/10/16 john smith.wav"},
		{"date then dotted name", "10/16/2026 john.smith"},
		{"windows dated directories", `C:\Users\john\2026\10\16\x.wav`},
	}
	for _, tt := range leaks {
		t.Run("no leak "+tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NotContains(t, ScrubMessage(tt.input), "john")
		})
	}

	// After a path, a date is kept only before a clock time; anything else
	// could be the rest of a path segment, so the path stage handles it.
	notClockTimes := []struct {
		name  string
		input string
	}{
		{"three digit hour", "/home/john 10/16/2026 123:45"},
		{"hour without a colon", "/home/john 10/16/2026 12"},
	}
	for _, tt := range notClockTimes {
		t.Run("date not kept after path "+tt.name, func(t *testing.T) {
			t.Parallel()
			got := ScrubMessage(tt.input)
			assert.NotContains(t, got, "10/16/2026")
			assert.NotContains(t, got, "john")
		})
	}
}

func TestScrubMessage_LogLinesKeepDeviceIDs(t *testing.T) {
	t.Parallel()

	keep := []string{
		"10/16/2026 00:12:34 opened Blue Yeti Stereo Microphone: USB Audio (hw:1,0) id usb-id:0d8c:0014:<redacted>",
		`{"time":"2026-10-16T00:12:34.015573-07:00","msg":"device ready","device":"hw:1,0","id":"usb-id:046d:0825:"}`,
	}
	for _, in := range keep {
		t.Run("keeps "+in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, in, ScrubMessage(in))
		})
	}

	t.Run("scrubs private data beside device ids", func(t *testing.T) {
		t.Parallel()
		in := "10/16/2026 00:12:34 (hw:1,0) usb-id:0d8c:0014:SN12345 peer 2001:db8::1 at 60.1699,24.9384 file /home/john/clip.wav"
		got := ScrubMessage(in)
		assert.Contains(t, got, "10/16/2026 00:12:34 (hw:1,0) usb-id:0d8c:0014:<redacted> peer ")
		for _, leak := range []string{"SN12345", "2001:db8::1", "60.1699", "24.9384", "john"} {
			assert.NotContains(t, got, leak)
		}
	})
}

func TestScrubMessage_MasksUSBIDSerials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare token", "usb-id:0d8c:0014:SN12345", "usb-id:0d8c:0014:<redacted>"},
		{"dots dashes underscores belong to the serial", "opened usb-id:0d8c:0014:SN-1.2_3 ok", "opened usb-id:0d8c:0014:<redacted> ok"},
		{"colons slashes and equals belong to the serial", "opened usb-id:0d8c:0014:AB:CD/EF=1 ok", "opened usb-id:0d8c:0014:<redacted> ok"},
		{"quoted serial with a space", `{"id":"usb-id:0d8c:0014:A1 B2","x":1}`, `{"id":"usb-id:0d8c:0014:<redacted>","x":1}`},
		{"nested JSON value ends at the escaped quote", `{\"id\":\"usb-id:0d8c:0014:SN1\"}`, `{\"id\":\"usb-id:0d8c:0014:<redacted>\"}`},
		{"closing bracket ends the serial", "(usb-id:0d8c:0014:SN1)", "(usb-id:0d8c:0014:<redacted>)"},
		{"comma ends the serial and the scan continues", "usb-id:0d8c:0014:SN1,usb-id:046d:0825:SN2", "usb-id:0d8c:0014:<redacted>,usb-id:046d:0825:<redacted>"},
		{"hex serial keeps the ids", "usb-id:046d:0825:ab12", "usb-id:046d:0825:<redacted>"},
		{"backtick opens quoted mode", "`usb-id:0d8c:0014:A1 B2`", "`usb-id:0d8c:0014:<redacted>`"},
		{"backslash inside a JSON serial is masked with it", `{"id":"usb-id:0d8c:0014:A1\\B2"}`, `{"id":"usb-id:0d8c:0014:<redacted>"}`},
		{"escaped backslash at the end of a JSON serial", `{"id":"usb-id:0d8c:0014:A1\\"}`, `{"id":"usb-id:0d8c:0014:<redacted>"}`},
		{"second token inside a quoted serial goes with it", `msg="usb-id:0d8c:0014:SN1 replaced by usb-id:046d:0825:SN2"`, `msg="usb-id:0d8c:0014:<redacted>"`},
		{"masked token in quoted prose is kept", `msg="usb-id:0d8c:0014:<redacted> failed"`, `msg="usb-id:0d8c:0014:<redacted> failed"`},
		{"masked token before a period is kept", "device usb-id:0d8c:0014:<redacted>.", "device usb-id:0d8c:0014:<redacted>."},
		{"masked token before an error suffix is kept", "open usb-id:0d8c:0014:<redacted>: device busy", "open usb-id:0d8c:0014:<redacted>: device busy"},
		{"serial that starts with the marker is masked", "usb-id:046d:0825:<redacted>ABC123 ok", "usb-id:046d:0825:<redacted> ok"},
		{"marker followed by a dash and serial text is masked", "usb-id:046d:0825:<redacted>-ABC123 ok", "usb-id:046d:0825:<redacted> ok"},
		{"marker followed by a dot and serial text is masked", "usb-id:046d:0825:<redacted>.SN1 ok", "usb-id:046d:0825:<redacted> ok"},
		{"quoted serial that starts with a space", `device="usb-id:0d8c:0014: A1 B2"`, `device="usb-id:0d8c:0014:<redacted>"`},
		{"quoted serial that starts with a comma", "'usb-id:0d8c:0014:,A1'", "'usb-id:0d8c:0014:<redacted>'"},
		{"quoted empty serial is kept", `"usb-id:0d8c:0014:"`, `"usb-id:0d8c:0014:"`},
		{"unquoted serial that starts with a space is empty", "usb-id:0d8c:0014: A1", "usb-id:0d8c:0014: A1"},
		{"unterminated quoted serial runs to the end", `id="usb-id:0d8c:0014:A1 B2`, `id="usb-id:0d8c:0014:<redacted>`},
		{"quoted serial cut off after a backslash", `id="usb-id:0d8c:0014:A1\`, `id="usb-id:0d8c:0014:<redacted>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ScrubMessage(tt.input)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, got, ScrubMessage(got), "second pass must not change the output")
		})
	}
}

// The later stages of ScrubMessage rewrite hex-and-colon text, so the stage is
// checked on its own for text that is not a well-formed usb-id token.
func TestScrubUSBIDSerials_LeavesMalformedTokens(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"usb-id:zzzz:0014:SN1",
		"usb-id:0d8c:zzzz:SN1",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, in, scrubUSBIDSerials(in))
		})
	}
}

// Each byte that ends an unquoted serial, checked on the stage alone because
// later ScrubMessage stages rewrite some of the text around it.
func TestScrubUSBIDSerials_UnquotedSerialEndsAtTerminators(t *testing.T) {
	t.Parallel()

	for _, term := range []string{" ", "\t", "\n", "\r", "\v", "\f", `"`, "'", "`", ",", ";", ")", "]", "}", `\`} {
		t.Run(strconv.Quote(term), func(t *testing.T) {
			t.Parallel()
			in := "x usb-id:0d8c:0014:SN1" + term + "y"
			assert.Equal(t, "x usb-id:0d8c:0014:<redacted>"+term+"y", scrubUSBIDSerials(in))
		})
	}
}

func TestRedactUSBDeviceID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"serial is masked", "usb-id:0b33:0024:ABC123", "usb-id:0b33:0024:<redacted>"},
		{"empty serial is unchanged", "usb-id:0d8c:0014:", "usb-id:0d8c:0014:"},
		{"usb-path is unchanged", "usb-path:usb-xhci-hcd.0-1", "usb-path:usb-xhci-hcd.0-1"},
		{"device name is unchanged", "USB Audio Device", "USB Audio Device"},
		{"malformed usb-id is unchanged", "usb-id:onlyone", "usb-id:onlyone"},
		{"serial with a space is masked whole", "usb-id:0d8c:0014:A1 B2", "usb-id:0d8c:0014:<redacted>"},
		{"already masked is unchanged", "usb-id:0d8c:0014:<redacted>", "usb-id:0d8c:0014:<redacted>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RedactUSBDeviceID(tt.in))
		})
	}
}

func TestScrubStandaloneIPs_IPv4LeadingZerosStillHashed(t *testing.T) {
	t.Parallel()
	got := ScrubStandaloneIPs("host 192.168.001.010 up")
	assert.Contains(t, got, "invalid-ip-")
	assert.NotContains(t, got, "192.168.001.010")
}

func TestScrubMessage_KeepsLogTimestampScrubsIP(t *testing.T) {
	t.Parallel()
	got := ScrubMessage(`{"time":"2026-10-16T00:12:34.015573-07:00","msg":"conn from 192.168.1.100"}`)
	assert.Contains(t, got, `"time":"2026-10-16T00:12:34.015573-07:00"`)
	assert.NotContains(t, got, "192.168.1.100")
}
