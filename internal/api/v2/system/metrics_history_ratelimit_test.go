// metrics_history_ratelimit_test.go pins the metrics stream connection rate
// limit: 10 connections per minute per client IP with a burst of 10. Every test
// that advances time runs in a synctest bubble, so the limiter's clock is virtual
// and no test waits in real time.
package system

import (
	"context"
	"fmt"
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

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/observability"
)

const (
	// metricsRateTestBurst is the number of connections one client may open in quick
	// succession.
	metricsRateTestBurst = 10
	// metricsRateTestOverBurst is how many attempts beyond the burst a test makes
	// to show that only the burst passes.
	metricsRateTestOverBurst = 5
	// metricsRateTestRefillInterval is how long one connection token takes to come
	// back (10 per minute is one every 6 seconds).
	metricsRateTestRefillInterval = 6 * time.Second
	// metricsRateTestFullRefill is how long an exhausted bucket takes to fill again.
	metricsRateTestFullRefill = metricsRateTestBurst * metricsRateTestRefillInterval
	// metricsRateTestAlmostRefilled is the idle time after which exactly one token
	// of an exhausted bucket is still missing.
	metricsRateTestAlmostRefilled = metricsRateTestFullRefill - metricsRateTestRefillInterval
	// metricsRateTestIdleMargin is how long past the expiry a visitor stays idle so
	// that it is certainly forgotten.
	metricsRateTestIdleMargin = time.Second
	// metricsRateTestRateTolerance is the allowed difference between the
	// configured per-second rate and 10 per minute.
	metricsRateTestRateTolerance = 1e-9
	// metricsRateTestFirstPort is the first source port of the simulated client;
	// each request uses the next one, as a browser does for every new connection.
	metricsRateTestFirstPort = 40000
	// metricsRateTestClientIP and metricsRateTestOtherIP are documentation-range
	// addresses.
	metricsRateTestClientIP = "192.0.2.10"
	metricsRateTestOtherIP  = "192.0.2.20"
	// metricsRateTestStreamPath and metricsRateTestHistoryPath are the registered
	// routes.
	metricsRateTestStreamPath  = "/api/v2/system/metrics/stream"
	metricsRateTestHistoryPath = "/api/v2/system/metrics/history"
	// metricsRateTestIPv6Block formats an address in one documentation-range /64;
	// each request takes the next host part, as a client that rotates addresses
	// would. metricsRateTestOtherIPv6 is an address in a different /64.
	metricsRateTestIPv6Block = "2001:db8:1:2::%x"
	metricsRateTestOtherIPv6 = "2001:db8:1:3::1"
	// metricsRateTestVisitor and metricsRateTestOtherVisitor name two visitors when
	// the store is used directly; the second one triggers store cleanup.
	metricsRateTestVisitor      = "visitor-a"
	metricsRateTestOtherVisitor = "visitor-b"
)

// metricsAllowN calls Allow n times and returns how many calls were allowed.
func metricsAllowN(t *testing.T, store middleware.RateLimiterStore, identifier string, n int) int {
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

// TestMetricsStreamRateLimiterStoreConfig asserts the configured rate and burst.
// Echo reads Rate as requests per second, so 10 per minute must be 10/60, and an
// idle visitor must be kept at least until its bucket has fully refilled.
func TestMetricsStreamRateLimiterStoreConfig(t *testing.T) {
	t.Parallel()
	cfg := metricsStreamRateLimiterStoreConfig()

	assert.InDelta(t, float64(metricsRateTestBurst)/time.Minute.Seconds(), float64(cfg.Rate), metricsRateTestRateTolerance,
		"rate must be 10 requests per minute expressed per second")
	assert.Equal(t, metricsRateTestBurst, cfg.Burst)
	assert.GreaterOrEqual(t, cfg.ExpiresIn, metricsRateTestFullRefill,
		"idle visitors must not be forgotten before the bucket has fully refilled")
}

// TestMetricsStreamRateLimiterStoreRefill drives the store from the production
// config through a burst and the return of single tokens.
func TestMetricsStreamRateLimiterStoreRefill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := apicore.NewRateLimiterStore(metricsStreamRateLimiterStoreConfig())

		assert.Equal(t, metricsRateTestBurst,
			metricsAllowN(t, store, metricsRateTestVisitor, metricsRateTestBurst+metricsRateTestOverBurst),
			"only the burst may pass at once")

		time.Sleep(metricsRateTestRefillInterval - time.Nanosecond)
		assert.Zero(t, metricsAllowN(t, store, metricsRateTestVisitor, 1), "no token before one interval has passed")

		time.Sleep(time.Nanosecond)
		assert.Equal(t, 1, metricsAllowN(t, store, metricsRateTestVisitor, metricsRateTestBurst),
			"one token per interval")
	})
}

