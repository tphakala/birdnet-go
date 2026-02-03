package birdweather

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()

			start := time.Now()
			ips, err := resolveDNSWithFallback(ctx, tc.hostname)
			duration := time.Since(start)

			if tc.expectError {
				require.Error(t, err, "Expected error for hostname %s", tc.hostname)
			} else {
				require.NoError(t, err, "Unexpected error resolving %s", tc.hostname)
				assert.NotEmpty(t, ips, "Expected IPs for %s", tc.hostname)
			}

			// Verify that resolution completes within reasonable time
			// System DNS (10s) + fallback DNS (3 servers × 5s = 15s) = 25s max theoretical
			// Use 27s to allow for some processing overhead while catching regressions
			maxDuration := 27 * time.Second
			assert.LessOrEqual(t, duration, maxDuration,
				"DNS resolution took too long: %v (max: %v)", duration, maxDuration)

			t.Logf("DNS resolution for %s took %v, returned %d IPs", tc.hostname, duration, len(ips))
		})
	}
}

// TestAPIConnectivityTimeout tests that API connectivity tests properly timeout
func TestAPIConnectivityTimeout(t *testing.T) {
	t.Parallel() // Safe - creates independent client

	settings := MockSettings()
	client, err := New(settings)
	require.NoError(t, err, "Failed to create BwClient")

	// Create a context with a very short timeout to test timeout behavior
	// Use 10ms instead of 1ms to reduce flakiness while still being fast enough
	// to trigger a timeout before any real network operation completes
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	result := client.testAPIConnectivity(ctx)

	// The test should timeout or fail quickly
	assert.False(t, result.Success, "Expected API connectivity test to fail with timeout")
	assert.NotEmpty(t, result.Error, "Expected error message")

	t.Logf("Timeout test result: %+v", result)
}

// TestTimeoutConstants verifies that timeout constants are properly configured
func TestTimeoutConstants(t *testing.T) {
	t.Parallel() // Safe - only reads constants

	// Verify that timeouts are long enough to handle DNS delays
	assert.GreaterOrEqual(t, apiTimeout, 10*time.Second,
		"apiTimeout should be at least 10s to handle DNS resolution delays")
	assert.GreaterOrEqual(t, authTimeout, 10*time.Second,
		"authTimeout should be at least 10s to handle DNS resolution delays")
	assert.GreaterOrEqual(t, uploadTimeout, 20*time.Second,
		"uploadTimeout should be at least 20s to handle encoding and DNS delays")

	// Verify DNS-specific timeouts are reasonable
	// Linux default DNS timeout is 5s per server, so our timeouts should accommodate this
	assert.GreaterOrEqual(t, dnsResolverTimeout, 5*time.Second,
		"dnsResolverTimeout should be at least 5s to match Linux DNS default")
	assert.GreaterOrEqual(t, dnsLookupTimeout, 5*time.Second,
		"dnsLookupTimeout should be at least 5s to match Linux DNS default")
	assert.GreaterOrEqual(t, systemDNSTimeout, 10*time.Second,
		"systemDNSTimeout should be at least 10s to allow for 2 DNS servers at 5s each")

	// Verify timeout hierarchy makes sense
	assert.Greater(t, uploadTimeout, apiTimeout,
		"uploadTimeout should be longer than apiTimeout to account for encoding")

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
	assert.LessOrEqual(t, dnsLookupTimeout, apiTimeout,
		"dnsLookupTimeout should not exceed apiTimeout")

	t.Logf("Timeout constants: api=%v, auth=%v, upload=%v, post=%v, systemDNS=%v, dnsResolver=%v, dnsLookup=%v",
		apiTimeout, authTimeout, uploadTimeout, postTimeout, systemDNSTimeout, dnsResolverTimeout, dnsLookupTimeout)
}

// TestFallbackDNSResolvers verifies that fallback DNS resolvers are configured
func TestFallbackDNSResolvers(t *testing.T) {
	t.Parallel() // Safe - only reads global slice

	assert.NotEmpty(t, fallbackDNSResolvers, "No fallback DNS resolvers configured")

	// Verify that each resolver is properly formatted using net.SplitHostPort
	for _, resolver := range fallbackDNSResolvers {
		assert.NotEmpty(t, resolver, "Empty fallback DNS resolver found")

		// Parse and validate the host:port format
		host, port, err := net.SplitHostPort(resolver)
		require.NoError(t, err, "Fallback DNS resolver '%s' is not a valid host:port format", resolver)

		// Verify host is a valid IP address
		assert.NotNil(t, net.ParseIP(host), "Fallback DNS resolver '%s' has invalid IP address: %s", resolver, host)

		// Verify port is not empty
		assert.NotEmpty(t, port, "Fallback DNS resolver '%s' has empty port", resolver)
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

			assert.Equal(t, tc.expectDNS, result, "isDNSError(%q)", tc.errorMsg)
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

	ctx, cancel := context.WithCancel(t.Context())

	// Cancel immediately to test cancellation behavior
	cancel()

	_, err := net.DefaultResolver.LookupIP(ctx, "ip", "app.birdweather.com")

	require.ErrorIs(t, err, context.Canceled, "Expected context.Canceled")

	// Also verify it's recognized as a DNS error
	assert.True(t, isDNSError(err), "Cancelled DNS lookup should be classified as DNS error")
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
			assert.Equal(t, tc.expectTimeout, result, "isDNSTimeout() for error: %v", tc.err)

			// Validate DNS error classification
			isDNSErr := isDNSError(tc.err)
			assert.Equal(t, tc.expectDNSErr, isDNSErr, "isDNSError() for error: %v", tc.err)

			// DNS-specific timeouts should be classified as both timeout and DNS error
			if tc.expectTimeout && tc.expectDNSErr {
				assert.True(t, isDNSErr, "DNS timeout should be classified as DNS error: %v", tc.err)
			}
		})
	}
}
