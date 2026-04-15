package birdweather

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// newCBTestClient returns a BwClient with a short-fused circuit breaker suited
// for deterministic state transition tests. The HTTPClient is preconfigured to
// point at server via a rewriting transport so the tests do not race on DNS or
// network stacks.
func newCBTestClient(t *testing.T, server *httptest.Server, cfg notification.CircuitBreakerConfig) *BwClient {
	t.Helper()

	client := &BwClient{
		Settings:      MockSettings(),
		BirdweatherID: "test-station-123",
		HTTPClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: &mockTransport{server: server},
		},
		circuitBreaker: notification.NewPushCircuitBreaker(cfg, nil, bwCircuitBreakerProvider),
	}
	return client
}

// newCBTestConfig returns a tightened CircuitBreakerConfig that lets the tests
// observe all three state transitions (closed → open → half-open → closed)
// without touching real wall-clock durations.
func newCBTestConfig() notification.CircuitBreakerConfig {
	return notification.CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
}

// TestBwClient_CircuitBreaker_OpensAfterConsecutiveFailures verifies the
// closed → open transition when the upstream BirdWeather API keeps failing.
// The test asserts that once the breaker is open no further HTTP requests are
// made (hit counter stops incrementing) and subsequent calls return the
// sentinel ErrCircuitBreakerOpen.
func TestBwClient_CircuitBreaker_OpensAfterConsecutiveFailures(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"success":false}`)
	}))
	defer server.Close()

	cfg := newCBTestConfig()
	client := newCBTestClient(t, server, cfg)

	// Drive the breaker to open by triggering MaxFailures failed detections.
	for range cfg.MaxFailures {
		err := client.PostDetection("1", "2024-01-01T00:00:00.000+0000", "Great Tit", "Parus major", 0.9)
		require.Error(t, err, "expected upstream 500 to surface as error")
	}

	require.Equal(t, notification.StateOpen, client.CircuitBreakerState(),
		"breaker should be OPEN after %d consecutive failures", cfg.MaxFailures)

	before := hits.Load()

	// Further calls must short-circuit: no HTTP request, error detected as CB-open.
	err := client.PostDetection("1", "2024-01-01T00:00:00.000+0000", "Great Tit", "Parus major", 0.9)
	require.Error(t, err, "call while breaker open must return an error")
	assert.True(t, isCircuitBreakerOpen(err),
		"error %v should be recognised as circuit-breaker open", err)
	assert.Equal(t, before, hits.Load(),
		"no HTTP request should be made while breaker is open")
}

// TestBwClient_CircuitBreaker_HalfOpenAllowsProbe verifies the open → half-open
// transition after the configured reset timeout and the half-open → closed
// transition once a probe request succeeds.
func TestBwClient_CircuitBreaker_HalfOpenAllowsProbe(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var succeedAfter atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n <= succeedAfter.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"success":false}`)
			return
		}
		// Probe response — succeed to close the breaker.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"success":true}`)
	}))
	defer server.Close()

	cfg := newCBTestConfig()
	succeedAfter.Store(int32(cfg.MaxFailures))
	client := newCBTestClient(t, server, cfg)

	// Fail enough times to open the breaker.
	for range cfg.MaxFailures {
		_ = client.PostDetection("42", "2024-01-01T00:00:00.000+0000", "Great Tit", "Parus major", 0.9)
	}
	require.Equal(t, notification.StateOpen, client.CircuitBreakerState())

	// Wait past the reset timeout so the next call transitions to half-open.
	time.Sleep(cfg.Timeout + 20*time.Millisecond)

	// Probe request should succeed and close the breaker again.
	err := client.PostDetection("42", "2024-01-01T00:00:00.000+0000", "Great Tit", "Parus major", 0.9)
	require.NoError(t, err, "probe request should succeed with 201 response")
	assert.Equal(t, notification.StateClosed, client.CircuitBreakerState(),
		"breaker should close again after a successful probe")
}