// TestMetricsStreamRateLimiterIdleVisitorExpiry verifies that an idle visitor is
// kept until its bucket has refilled, and that a forgotten visitor starts with a
// full burst and no more. One token short of a full refill, another visitor's
// request reaches the store's cleanup check; an expiry shorter than the refill would
// let that request drop the idle visitor and hand it a full burst instead of the
// tokens it has earned.
func TestMetricsStreamRateLimiterIdleVisitorExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := metricsStreamRateLimiterStoreConfig()
		store := apicore.NewRateLimiterStore(cfg)

		require.Equal(t, metricsRateTestBurst,
			metricsAllowN(t, store, metricsRateTestVisitor, metricsRateTestBurst+metricsRateTestOverBurst))

		time.Sleep(metricsRateTestAlmostRefilled)
		require.Equal(t, 1, metricsAllowN(t, store, metricsRateTestOtherVisitor, 1))
		assert.Equal(t, metricsRateTestBurst-1,
			metricsAllowN(t, store, metricsRateTestVisitor, metricsRateTestBurst+metricsRateTestOverBurst),
			"a visitor idle for less than a full refill has earned all but one token")

		time.Sleep(cfg.ExpiresIn + metricsRateTestIdleMargin)
		require.Equal(t, 1, metricsAllowN(t, store, metricsRateTestOtherVisitor, 1))
		assert.Equal(t, metricsRateTestBurst,
			metricsAllowN(t, store, metricsRateTestVisitor, metricsRateTestBurst+metricsRateTestOverBurst),
			"a forgotten visitor starts with a full burst and no more")
	})
}

// metricsRouteResult sends a request from the given client address and returns
// the response. The request context is already cancelled, so an allowed stream
// sends its connection message and returns instead of blocking in its event loop.
func metricsRouteResult(t *testing.T, e *echo.Echo, path, ip string, port int) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody).WithContext(ctx)
	req.RemoteAddr = net.JoinHostPort(ip, strconv.Itoa(port))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestMetricsStreamRouteRateLimit verifies the registered routes: only the stream
// is limited, a new source port from the same IP is not a new client, other IPs
// are unaffected, and one token comes back per interval. The core is built inside
// the synctest bubble, so the limiter's clock and the ticker of the metrics
// collector started by RegisterMetricsHistoryRoutes are virtual, and the core's
// own cleanup stops the collector before the bubble ends, on failure paths too.
func TestMetricsStreamRouteRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
		core.MetricsStore = observability.NewMemoryStore(MetricsHistoryMaxPoints)
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		h := New(core, nil, nil, nil)
		h.RegisterMetricsHistoryRoutes(core.Group)

		port := metricsRateTestFirstPort
		// request sends one request from a new source port.
		request := func(path, ip string) *httptest.ResponseRecorder {
			port++
			return metricsRouteResult(t, e, path, ip, port)
		}

		for i := range metricsRateTestBurst {
			require.Equal(t, http.StatusOK, request(metricsRateTestStreamPath, metricsRateTestClientIP).Code,
				"request %d is within the burst", i+1)
		}
		denied := request(metricsRateTestStreamPath, metricsRateTestClientIP)
		assert.Equal(t, http.StatusTooManyRequests, denied.Code, "the request after the burst must be refused")
		assert.JSONEq(t, `{"error":"Too many metrics SSE connection attempts, please wait before trying again"}`, denied.Body.String())

		assert.Equal(t, http.StatusOK, request(metricsRateTestStreamPath, metricsRateTestOtherIP).Code,
			"another IP has its own bucket")
		for i := range metricsRateTestBurst + metricsRateTestOverBurst {
			assert.Equal(t, http.StatusOK, request(metricsRateTestHistoryPath, metricsRateTestClientIP).Code,
				"history request %d is not limited, past the stream burst", i+1)
		}

		time.Sleep(metricsRateTestRefillInterval)
		assert.Equal(t, http.StatusOK, request(metricsRateTestStreamPath, metricsRateTestClientIP).Code,
			"one token is back after one interval")
		assert.Equal(t, http.StatusTooManyRequests, request(metricsRateTestStreamPath, metricsRateTestClientIP).Code,
			"only one token came back")
	})
}

// TestMetricsStreamRouteRateLimitIPv6Prefix verifies that IPv6 clients are
// limited per /64: rotating the host part of the address does not start a new
// bucket, and a different /64 has its own. The core is built inside the synctest
// bubble for the same reason as in TestMetricsStreamRouteRateLimit.
func TestMetricsStreamRouteRateLimitIPv6Prefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
		core.MetricsStore = observability.NewMemoryStore(MetricsHistoryMaxPoints)
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		h := New(core, nil, nil, nil)
		h.RegisterMetricsHistoryRoutes(core.Group)

		port := metricsRateTestFirstPort
		// request sends one request from the given address and a new source port.
		request := func(ip string) *httptest.ResponseRecorder {
			port++
			return metricsRouteResult(t, e, metricsRateTestStreamPath, ip, port)
		}
		// rotated returns the n-th address of the simulated client's /64.
		rotated := func(n int) string { return fmt.Sprintf(metricsRateTestIPv6Block, n) }

		for i := range metricsRateTestBurst {
			require.Equal(t, http.StatusOK, request(rotated(i+1)).Code, "request %d is within the burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(rotated(metricsRateTestBurst+1)).Code,
			"a new address in the same /64 shares the exhausted bucket")
		assert.Equal(t, http.StatusOK, request(metricsRateTestOtherIPv6).Code, "another /64 has its own bucket")
	})
}
