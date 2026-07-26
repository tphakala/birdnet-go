package imageprovider

import (
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
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

// dnsFailureRoundTripper fails every request with a DNS resolution error without
// touching the network, so the production retry-and-circuit-breaker path can be
// driven end to end in a unit test.
type dnsFailureRoundTripper struct {
	calls atomic.Int64
}

func (rt *dnsFailureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return nil, &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{Err: "server misbehaving", Name: req.URL.Hostname()},
	}
}

// TestNetworkErrorOpensCircuitBreaker tests that network/DNS failures open the circuit
// breaker through the real request path.
//
// The previous version called provider.openCircuit itself and then asserted the circuit
// was open, which only proved that a setter sets. It would have stayed green if
// queryWithRetryAndLimiter stopped calling openCircuit altogether. This drives the
// production retry loop with a failing transport instead.
func TestNetworkErrorOpensCircuitBreaker(t *testing.T) {
	t.Parallel()

	// synctest makes the retry backoff (retryMinDelay, 2s) virtual, so the test is
	// instant rather than waiting out a real sleep.
	synctest.Test(t, func(t *testing.T) {
		rt := &dnsFailureRoundTripper{}
		provider := &wikiMediaProvider{
			httpClient:    &http.Client{Transport: rt},
			userAgent:     buildUserAgent("test"),
			maxRetries:    1,
			globalLimiter: rate.NewLimiter(rate.Inf, 1),
		}

		open, _ := provider.isCircuitOpen()
		require.False(t, open, "circuit should start closed")

		_, err := provider.queryWithRetryAndLimiter(t.Context(), "test-req-1",
			map[string]string{"action": "query", "titles": "Parus major"}, nil)
		require.Error(t, err, "a failing transport must surface an error")
		assert.Positive(t, rt.calls.Load(), "the production path should have attempted the request")

		open, reason := provider.isCircuitOpen()
		assert.True(t, open, "circuit should be open after the retry loop exhausts on a network error")
		assert.Contains(t, reason, "Network/DNS failure", "reason should mention network failure")
		assert.Contains(t, reason, "server misbehaving", "reason should contain original error")
	})
}

// TestResetCircuitReenablesErrorLevelLogging tests that circuit recovery clears the
// latch that downgrades repeated network-error logs to Debug.
//
// The previous TestNetworkErrorLogDowngrade asserted the first two CompareAndSwap
// results directly, which only restated sync/atomic.Bool semantics. Only the
// resetCircuit behaviour below belongs to this package.
func TestResetCircuitReenablesErrorLevelLogging(t *testing.T) {
	t.Parallel()

	provider := &wikiMediaProvider{}

	// Latch the flag as the first logged network error would.
	require.True(t, provider.networkErrorLogged.CompareAndSwap(false, true))

	provider.resetCircuit()

	assert.True(t, provider.networkErrorLogged.CompareAndSwap(false, true),
		"resetCircuit must clear the log-downgrade latch so recovery is visible at Error level")
}

// TestCircuitBreakerNetworkDuration tests the constant value is reasonable.
func TestCircuitBreakerNetworkDuration(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 2*time.Minute, circuitBreakerNetworkDuration,
		"network circuit breaker duration should be 2 minutes")
}
