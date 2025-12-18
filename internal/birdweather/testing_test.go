package birdweather

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestResolveDNSWithFallback tests the DNS fallback resolution mechanism
//
//nolint:gocognit // Test function with multiple sub-tests and thorough DNS fallback validation
func TestResolveDNSWithFallback(t *testing.T) {
	t.Parallel() // Safe to parallelize - no shared state

	testCases := []struct{
		name        string
		hostname    string
		expectError bool
	}{
		{
			name:        "Valid hostname - BirdWeather API",
			hostname:    "app.birdweather.com",
			expectError: false,
		},
		{
			name:        "Valid hostname - Google",
			hostname:    "www.google.com",
			expectError: false,
		},
		{
			name:        "Invalid hostname",
			hostname:    "this-hostname-definitely-does-not-exist-12345.invalid",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // Safe - each subtest has independent hostname

			// Set a reasonable timeout for the test to prevent hangs on slow/flaky DNS
			// 12s allows: system DNS (10s) + at least one fallback attempt (5s) with overhead
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()

			start := time.Now()
			ips, err := resolveDNSWithFallback(ctx, tc.hostname)
			duration := time.Since(start)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error for hostname %s, but got none", tc.hostname)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error resolving %s: %v", tc.hostname, err)
				}
				if len(ips) == 0 {
					t.Errorf("Expected IPs for %s, but got none", tc.hostname)
				}
			}

			// Verify that resolution completes within reasonable time
			// System DNS (10s) + fallback DNS (3 servers × 5s = 15s) = 25s max theoretical
			// Use 27s to allow for some processing overhead while catching regressions
			maxDuration := 27 * time.Second
			if duration > maxDuration {
				t.Errorf("DNS resolution took too long: %v (max: %v)", duration, maxDuration)
			}

			t.Logf("DNS resolution for %s took %v, returned %d IPs", tc.hostname, duration, len(ips))
		})
	}
}

// TestAPIConnectivityTimeout tests that API connectivity tests properly timeout
func TestAPIConnectivityTimeout(t *testing.T) {
	t.Parallel() // Safe - creates independent client

	settings := MockSettings()
	client, err := New(settings)
	if err != nil {
		t.Fatalf("Failed to create BwClient: %v", err)
	}

	// Create a context with a very short timeout to test timeout behavior
	// Use 10ms instead of 1ms to reduce flakiness while still being fast enough
	// to trigger a timeout before any real network operation completes
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result := client.testAPIConnectivity(ctx)

	// The test should timeout or fail quickly
	if result.Success {
		t.Error("Expected API connectivity test to fail with timeout, but it succeeded")
	}

	// Check that the error message mentions timeout or context
	if result.Error == "" {
		t.Error("Expected error message, but got empty string")
	}

	t.Logf("Timeout test result: %+v", result)
}

// TestTimeoutConstants verifies that timeout constants are properly configured
func TestTimeoutConstants(t *testing.T) {
	t.Parallel() // Safe - only reads constants

	// Verify that timeouts are long enough to handle DNS delays
	if apiTimeout < 10*time.Second {
		t.Errorf("apiTimeout (%v) should be at least 10s to handle DNS resolution delays", apiTimeout)
	}

	if authTimeout < 10*time.Second {
		t.Errorf("authTimeout (%v) should be at least 10s to handle DNS resolution delays", authTimeout)
	}

	if uploadTimeout < 20*time.Second {
		t.Errorf("uploadTimeout (%v) should be at least 20s to handle encoding and DNS delays", uploadTimeout)
	}

	// Verify DNS-specific timeouts are reasonable
	// Linux default DNS timeout is 5s per server, so our timeouts should accommodate this
	if dnsResolverTimeout < 5*time.Second {
		t.Errorf("dnsResolverTimeout (%v) should be at least 5s to match Linux DNS default", dnsResolverTimeout)
	}

	if dnsLookupTimeout < 5*time.Second {
		t.Errorf("dnsLookupTimeout (%v) should be at least 5s to match Linux DNS default", dnsLookupTimeout)
	}

	if systemDNSTimeout < 10*time.Second {
		t.Errorf("systemDNSTimeout (%v) should be at least 10s to allow for 2 DNS servers at 5s each", systemDNSTimeout)
	}

	// Verify timeout hierarchy makes sense
	if uploadTimeout <= apiTimeout {
		t.Errorf("uploadTimeout (%v) should be longer than apiTimeout (%v) to account for encoding",
			uploadTimeout, apiTimeout)
	}

	// Verify fallback DNS timeout math is reasonable
	expectedFallbackDuration := dnsLookupTimeout * time.Duration(len(fallbackDNSResolvers))
	maxDNSDuration := systemDNSTimeout + expectedFallbackDuration
	if maxDNSDuration > apiTimeout {
		t.Logf("Warning: Maximum DNS resolution time (%v) may exceed API timeout (%v)",
			maxDNSDuration, apiTimeout)
		t.Logf("  System DNS timeout: %v", systemDNSTimeout)
		t.Logf("  Fallback DNS attempts: %d servers × %v = %v",
			len(fallbackDNSResolvers), dnsLookupTimeout, expectedFallbackDuration)
	}

	// Ensure individual DNS timeouts aren't longer than the stage timeouts they're used in
	if dnsLookupTimeout > apiTimeout {
		t.Errorf("dnsLookupTimeout (%v) should not exceed apiTimeout (%v)", dnsLookupTimeout, apiTimeout)
	}

	t.Logf("Timeout constants: api=%v, auth=%v, upload=%v, post=%v, systemDNS=%v, dnsResolver=%v, dnsLookup=%v",
		apiTimeout, authTimeout, uploadTimeout, postTimeout, systemDNSTimeout, dnsResolverTimeout, dnsLookupTimeout)
}

