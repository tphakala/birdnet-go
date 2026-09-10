package imageprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// newTestWikiProvider returns a provider pointed at a local httptest server.
//
// Every HTTP path in wikipedia.go (the retry loop, the circuit breaker, response
// classification) previously had no coverage because the endpoint was a package
// constant, so exercising it meant calling the live Wikipedia API. apiURL exists as
// the seam that makes these paths testable.
func newTestWikiProvider(t *testing.T, handler http.HandlerFunc) *wikiMediaProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &wikiMediaProvider{
		httpClient: server.Client(),
		apiURL:     server.URL,
		// A very high limit keeps the tests fast while still exercising the limiter
		// call sites; the production value is 1 req/s.
		globalLimiter: rate.NewLimiter(rate.Inf, 1),
		maxRetries:    defaultMaxRetries,
	}
}

// TestQueryWithRetry_RetriesThenSucceeds pins the happy retry path and, importantly,
// that a success resets the circuit breaker rather than leaving it half-open.
func TestQueryWithRetry_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":{"pages":[{"title":"Turdus merula"}]}}`))
	})
	// The production backoff is seconds; the retry behaviour under test is the
	// sequencing, not the wait.
	provider.maxRetries = 2

	resp, err := provider.queryWithRetryAndLimiter(t.Context(), "test", map[string]string{
		"action": "query",
		"titles": "Turdus merula",
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int64(2), calls.Load())

	open, _ := provider.isCircuitOpen()
	assert.False(t, open, "a successful response must reset the circuit")
}

// TestQueryWithRetry_RateLimitAbandonsRemainingAttempts is the request-amplification
// guard: a 429 opens the circuit breaker, and the retry loop must stop rather than
// spend its remaining attempts hammering a host that just told us to back off.
func TestQueryWithRetry_RateLimitAbandonsRemainingAttempts(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := provider.queryWithRetryAndLimiter(t.Context(), "test", map[string]string{
		"action": "query",
		"titles": "Turdus merula",
	}, nil)

	require.Error(t, err)
	assert.Equal(t, int64(1), calls.Load(),
		"a 429 opens the circuit, so attempts 2 and 3 must not fire")

	open, reason := provider.isCircuitOpen()
	assert.True(t, open)
	assert.Contains(t, reason, "429")

	// The breaker's own message is what internal/errors telemetry suppression matches
	// on, so it must survive rather than being replaced by a retry-exhausted error.
	assert.Contains(t, err.Error(), "circuit breaker open")
}

// TestQueryWithRetry_CircuitOpenSkipsTheRequestEntirely asserts the pre-loop check
// still short-circuits, so an already-open breaker costs no outbound traffic.
func TestQueryWithRetry_CircuitOpenSkipsTheRequestEntirely(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	provider.openCircuit(time.Minute, "test block")

	_, err := provider.queryWithRetryAndLimiter(t.Context(), "test", map[string]string{"action": "query"}, nil)

	require.Error(t, err)
	assert.Zero(t, calls.Load())
}

// TestQueryWithRetry_ContextCancellationStopsRetrying asserts the backoff is
// interruptible. Fetch used to pass context.Background(), whose Done() channel is nil
// and can therefore never be selected, so neither the backoff nor the limiter wait
// could be abandoned.
func TestQueryWithRetry_ContextCancellationStopsRetrying(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := provider.queryWithRetryAndLimiter(ctx, "test", map[string]string{"action": "query"}, nil)
		done <- err
	}()

	// The first attempt fails fast and the loop enters its 2s backoff; cancelling
	// there must return immediately rather than waiting the backoff out.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// The specific error matters: require.Error alone is satisfied by the 500 that
		// already happened, so the assertion would pass even if the backoff ran to
		// completion and the cancellation was never observed.
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("a cancelled context did not interrupt the retry backoff")
	}
}

// TestQueryWithRetry_UnparseableResponseIsNotRefetched is the regression test for the
// diagnostic double-request: an HTML error page served with HTTP 200 used to trigger a
// second GET of the same URL purely to classify it, doubling outbound traffic at
// exactly the moment Wikipedia was throttling. The classification now runs on the
// response already in hand.
func TestQueryWithRetry_UnparseableResponseIsNotRefetched(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body><h1>Access blocked</h1></body></html>"))
	})
	provider.maxRetries = 1

	_, err := provider.queryWithRetryAndLimiter(t.Context(), "test", map[string]string{"action": "query"}, nil)

	require.Error(t, err)
	assert.Equal(t, int64(1), calls.Load(),
		"classifying an unparseable response must not issue a second request for it")
}

// TestQueryWithRetry_NonPositiveMaxRetries guards the nil-dereference that a
// non-positive maxRetries used to cause: the loop body never ran, leaving lastErr nil
// for the post-loop error construction to dereference.
func TestQueryWithRetry_NonPositiveMaxRetries(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	provider.maxRetries = 0

	_, err := provider.queryWithRetryAndLimiter(t.Context(), "test", map[string]string{"action": "query"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid maxRetries")
}

// TestBuildUserAgent_IncludesVersion covers the memoized User-Agent branch that was
// unreachable in tests because Settings.Version is empty in the test binary.
func TestBuildUserAgent_IncludesVersion(t *testing.T) {
	t.Parallel()

	ua := buildUserAgent("1.2.3-test")
	assert.Contains(t, ua, "1.2.3-test")
	assert.Contains(t, ua, userAgentName)
}
