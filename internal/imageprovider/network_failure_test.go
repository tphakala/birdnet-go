package imageprovider

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestIsNetworkError tests detection of DNS and network-level errors.
func TestIsNetworkError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error is not a network error",
			err:  nil,
			want: false,
		},
		{
			name: "DNS error",
			err: &net.DNSError{
				Err:  "server misbehaving",
				Name: "en.wikipedia.org",
			},
			want: true,
		},
		{
			name: "net.OpError wrapping dial failure",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: fmt.Errorf("connection refused"),
			},
			want: true,
		},
		{
			name: "error message containing dial tcp",
			err:  fmt.Errorf("Get \"https://en.wikipedia.org\": dial tcp: lookup en.wikipedia.org: no such host"),
			want: true,
		},
		{
			name: "error message containing no such host",
			err:  fmt.Errorf("lookup en.wikipedia.org: no such host"),
			want: true,
		},
		{
			name: "error message containing connection refused",
			err:  fmt.Errorf("dial tcp 1.2.3.4:443: connection refused"),
			want: true,
		},
		{
			name: "HTTP 500 error is not a network error",
			err:  fmt.Errorf("HTTP 500 Internal Server Error"),
			want: false,
		},
		{
			name: "JSON parsing error is not a network error",
			err:  fmt.Errorf("invalid character '<' looking for beginning of value"),
			want: false,
		},
		{
			name: "generic error is not a network error",
			err:  fmt.Errorf("something went wrong"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isNetworkError(tt.err)
			assert.Equal(t, tt.want, got, "isNetworkError(%v)", tt.err)
		})
	}
}

// TestNetworkErrorOpensCircuitBreaker tests that network/DNS failures open the circuit breaker.
func TestNetworkErrorOpensCircuitBreaker(t *testing.T) {
	t.Parallel()

	provider := &wikiMediaProvider{
		maxRetries: 1,
	}

	// Verify circuit is initially closed
	open, _ := provider.isCircuitOpen()
	assert.False(t, open, "circuit should start closed")

	// Simulate a DNS error triggering circuit open
	dnsErr := &net.DNSError{
		Err:  "server misbehaving",
		Name: "en.wikipedia.org",
	}
	provider.openCircuit(circuitBreakerNetworkDuration,
		fmt.Sprintf("Network/DNS failure: %s", dnsErr.Error()))

	// Verify circuit is now open
	open, reason := provider.isCircuitOpen()
	assert.True(t, open, "circuit should be open after network error")
	assert.Contains(t, reason, "Network/DNS failure", "reason should mention network failure")
	assert.Contains(t, reason, "server misbehaving", "reason should contain original error")
}

// TestNetworkErrorLogDowngrade tests that repeated network errors are downgraded to debug level.
func TestNetworkErrorLogDowngrade(t *testing.T) {
	t.Parallel()

	provider := &wikiMediaProvider{}

	// First call: CompareAndSwap(false, true) should succeed
	swapped := provider.networkErrorLogged.CompareAndSwap(false, true)
	assert.True(t, swapped, "first network error should log at Error level")

	// Second call: CompareAndSwap(false, true) should fail (already true)
	swapped = provider.networkErrorLogged.CompareAndSwap(false, true)
	assert.False(t, swapped, "repeated network error should be downgraded to Debug level")

	// Reset on circuit recovery
	provider.resetCircuit()
	swapped = provider.networkErrorLogged.CompareAndSwap(false, true)
	assert.True(t, swapped, "after circuit reset, first error should log at Error level again")
}

// TestCircuitBreakerNetworkDuration tests the constant value is reasonable.
func TestCircuitBreakerNetworkDuration(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 2*time.Minute, circuitBreakerNetworkDuration,
		"network circuit breaker duration should be 2 minutes")
}
