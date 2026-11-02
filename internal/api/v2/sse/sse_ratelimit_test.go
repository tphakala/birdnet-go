// sse_ratelimit_test.go pins the stream connection rate limit: 10 connections
// per minute per client IP with a burst of 10, shared by the detection and sound
// level streams. The tests run in a synctest bubble, so the limiter's clock is
// virtual and no test waits in real time.
package sse

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// rateTestBurst is the number of connections one IP may open in quick
	// succession.
	rateTestBurst = 10
	// rateTestOverBurst is how many attempts beyond the burst a test makes to
	// show that only the burst passes.
	rateTestOverBurst = 5
	// rateTestRefillInterval is how long one connection token takes to come back
	// (10 per minute is one every 6 seconds).
	rateTestRefillInterval = 6 * time.Second
	// rateTestFullRefill is how long an exhausted bucket takes to fill again.
	rateTestFullRefill = rateTestBurst * rateTestRefillInterval
	// rateTestAlmostRefilled is the idle time after which exactly one token of an
	// exhausted bucket is still missing.
	rateTestAlmostRefilled = rateTestFullRefill - rateTestRefillInterval
	// rateTestIdleMargin is how long past the expiry a visitor stays idle so that
	// it is certainly forgotten.
	rateTestIdleMargin = time.Second
	// rateTestRateTolerance is the allowed difference between the configured
	// per-second rate and 10 per minute.
	rateTestRateTolerance = 1e-9
	// rateTestFirstPort is the first source port of the simulated client; each
	// request uses the next one, as a browser does for every new connection.
	rateTestFirstPort = 40000
	// rateTestClientIP and rateTestOtherIP are documentation-range addresses.
	rateTestClientIP = "192.0.2.10"
	rateTestOtherIP  = "192.0.2.20"
	// rateTestStoreIdentifier names a visitor when the store is used directly.
	rateTestStoreIdentifier = "visitor-a"
	// rateTestOtherIdentifier is a second visitor used to trigger store cleanup.
	rateTestOtherIdentifier = "visitor-b"
)

// allowN calls Allow n times and returns how many calls were allowed.
func allowN(t *testing.T, store *middleware.RateLimiterMemoryStore, identifier string, n int) int {
	t.Helper()
	allowed := 0
	for range n {
		ok, err := store.Allow(identifier)
		require.NoError(t, err)
		if ok {
			allowed++
		}
	}
	return allowed
}

// TestStreamRateLimiterStoreConfig asserts the configured rate and burst. Echo
// reads Rate as requests per second, so 10 per minute must be 10/60, and an idle
// visitor must be kept at least until its bucket has fully refilled.
func TestStreamRateLimiterStoreConfig(t *testing.T) {
	t.Parallel()
	cfg := streamRateLimiterStoreConfig()

	assert.InDelta(t, float64(rateTestBurst)/time.Minute.Seconds(), float64(cfg.Rate), rateTestRateTolerance,
		"rate must be 10 requests per minute expressed per second")
	assert.Equal(t, rateTestBurst, cfg.Burst)
	assert.GreaterOrEqual(t, cfg.ExpiresIn, rateTestFullRefill,
		"idle visitors must not be forgotten before the bucket has fully refilled")
}

// TestStreamRateLimiterIdleVisitorExpiry verifies that an idle visitor is kept
// until its bucket has refilled, and that a forgotten visitor starts with a full
// burst and no more. One token short of a full refill, another visitor's request
// reaches Echo's cleanup check; an expiry shorter than the refill would let that
// request drop the idle visitor and hand it a full burst instead of the tokens it
// has earned.
func TestStreamRateLimiterIdleVisitorExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := streamRateLimiterStoreConfig()
		store := middleware.NewRateLimiterMemoryStoreWithConfig(cfg)

		require.Equal(t, rateTestBurst, allowN(t, store, rateTestStoreIdentifier, rateTestBurst+rateTestOverBurst))

		time.Sleep(rateTestAlmostRefilled)
		require.Equal(t, 1, allowN(t, store, rateTestOtherIdentifier, 1))
		assert.Equal(t, rateTestBurst-1, allowN(t, store, rateTestStoreIdentifier, rateTestBurst+rateTestOverBurst),
			"a visitor idle for less than a full refill has earned all but one token")

		time.Sleep(cfg.ExpiresIn + rateTestIdleMargin)
		require.Equal(t, 1, allowN(t, store, rateTestOtherIdentifier, 1))
		assert.Equal(t, rateTestBurst, allowN(t, store, rateTestStoreIdentifier, rateTestBurst+rateTestOverBurst),
			"a forgotten visitor starts with a full burst and no more")
	})
}

// streamRouteResult sends a stream request and returns the response. The request
// context is already cancelled, so an allowed stream sends its connection
// message and returns instead of blocking in its event loop.
func streamRouteResult(t *testing.T, e *echo.Echo, path, ip string, port int) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody).WithContext(ctx)
	req.RemoteAddr = net.JoinHostPort(ip, strconv.Itoa(port))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestStreamRoutesRateLimit verifies the registered stream routes: both share
// one bucket per client IP, a new source port from the same IP is not a new
// client, other IPs are unaffected, and tokens come back at 10 per minute.
func TestStreamRoutesRateLimit(t *testing.T) {
	h, core := newLoopTestHandler(t)
	e := core.Echo
	paths := []string{"/api/v2/detections/stream", "/api/v2/soundlevels/stream"}

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		h.RegisterRoutes(core.Group)

		port := rateTestFirstPort
		// request sends one request from a new source port and alternates between
		// the two stream endpoints.
		request := func(ip string, endpoint int) *httptest.ResponseRecorder {
			port++
			return streamRouteResult(t, e, paths[endpoint%len(paths)], ip, port)
		}

		for i := range rateTestBurst {
			require.Equal(t, http.StatusOK, request(rateTestClientIP, i).Code, "request %d is within the burst", i+1)
		}
		denied := request(rateTestClientIP, 0)
		assert.Equal(t, http.StatusTooManyRequests, denied.Code, "the request after the burst must be refused")
		assert.JSONEq(t, `{"error":"Too many SSE connection attempts, please wait before trying again"}`, denied.Body.String())

		assert.Equal(t, http.StatusOK, request(rateTestOtherIP, 0).Code, "another IP has its own bucket")

		time.Sleep(rateTestRefillInterval)
		assert.Equal(t, http.StatusOK, request(rateTestClientIP, 1).Code, "one token is back after one interval")
		assert.Equal(t, http.StatusTooManyRequests, request(rateTestClientIP, 0).Code, "only one token came back")

		time.Sleep(rateTestFullRefill)
		for i := range rateTestBurst {
			assert.Equal(t, http.StatusOK, request(rateTestClientIP, i).Code, "burst restored after a full refill, request %d", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(rateTestClientIP, 0).Code)
	})
}
