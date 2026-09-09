package imageprovider

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
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

	// synctest bubbles the clock so any backoff is virtual and the test stays instant.
	synctest.Test(t, func(t *testing.T) {
		rt := &dnsFailureRoundTripper{}
		provider := &wikiMediaProvider{
			httpClient:    &http.Client{Transport: rt},
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

// statusRoundTripper answers every request with a fixed status and counts the calls.
type statusRoundTripper struct {
	status int
	calls  atomic.Int64
}

func (rt *statusRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return &http.Response{
		StatusCode: rt.status,
		Body:       io.NopCloser(strings.NewReader("{}")),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}, nil
}

// TestRateLimitStopsFurtherAttempts asserts that a 429 which opens the circuit breaker
// stops the retry loop immediately, spending neither another request nor a backoff.
func TestRateLimitStopsFurtherAttempts(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rt := &statusRoundTripper{status: http.StatusTooManyRequests}
		provider := &wikiMediaProvider{
			httpClient:    &http.Client{Transport: rt},
			maxRetries:    3,
			globalLimiter: rate.NewLimiter(rate.Inf, 1),
		}

		start := time.Now()
		_, err := provider.queryWithRetryAndLimiter(t.Context(), "test-429",
			map[string]string{"action": "query", "titles": "Parus major"}, nil)
		elapsed := time.Since(start)
		require.Error(t, err)

		open, _ := provider.isCircuitOpen()
		require.True(t, open, "a 429 should open the circuit breaker")

		assert.Equal(t, int64(1), rt.calls.Load(),
			"once the 429 opened the circuit, no further request should have been sent")

		// Bailing out must also skip the backoff that precedes the abandoned attempt.
		// Checking the breaker only at the top of the next iteration still burned
		// calculateRetryDelay(0) first, which this pins.
		assert.Zero(t, elapsed,
			"no backoff should be paid once the circuit is open")

		// The invariant the whole design rests on: a transient breaker error must never
		// be mistaken for a permanent "no image exists" and persisted as a negative
		// cache entry. Without this, a rate-limit episode could blank species for ~10y.
		require.NotErrorIs(t, err, ErrImageNotFound,
			"a circuit-breaker error must never be classified as image-not-found")
	})
}

// TestRetryLoopDoesNotBackOffAfterFinalAttempt asserts the loop stops as soon as the
// last attempt fails, instead of sleeping a backoff it will never use.
//
// The loop slept calculateRetryDelay(attempt) unconditionally, so an exhausted fetch
// held its caller a further calculateRetryDelay(maxRetries-1) with nothing left to wait
// for. synctest makes that wasted sleep directly observable as virtual time.
func TestRetryLoopDoesNotBackOffAfterFinalAttempt(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// 500 is retryable and does not open the circuit, so all attempts run and the
		// only thing under test is the trailing sleep.
		rt := &statusRoundTripper{status: http.StatusInternalServerError}
		provider := &wikiMediaProvider{
			httpClient:    &http.Client{Transport: rt},
			maxRetries:    3,
			globalLimiter: rate.NewLimiter(rate.Inf, 1),
		}

		start := time.Now()
		_, err := provider.queryWithRetryAndLimiter(t.Context(), "test-500",
			map[string]string{"action": "query", "titles": "Parus major"}, nil)
		elapsed := time.Since(start)
		require.Error(t, err)

		require.Equal(t, int64(3), rt.calls.Load(), "all three attempts should have been made")

		// Backoff runs only BETWEEN attempts: delays for attempts 0 and 1, none after
		// attempt 2. Sleeping after the final attempt would add calculateRetryDelay(2).
		// Exact, not LessOrEqual: the virtual clock makes this deterministic, and a
		// one-sided bound also passes at elapsed == 0, so it would stay green if the
		// between-attempt backoff were removed entirely.
		wantTotal := calculateRetryDelay(0) + calculateRetryDelay(1)
		assert.Equal(t, wantTotal, elapsed,
			"backoff must run between attempts only (a trailing sleep would add %s)",
			calculateRetryDelay(2))
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

// TestQueryWithNonPositiveMaxRetriesReturnsError covers the guard added for a
// non-positive maxRetries.
//
// Without it the loop body never runs, lastErr stays nil, and the post-loop error
// construction dereferences it: buildRetryExhaustedError calls lastErr.Error() and
// logAPIError does the same, so the call panics rather than returning. Production always
// sets defaultMaxRetries, but the provider is a plain struct that tests construct
// directly, so a zero value is one forgotten field away.
func TestQueryWithNonPositiveMaxRetriesReturnsError(t *testing.T) {
	t.Parallel()

	for _, maxRetries := range []int{0, -1} {
		t.Run(fmt.Sprintf("maxRetries=%d", maxRetries), func(t *testing.T) {
			t.Parallel()

			rt := &statusRoundTripper{status: http.StatusOK}
			provider := &wikiMediaProvider{
				httpClient:    &http.Client{Transport: rt},
				maxRetries:    maxRetries,
				globalLimiter: rate.NewLimiter(rate.Inf, 1),
			}

			// require.NotPanics is the assertion that matters: the pre-guard code
			// panicked here rather than returning.
			var (
				resp *wikiAPIResponse
				err  error
			)
			require.NotPanics(t, func() {
				resp, err = provider.queryWithRetryAndLimiter(t.Context(), "test-maxretries",
					map[string]string{"action": "query", "titles": "Parus major"}, nil)
			})

			require.Error(t, err, "a non-positive maxRetries must be reported, not silently ignored")
			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), "maxRetries")
			assert.Zero(t, rt.calls.Load(), "no request should be attempted with a non-positive retry count")
		})
	}
}
