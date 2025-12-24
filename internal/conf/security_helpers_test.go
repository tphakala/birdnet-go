package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test constants for Security helper methods.
const (
	// Test hostnames and domains
	testHostExample     = "birdnet.example.com"
	testHostIgnored     = "ignored.example.com"
	testHostDifferent   = "different.example.com"
	testHostSubdomain   = "my.birdnet.home.arpa"
	testHostLocalhost   = "localhost"
	testHostIP          = "192.168.1.100"
	testHostIPv6        = "2001:db8::1"
	testHostIPv6Bracket = "[2001:db8::1]"

	// Test ports
	testPortCustom = "8080"
	testPort5500   = "5500"

	// Test URLs
	testURLHTTPS         = "https://birdnet.example.com:5500"
	testURLHTTPSNoPort   = "https://birdnet.example.com"
	testURLHTTPNoPort    = "http://birdnet.example.com"
	testURLHTTPLocalhost = "http://localhost:8080"
	testURLTrailingSlash = "https://birdnet.example.com/"
	testURLWithSubdomain = "https://my.birdnet.home.arpa:8080"
	testURLWithIP        = "http://192.168.1.100:8080"
	testURLWithIPv6      = "https://[2001:db8::1]:8080"
	testURLInvalid       = "not-a-valid-url"
	testURLDifferent     = "https://different.example.com:5500"
)

// TestSecurity_GetBaseURL tests the GetBaseURL helper method.
func TestSecurity_GetBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		port     string
		want     string
	}{
		{
			name: "BaseURL set - should use BaseURL as-is",
			security: Security{
				BaseURL: testURLHTTPS,
				Host:    testHostIgnored,
				AutoTLS: false,
			},
			port: testPortCustom,
			want: testURLHTTPS,
		},
		{
			name: "BaseURL with trailing slash - should trim slash",
			security: Security{
				BaseURL: testURLTrailingSlash,
				Host:    "",
				AutoTLS: false,
			},
			port: testPortCustom,
			want: testURLHTTPSNoPort,
		},
		{
			name: "BaseURL empty, Host set with AutoTLS - should construct HTTPS URL",
			security: Security{
				BaseURL: "",
				Host:    testHostExample,
				AutoTLS: true,
			},
			port: DefaultHTTPSPort,
			want: testURLHTTPSNoPort,
		},
		{
			name: "BaseURL empty, Host set without AutoTLS - should construct HTTP URL",
			security: Security{
				BaseURL: "",
				Host:    testHostExample,
				AutoTLS: false,
			},
			port: testPortCustom,
			want: "http://birdnet.example.com:8080",
		},
		{
			name: "BaseURL empty, Host set with default HTTP port - should omit port",
			security: Security{
				BaseURL: "",
				Host:    testHostExample,
				AutoTLS: false,
			},
			port: DefaultHTTPPort,
			want: testURLHTTPNoPort,
		},
		{
			name: "BaseURL empty, Host set with default HTTPS port - should omit port",
			security: Security{
				BaseURL: "",
				Host:    testHostExample,
				AutoTLS: true,
			},
			port: DefaultHTTPSPort,
			want: testURLHTTPSNoPort,
		},
		{
			name: "BaseURL empty, Host empty - should return empty string",
			security: Security{
				BaseURL: "",
				Host:    "",
				AutoTLS: false,
			},
			port: testPortCustom,
			want: "",
		},
		{
			name: "BaseURL with HTTP scheme explicitly set",
			security: Security{
				BaseURL: testURLHTTPLocalhost,
				Host:    "",
				AutoTLS: true, // AutoTLS ignored when BaseURL is set
			},
			port: DefaultHTTPSPort,
			want: testURLHTTPLocalhost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.security.GetBaseURL(tt.port)
			assert.Equal(t, tt.want, got, "Security.GetBaseURL(%q)", tt.port)
		})
	}
}

