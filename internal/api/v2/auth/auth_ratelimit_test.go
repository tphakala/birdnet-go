// auth_ratelimit_test.go pins the login attempt limit: a burst of 5 attempts per
// client IP, refilled at 5 per 15 minutes. The idle expiry and route tests run in
// a synctest bubble, so the limiter's clock is virtual and no test waits in real
// time.
package authapi

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// loginRateTestBurst is the number of attempts one IP may make in quick
	// succession.
	loginRateTestBurst = 5
	// loginRateTestWindow is how long an exhausted bucket takes to refill the
	// loginRateTestBurst attempts it holds.
	loginRateTestWindow = 15 * time.Minute
	// loginRateTestRefillInterval is how long one attempt token takes to come back
	// (5 per 15 minutes is one every 3 minutes).
	loginRateTestRefillInterval = loginRateTestWindow / loginRateTestBurst
	// loginRateTestOverBurst is how many attempts beyond the burst a test makes to
	// show that only the burst passes.
	loginRateTestOverBurst = 5
	// loginRateTestAlmostRefilled is the idle time after which exactly one token of
	// an exhausted bucket is still missing.
	loginRateTestAlmostRefilled = loginRateTestWindow - loginRateTestRefillInterval
	// loginRateTestRateTolerance is the allowed difference between the configured
	// per-second rate and 5 per 15 minutes.
	loginRateTestRateTolerance = 1e-12
	// loginRateTestStoreIdentifier names a visitor when the store is used
	// directly; loginRateTestOtherIdentifier is a second visitor used to trigger
	// store cleanup.
	loginRateTestStoreIdentifier = "visitor-a"
	loginRateTestOtherIdentifier = "visitor-b"
	// loginRateTestFirstPort is the first source port of the simulated client; each
	// request uses the next one, as a browser does for every new connection.
	loginRateTestFirstPort = 40000
	// loginRateTestClientIP and loginRateTestOtherIP are documentation-range
	// addresses.
	loginRateTestClientIP = "192.0.2.10"
	loginRateTestOtherIP  = "192.0.2.20"
	// loginRateTestLoginPath is the registered login route.
	loginRateTestLoginPath = "/api/v2/auth/login"
	// loginRateTestWrongPayload is a login body that the auth service rejects.
	loginRateTestWrongPayload = `{"username":"birdnet-client","password":"wrongpassword"}`
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

// TestLoginRateLimiterStoreConfig asserts the configured rate and burst. Echo
// reads Rate as requests per second, so 5 per 15 minutes must be 5/900, and an
// idle visitor must be kept at least until its bucket has fully refilled.
func TestLoginRateLimiterStoreConfig(t *testing.T) {
	t.Parallel()
	cfg := loginRateLimiterStoreConfig()

	assert.InDelta(t, float64(loginRateTestBurst)/loginRateTestWindow.Seconds(), float64(cfg.Rate), loginRateTestRateTolerance,
		"rate must be 5 attempts per 15 minutes expressed per second")
	assert.Equal(t, loginRateTestBurst, cfg.Burst)
	assert.GreaterOrEqual(t, cfg.ExpiresIn, loginRateTestWindow,
		"idle visitors must not be forgotten before the bucket has fully refilled")
}

// TestLoginRateLimiterIdleVisitorExpiry verifies that an idle visitor is kept
// for the time it has been idle. One token short of a full refill, another
// visitor's request reaches Echo's cleanup check; an expiry shorter than that
// idle time would let the request drop the idle visitor and hand it a full burst
// instead of the tokens it has earned. TestLoginRateLimiterStoreConfig pins the
// full refill time as the lower bound of the expiry.
func TestLoginRateLimiterIdleVisitorExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := loginRateLimiterStoreConfig()
		store := middleware.NewRateLimiterMemoryStoreWithConfig(cfg)

		require.Equal(t, loginRateTestBurst, allowN(t, store, loginRateTestStoreIdentifier, loginRateTestBurst+loginRateTestOverBurst))

		time.Sleep(loginRateTestAlmostRefilled)
		require.Equal(t, 1, allowN(t, store, loginRateTestOtherIdentifier, 1))
		assert.Equal(t, loginRateTestBurst-1, allowN(t, store, loginRateTestStoreIdentifier, loginRateTestBurst+loginRateTestOverBurst),
			"a visitor idle for less than a full refill has earned all but one token")
	})
}

// loginRouteResult posts a rejected login from the given client address and
// returns the response.
func loginRouteResult(e *echo.Echo, ip string, port int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, loginRateTestLoginPath, strings.NewReader(loginRateTestWrongPayload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.RemoteAddr = net.JoinHostPort(ip, strconv.Itoa(port))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestLoginRouteRateLimit verifies the registered login route: only the burst of
// attempts passes, a new source port from the same IP is not a new client, other
// IPs are unaffected, and attempts come back at 5 per 15 minutes rather than 5
// per second.
func TestLoginRouteRateLimit(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		h.RegisterRoutes(h.Group)

		port := loginRateTestFirstPort
		// attempt sends one request from a new source port.
		attempt := func(ip string) *httptest.ResponseRecorder {
			port++
			return loginRouteResult(e, ip, port)
		}

		for i := range loginRateTestBurst {
			require.Equal(t, http.StatusUnauthorized, attempt(loginRateTestClientIP).Code, "attempt %d is within the burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(loginRateTestClientIP).Code, "the attempt after the burst must be refused")

		assert.Equal(t, http.StatusUnauthorized, attempt(loginRateTestOtherIP).Code, "another IP has its own bucket")

		time.Sleep(time.Second)
		assert.Equal(t, http.StatusTooManyRequests, attempt(loginRateTestClientIP).Code, "no token comes back after a second")

		time.Sleep(loginRateTestRefillInterval)
		assert.Equal(t, http.StatusUnauthorized, attempt(loginRateTestClientIP).Code, "one token is back after one interval")
		assert.Equal(t, http.StatusTooManyRequests, attempt(loginRateTestClientIP).Code, "only one token came back")

		time.Sleep(loginRateTestWindow)
		for i := range loginRateTestBurst {
			assert.Equal(t, http.StatusUnauthorized, attempt(loginRateTestClientIP).Code, "burst restored after the window, attempt %d", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(loginRateTestClientIP).Code)
	})
}
