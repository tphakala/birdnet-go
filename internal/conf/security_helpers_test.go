package conf

import (
	"testing"
)

// TestSecurity_GetBaseURL tests the GetBaseURL helper method
// This test is written in advance (TDD) for the future BaseURL feature
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
				BaseURL: "https://birdnet.example.com:5500",
				Host:    "ignored.example.com",
				AutoTLS: false,
			},
			port: "8080",
			want: "https://birdnet.example.com:5500",
		},
		{
			name: "BaseURL with trailing slash - should trim slash",
			security: Security{
				BaseURL: "https://birdnet.example.com/",
				Host:    "",
				AutoTLS: false,
			},
			port: "8080",
			want: "https://birdnet.example.com",
		},
		{
			name: "BaseURL empty, Host set with AutoTLS - should construct HTTPS URL",
			security: Security{
				BaseURL: "",
				Host:    "birdnet.example.com",
				AutoTLS: true,
			},
			port: "443",
			want: "https://birdnet.example.com",
		},
		{
			name: "BaseURL empty, Host set without AutoTLS - should construct HTTP URL",
			security: Security{
				BaseURL: "",
				Host:    "birdnet.example.com",
				AutoTLS: false,
			},
			port: "8080",
			want: "http://birdnet.example.com:8080",
		},
		{
			name: "BaseURL empty, Host set with default HTTP port - should omit port",
			security: Security{
				BaseURL: "",
				Host:    "birdnet.example.com",
				AutoTLS: false,
			},
			port: "80",
			want: "http://birdnet.example.com",
		},
		{
			name: "BaseURL empty, Host set with default HTTPS port - should omit port",
			security: Security{
				BaseURL: "",
				Host:    "birdnet.example.com",
				AutoTLS: true,
			},
			port: "443",
			want: "https://birdnet.example.com",
		},
		{
			name: "BaseURL empty, Host empty - should return empty string",
			security: Security{
				BaseURL: "",
				Host:    "",
				AutoTLS: false,
			},
			port: "8080",
			want: "",
		},
		{
			name: "BaseURL with HTTP scheme explicitly set",
			security: Security{
				BaseURL: "http://localhost:8080",
				Host:    "",
				AutoTLS: true, // AutoTLS ignored when BaseURL is set
			},
			port: "443",
			want: "http://localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Note: This method doesn't exist yet - this is TDD
			// Uncomment when implementing:
			// got := tt.security.GetBaseURL(tt.port)
			// if got != tt.want {
			// 	t.Errorf("Security.GetBaseURL() = %v, want %v", got, tt.want)
			// }

			// For now, skip the test
			t.Skip("Waiting for GetBaseURL() implementation")
		})
	}
}

// TestSecurity_GetHostnameForCertificates tests hostname extraction for AutoTLS
// This test is written in advance (TDD) for the future BaseURL feature
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
				Host:    "birdnet.example.com",
				BaseURL: "https://different.example.com:5500",
			},
			want: "birdnet.example.com",
		},
		{
			name: "Host empty, BaseURL set without port - should extract hostname",
			security: Security{
				Host:    "",
				BaseURL: "https://birdnet.example.com",
			},
			want: "birdnet.example.com",
		},
		{
			name: "Host empty, BaseURL set with port - should extract hostname without port",
			security: Security{
				Host:    "",
				BaseURL: "https://birdnet.example.com:5500",
			},
			want: "birdnet.example.com",
		},
		{
			name: "Host empty, BaseURL with subdomain - should extract full hostname",
			security: Security{
				Host:    "",
				BaseURL: "https://my.birdnet.home.arpa:8080",
			},
			want: "my.birdnet.home.arpa",
		},
		{
			name: "Host empty, BaseURL set with IP and port - should extract IP without port",
			security: Security{
				Host:    "",
				BaseURL: "http://192.168.1.100:8080",
			},
			want: "192.168.1.100",
		},
		{
			name: "Host empty, BaseURL set with IPv6 - should extract IPv6 address",
			security: Security{
				Host:    "",
				BaseURL: "https://[2001:db8::1]:8080",
			},
			want: "2001:db8::1",
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
				BaseURL: "not-a-valid-url",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Note: This method doesn't exist yet - this is TDD
			// Uncomment when implementing:
			// got := tt.security.GetHostnameForCertificates()
			// if got != tt.want {
			// 	t.Errorf("Security.GetHostnameForCertificates() = %v, want %v", got, tt.want)
			// }

			// For now, skip the test
			t.Skip("Waiting for GetHostnameForCertificates() implementation")
		})
	}
}

// TestSecurity_GetExternalHost tests external host extraction for backward compatibility
// This test is written in advance (TDD) for the future BaseURL feature
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
				BaseURL: "https://birdnet.example.com:5500",
				Host:    "ignored.example.com",
			},
			want: "birdnet.example.com:5500",
		},
		{
			name: "BaseURL set without explicit port (HTTPS) - should return just hostname",
			security: Security{
				BaseURL: "https://birdnet.example.com",
				Host:    "",
			},
			want: "birdnet.example.com",
		},
		{
			name: "BaseURL set without explicit port (HTTP) - should return just hostname",
			security: Security{
				BaseURL: "http://birdnet.example.com",
				Host:    "",
			},
			want: "birdnet.example.com",
		},
		{
			name: "BaseURL empty, Host set - should return Host",
			security: Security{
				BaseURL: "",
				Host:    "birdnet.example.com",
			},
			want: "birdnet.example.com",
		},
		{
			name: "BaseURL invalid, Host set - should fallback to Host",
			security: Security{
				BaseURL: "invalid-url",
				Host:    "birdnet.example.com",
			},
			want: "birdnet.example.com",
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

			// Note: This method doesn't exist yet - this is TDD
			// Uncomment when implementing:
			// got := tt.security.GetExternalHost()
			// if got != tt.want {
			// 	t.Errorf("Security.GetExternalHost() = %v, want %v", got, tt.want)
			// }

			// For now, skip the test
			t.Skip("Waiting for GetExternalHost() implementation")
		})
	}
}