// TestSecurity_GetHostnameForCertificates tests hostname extraction for AutoTLS.
func TestSecurity_GetHostnameForCertificates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		want     string
	}{
		{
			name: "Host set - should return Host",
			security: Security{
				Host:    testHostExample,
				BaseURL: testURLDifferent,
			},
			want: testHostExample,
		},
		{
			name: "Host empty, BaseURL set without port - should extract hostname",
			security: Security{
				Host:    "",
				BaseURL: testURLHTTPSNoPort,
			},
			want: testHostExample,
		},
		{
			name: "Host empty, BaseURL set with port - should extract hostname without port",
			security: Security{
				Host:    "",
				BaseURL: testURLHTTPS,
			},
			want: testHostExample,
		},
		{
			name: "Host empty, BaseURL with subdomain - should extract full hostname",
			security: Security{
				Host:    "",
				BaseURL: testURLWithSubdomain,
			},
			want: testHostSubdomain,
		},
		{
			name: "Host empty, BaseURL set with IP and port - should extract IP without port",
			security: Security{
				Host:    "",
				BaseURL: testURLWithIP,
			},
			want: testHostIP,
		},
		{
			name: "Host empty, BaseURL set with IPv6 - should extract IPv6 address",
			security: Security{
				Host:    "",
				BaseURL: testURLWithIPv6,
			},
			want: testHostIPv6,
		},
		{
			name: "Host empty, BaseURL empty - should return empty string",
			security: Security{
				Host:    "",
				BaseURL: "",
			},
			want: "",
		},
		{
			name: "Host empty, BaseURL invalid - should return empty string",
			security: Security{
				Host:    "",
				BaseURL: testURLInvalid,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.security.GetHostnameForCertificates()
			assert.Equal(t, tt.want, got, "Security.GetHostnameForCertificates()")
		})
	}
}

// TestSecurity_GetExternalHost tests external host extraction for backward compatibility.
func TestSecurity_GetExternalHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		want     string
	}{
		{
			name: "BaseURL set with port - should return host:port",
			security: Security{
				BaseURL: testURLHTTPS,
				Host:    testHostIgnored,
			},
			want: testHostExample + ":" + testPort5500,
		},
		{
			name: "BaseURL set without explicit port (HTTPS) - should return just hostname",
			security: Security{
				BaseURL: testURLHTTPSNoPort,
				Host:    "",
			},
			want: testHostExample,
		},
		{
			name: "BaseURL set without explicit port (HTTP) - should return just hostname",
			security: Security{
				BaseURL: testURLHTTPNoPort,
				Host:    "",
			},
			want: testHostExample,
		},
		{
			name: "BaseURL empty, Host set - should return Host",
			security: Security{
				BaseURL: "",
				Host:    testHostExample,
			},
			want: testHostExample,
		},
		{
			name: "BaseURL invalid, Host set - should fallback to Host",
			security: Security{
				BaseURL: testURLInvalid,
				Host:    testHostExample,
			},
			want: testHostExample,
		},
		{
			name: "Both empty - should return empty string",
			security: Security{
				BaseURL: "",
				Host:    "",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.security.GetExternalHost()
			assert.Equal(t, tt.want, got, "Security.GetExternalHost()")
		})
	}
}

// TestSecurity_GetBaseURL_PortConstants verifies port constant usage.
func TestSecurity_GetBaseURL_PortConstants(t *testing.T) {
	t.Parallel()

	// Verify the constants are correctly defined
	assert.Equal(t, "80", DefaultHTTPPort, "DefaultHTTPPort should be 80")
	assert.Equal(t, "443", DefaultHTTPSPort, "DefaultHTTPSPort should be 443")
	assert.Equal(t, "http", SchemeHTTP, "SchemeHTTP should be http")
	assert.Equal(t, "https", SchemeHTTPS, "SchemeHTTPS should be https")
}

// TestSecurity_GetBaseURL_WhitespaceHandling tests whitespace handling in BaseURL.
func TestSecurity_GetBaseURL_WhitespaceHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"leading space", " https://example.com", "https://example.com"},
		{"trailing space", "https://example.com ", "https://example.com"},
		{"both spaces", " https://example.com ", "https://example.com"},
		{"tab characters", "\thttps://example.com\t", "https://example.com"},
		{"newline", "https://example.com\n", "https://example.com"},
		{"mixed whitespace", " \t https://example.com \n ", "https://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := Security{BaseURL: tt.baseURL}
			got := s.GetBaseURL(testPortCustom)
			assert.Equal(t, tt.want, got, "Whitespace should be trimmed from BaseURL")
		})
	}
}
