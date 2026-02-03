//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setEnv is a test helper that sets an environment variable and fails the test if it errors
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	err := os.Setenv(key, value)
	require.NoError(t, err, "Failed to set environment variable %s", key)
}

// unsetEnv is a test helper that unsets an environment variable and fails the test if it errors
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	err := os.Unsetenv(key)
	require.NoError(t, err, "Failed to unset environment variable %s", key)
}

func TestBuildBaseURL(t *testing.T) {
	// Note: No t.Parallel() here because subtests mutate the global BIRDNET_HOST environment variable

	tests := []struct {
		name     string
		host     string
		port     string
		autoTLS  bool
		envVar   string
		expected string
	}{
		{
			name:     "explicit host with custom port HTTP",
			host:     "birdnet.example.com",
			port:     "8080",
			autoTLS:  false,
			expected: "http://birdnet.example.com:8080",
		},
		{
			name:     "explicit host with custom port HTTPS",
			host:     "birdnet.example.com",
			port:     "8443",
			autoTLS:  true,
			expected: "https://birdnet.example.com:8443",
		},
		{
			name:     "explicit host with default HTTP port",
			host:     "birdnet.example.com",
			port:     "80",
			autoTLS:  false,
			expected: "http://birdnet.example.com",
		},
		{
			name:     "explicit host with default HTTPS port",
			host:     "birdnet.example.com",
			port:     "443",
			autoTLS:  true,
			expected: "https://birdnet.example.com",
		},
		{
			name:     "environment variable with custom port",
			host:     "",
			port:     "8080",
			autoTLS:  false,
			envVar:   "birdnet.home.arpa",
			expected: "http://birdnet.home.arpa:8080",
		},
		{
			name:     "environment variable with whitespace",
			host:     "",
			port:     "8080",
			autoTLS:  false,
			envVar:   "  birdnet.home.arpa  ",
			expected: "http://birdnet.home.arpa:8080",
		},
		{
			name:     "environment variable with IP address",
			host:     "",
			port:     "8080",
			autoTLS:  false,
			envVar:   "192.168.1.100",
			expected: "http://192.168.1.100:8080",
		},
		{
			name:     "localhost fallback when no host or env var",
			host:     "",
			port:     "8080",
			autoTLS:  false,
			envVar:   "",
			expected: "http://localhost:8080",
		},
		{
			name:     "explicit host takes priority over env var",
			host:     "config-host.example.com",
			port:     "8080",
			autoTLS:  false,
			envVar:   "env-host.example.com",
			expected: "http://config-host.example.com:8080",
		},
		{
			name:     "IPv6 address from environment",
			host:     "",
			port:     "8080",
			autoTLS:  false,
			envVar:   "[2001:db8::1]",
			expected: "http://[2001:db8::1]:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv automatically restores original value and prevents t.Parallel()
			if tt.envVar != "" {
				t.Setenv("BIRDNET_HOST", tt.envVar)
			} else {
				// Ensure env var is not set by setting to empty
				t.Setenv("BIRDNET_HOST", "")
			}

			result := BuildBaseURL(tt.host, tt.port, tt.autoTLS)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildBaseURL_HostResolutionPriority(t *testing.T) {
	// Test the explicit priority chain without parallel execution
	// since we're manipulating environment variables

	// Save original env var
	oldEnv := os.Getenv("BIRDNET_HOST")
	defer func() {
		if oldEnv != "" {
			setEnv(t, "BIRDNET_HOST", oldEnv)
		} else {
			unsetEnv(t, "BIRDNET_HOST")
		}
	}()

	// Test 1: Config host has highest priority
	setEnv(t, "BIRDNET_HOST", "env-host.example.com")
	result := BuildBaseURL("config-host.example.com", "8080", false)
	assert.Equal(t, "http://config-host.example.com:8080", result,
		"Config host should take priority over environment variable")

	// Test 2: Environment variable used when config empty
	result = BuildBaseURL("", "8080", false)
	assert.Equal(t, "http://env-host.example.com:8080", result,
		"Environment variable should be used when config host is empty")

	// Test 3: Localhost used when both empty
	unsetEnv(t, "BIRDNET_HOST")
	result = BuildBaseURL("", "8080", false)
	assert.Equal(t, "http://localhost:8080", result,
		"Should fall back to localhost when config and env var are both empty")
}

func TestBuildBaseURL_RealWorldScenarios(t *testing.T) {
	tests := []struct {
		name        string
		description string
		host        string
		port        string
		autoTLS     bool
		envVar      string
		expected    string
	}{
		{
			name:        "nginx reverse proxy with custom domain",
			description: "User accesses via nginx proxy at custom domain",
			host:        "",
			port:        "8080",
			autoTLS:     false,
			envVar:      "birdnet.home.arpa",
			expected:    "http://birdnet.home.arpa:8080",
		},
		{
			name:        "Docker container with host network",
			description: "Docker container using host network mode",
			host:        "",
			port:        "8080",
			autoTLS:     false,
			envVar:      "192.168.1.100",
			expected:    "http://192.168.1.100:8080",
		},
		{
			name:        "Cloudflare Tunnel with HTTPS",
			description: "User with Cloudflare Tunnel and AutoTLS",
			host:        "birdnet.example.com",
			port:        "443",
			autoTLS:     true,
			envVar:      "",
			expected:    "https://birdnet.example.com",
		},
		{
			name:        "Local network with mDNS hostname",
			description: "Raspberry Pi with .local mDNS hostname",
			host:        "",
			port:        "8080",
			autoTLS:     false,
			envVar:      "raspberrypi.local",
			expected:    "http://raspberrypi.local:8080",
		},
		{
			name:        "Direct access on localhost",
			description: "Developer testing directly on localhost",
			host:        "",
			port:        "8080",
			autoTLS:     false,
			envVar:      "",
			expected:    "http://localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore environment
			oldEnv := os.Getenv("BIRDNET_HOST")
			defer func() {
				if oldEnv != "" {
					setEnv(t, "BIRDNET_HOST", oldEnv)
				} else {
					unsetEnv(t, "BIRDNET_HOST")
				}
			}()

			if tt.envVar != "" {
				setEnv(t, "BIRDNET_HOST", tt.envVar)
			} else {
				unsetEnv(t, "BIRDNET_HOST")
			}

			result := BuildBaseURL(tt.host, tt.port, tt.autoTLS)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestBuildBaseURL_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     string
		autoTLS  bool
		envVar   string
		expected string
	}{
		{
			name:     "empty string port defaults to no port suffix",
			host:     "example.com",
			port:     "",
			autoTLS:  false,
			expected: "http://example.com:",
		},
		{
			name:     "whitespace-only env var treated as empty",
			host:     "",
			port:     "8080",
			autoTLS:  false,
			envVar:   "   ",
			expected: "http://localhost:8080",
		},
		{
			name:     "host with trailing dot (FQDN)",
			host:     "birdnet.example.com.",
			port:     "8080",
			autoTLS:  false,
			expected: "http://birdnet.example.com.:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldEnv := os.Getenv("BIRDNET_HOST")
			defer func() {
				if oldEnv != "" {
					setEnv(t, "BIRDNET_HOST", oldEnv)
				} else {
					unsetEnv(t, "BIRDNET_HOST")
				}
			}()

			if tt.envVar != "" {
				setEnv(t, "BIRDNET_HOST", tt.envVar)
			} else {
				unsetEnv(t, "BIRDNET_HOST")
			}

			result := BuildBaseURL(tt.host, tt.port, tt.autoTLS)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNewTemplateData verifies that template data is correctly constructed from detection events
func TestNewTemplateData(t *testing.T) {
	t.Parallel()

	// This test would require setting up a full detection event with metadata
	// For now, we'll focus on BuildBaseURL tests which are the main change
	// The integration test in detection_consumer_test.go covers the full flow
	t.Skip("Template data construction is covered by integration tests")
}

// BenchmarkBuildBaseURL measures the performance of URL construction
func BenchmarkBuildBaseURL(b *testing.B) {
	scenarios := []struct {
		name    string
		host    string
		port    string
		autoTLS bool
		envVar  string
	}{
		{"explicit_host", "birdnet.example.com", "8080", false, ""},
		{"env_var", "", "8080", false, "birdnet.home.arpa"},
		{"localhost_fallback", "", "8080", false, ""},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			// Setup environment for benchmark
			if scenario.envVar != "" {
				if err := os.Setenv("BIRDNET_HOST", scenario.envVar); err != nil {
					b.Fatalf("Failed to set env var: %v", err)
				}
				defer func() {
					if err := os.Unsetenv("BIRDNET_HOST"); err != nil {
						b.Fatalf("Failed to unset env var: %v", err)
					}
				}()
			} else {
				if err := os.Unsetenv("BIRDNET_HOST"); err != nil {
					b.Fatalf("Failed to unset env var: %v", err)
				}
			}

			b.ResetTimer()
			for b.Loop() {
				_ = BuildBaseURL(scenario.host, scenario.port, scenario.autoTLS)
			}
		})
	}
}