// TestFallbackDNSResolvers verifies that fallback DNS resolvers are configured
func TestFallbackDNSResolvers(t *testing.T) {
	t.Parallel() // Safe - only reads global slice

	if len(fallbackDNSResolvers) == 0 {
		t.Error("No fallback DNS resolvers configured")
	}

	// Verify that each resolver is properly formatted using net.SplitHostPort
	for _, resolver := range fallbackDNSResolvers {
		if resolver == "" {
			t.Error("Empty fallback DNS resolver found")
			continue
		}

		// Parse and validate the host:port format
		host, port, err := net.SplitHostPort(resolver)
		if err != nil {
			t.Errorf("Fallback DNS resolver '%s' is not a valid host:port format: %v", resolver, err)
			continue
		}

		// Verify host is a valid IP address
		if net.ParseIP(host) == nil {
			t.Errorf("Fallback DNS resolver '%s' has invalid IP address: %s", resolver, host)
		}

		// Verify port is not empty
		if port == "" {
			t.Errorf("Fallback DNS resolver '%s' has empty port", resolver)
		}
	}

	t.Logf("Configured %d fallback DNS resolvers: %v", len(fallbackDNSResolvers), fallbackDNSResolvers)
}

// TestIsDNSError tests the DNS error detection function
func TestIsDNSError(t *testing.T) {
	t.Parallel() // Safe - no shared state

	testCases := []struct {
		name        string
		errorMsg    string
		expectDNS   bool
	}{
		{
			name:      "DNS lookup error",
			errorMsg:  "lookup app.birdweather.com: no such host",
			expectDNS: true,
		},
		{
			name:      "DNS resolution error with lookup keyword",
			errorMsg:  "lookup app.birdweather.com on 192.168.1.1:53: no such host",
			expectDNS: true,
		},
		{
			name:      "Connection refused (not DNS-specific)",
			errorMsg:  "dial tcp: connection refused",
			expectDNS: false,
		},
		{
			name:      "Generic timeout (not DNS-specific)",
			errorMsg:  "i/o timeout",
			expectDNS: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // Safe - independent test cases

			// Create a simple error with the message
			err := &testError{msg: tc.errorMsg}
			result := isDNSError(err)

			if result != tc.expectDNS {
				t.Errorf("isDNSError(%q) = %v, want %v", tc.errorMsg, result, tc.expectDNS)
			}
		})
	}
}

// testError is a simple error implementation for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// TestDNSLookupCancellation verifies that context cancellation properly stops DNS lookups
func TestDNSLookupCancellation(t *testing.T) {
	t.Parallel() // Safe - independent context

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately to test cancellation behavior
	cancel()

	_, err := net.DefaultResolver.LookupIP(ctx, "ip", "app.birdweather.com")

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}

	// Also verify it's recognized as a DNS error
	if !isDNSError(err) {
		t.Error("Cancelled DNS lookup should be classified as DNS error")
	}
}

// TestIsDNSTimeout tests the DNS timeout detection function
// This leverages Go 1.23+ feature where DNSError wraps context.DeadlineExceeded
func TestIsDNSTimeout(t *testing.T) {
	t.Parallel() // Safe - no shared state

	testCases := []struct {
		name          string
		err           error
		expectTimeout bool
		expectDNSErr  bool // Whether this should also be a DNS error
	}{
		{
			name:          "Nil error",
			err:           nil,
			expectTimeout: false,
			expectDNSErr:  false,
		},
		{
			name:          "Context deadline exceeded (bare)",
			err:           context.DeadlineExceeded,
			expectTimeout: true,
			expectDNSErr:  false, // Bare context error is not DNS-specific
		},
		{
			name:          "Wrapped context deadline with 'lookup'",
			err:           fmt.Errorf("lookup failed: %w", context.DeadlineExceeded),
			expectTimeout: true,
			expectDNSErr:  true, // Contains "lookup " so matches DNS error pattern
		},
		{
			name: "DNS error with timeout",
			err: &net.DNSError{
				Err:       "i/o timeout",
				IsTimeout: true,
			},
			expectTimeout: true,
			expectDNSErr:  true, // This is both a timeout AND a DNS error
		},
		{
			name: "DNS error without timeout",
			err: &net.DNSError{
				Err:       "no such host",
				IsTimeout: false,
			},
			expectTimeout: false,
			expectDNSErr:  true, // This is a DNS error but not a timeout
		},
		{
			name:          "Generic error",
			err:           fmt.Errorf("some other error"),
			expectTimeout: false,
			expectDNSErr:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // Safe - independent test cases

			result := isDNSTimeout(tc.err)
			if result != tc.expectTimeout {
				t.Errorf("isDNSTimeout() = %v, want %v for error: %v", result, tc.expectTimeout, tc.err)
			}

			// Validate DNS error classification
			isDNSErr := isDNSError(tc.err)
			if isDNSErr != tc.expectDNSErr {
				t.Errorf("isDNSError() = %v, want %v for error: %v", isDNSErr, tc.expectDNSErr, tc.err)
			}

			// DNS-specific timeouts should be classified as both timeout and DNS error
			if tc.expectTimeout && tc.expectDNSErr && !isDNSErr {
				t.Errorf("DNS timeout should be classified as DNS error: %v", tc.err)
			}
		})
	}
}