// TestBwClient_CircuitBreaker_TransportErrorCountsAsFailure guards against a
// prior bug where transport errors bypassed the breaker state machine. A
// transport-level failure (closed connection, hijacked and aborted) must
// trip the breaker after MaxFailures just like a 5xx response. We use an
// aborted response because httptest.Server sits behind its own transport
// which ignores http.Client.Timeout on the real client object — using a
// forced network error is the most reliable way to exercise this path.
func TestBwClient_CircuitBreaker_TransportErrorCountsAsFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Hijack the connection and close it without sending a valid
		// response. The client will observe "EOF" / "connection reset"
		// which surfaces as a net.Error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		_ = conn.Close()
	}))
	defer server.Close()

	cfg := newCBTestConfig()
	client := newCBTestClient(t, server, cfg)

	for range cfg.MaxFailures {
		err := client.PostDetection("7", "2024-01-01T00:00:00.000+0000", "Great Tit", "Parus major", 0.9)
		require.Error(t, err, "transport error must surface as error from PostDetection")
	}

	assert.Equal(t, notification.StateOpen, client.CircuitBreakerState(),
		"transport errors must trip the breaker exactly like upstream errors")
}

// TestBwClient_CircuitBreaker_Nil_OK verifies graceful behaviour when the
// breaker has not been attached (e.g., legacy BwClient literal in existing
// tests). The client must still function; the helper simply executes fn.
func TestBwClient_CircuitBreaker_Nil_OK(t *testing.T) {
	t.Parallel()

	c := &BwClient{}
	called := false
	err := c.callWithCircuitBreaker(t.Context(), func(_ context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called, "fn must run when no breaker is attached")
	assert.Equal(t, notification.StateClosed, c.CircuitBreakerState())
}

// TestBwClient_CircuitBreaker_MalformedUploadResponseTripsBreaker verifies
// that parseSoundscapeResponse() failures (HTML served with 201, invalid
// JSON, success:false payloads) count as breaker failures. Without this,
// a degraded upstream returning 201s with broken bodies would silently pass
// the closure's HTTP checks while clients kept hammering the endpoint.
func TestBwClient_CircuitBreaker_MalformedUploadResponseTripsBreaker(t *testing.T) {
	t.Parallel()

	// Respond with 201 Created but an invalid JSON body — handleHTTPResponse
	// accepts the status code, but parseSoundscapeResponse fails.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `<html><body>gateway timeout</body></html>`)
	}))
	defer server.Close()

	cfg := newCBTestConfig()
	client := newCBTestClient(t, server, cfg)

	for range cfg.MaxFailures {
		_, err := client.UploadSoundscape("2024-01-01T00:00:00.000+0000", []byte{0, 0, 0, 0})
		require.Error(t, err, "malformed response must surface as error")
	}

	assert.Equal(t, notification.StateOpen, client.CircuitBreakerState(),
		"parseSoundscapeResponse failures must trip the breaker — otherwise a degraded upstream returning 201s with broken bodies would silently pass the closure")
}

// TestBwClient_CircuitBreaker_NotFoundDoesNotTrip verifies that 422 species
// validation errors (CategoryNotFound — expected when a non-bird species is
// sent to BirdWeather) do NOT count toward the breaker. Without this guard
// we would open the breaker simply because the user configured a non-bird
// species in their detection list, suppressing legitimate later uploads.
func TestBwClient_CircuitBreaker_NotFoundDoesNotTrip(t *testing.T) {
	t.Parallel()

	// Always respond with 422 + species error body — handleHTTPResponse tags
	// this as CategoryNotFound.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"species":"unknown species","error":"invalid"}`)
	}))
	defer server.Close()

	cfg := newCBTestConfig()
	client := newCBTestClient(t, server, cfg)

	// Post far more than MaxFailures detections — every one returns 422 but
	// the breaker must stay closed because CategoryNotFound errors are
	// business-logic, not transient failures.
	for range cfg.MaxFailures * 3 {
		err := client.PostDetection("n/a", "2024-01-01T00:00:00.000+0000", "Not A Bird", "notabird", 0.9)
		require.Error(t, err, "422 species validation must still surface as an error to the caller")
	}

	assert.Equal(t, notification.StateClosed, client.CircuitBreakerState(),
		"CategoryNotFound (species validation) must not trip the circuit breaker")
}
