// auth_peer_ratelimit_test.go pins the login limit that applies to a connection
// peer as a whole: a proxy or tunnel at one address may spend ten clients' worth
// of attempts per window, however many clients it names in a forwarded header.
// The tests run in a synctest bubble, so the limiter's clock is virtual and no
// test waits in real time.
package authapi

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

const (
	// loginPeerTestMultiplier is how many clients' budgets one connection peer
	// may spend.
	loginPeerTestMultiplier = 10
	// loginPeerTestBurst is the number of attempts one peer may pass in quick
	// succession, whoever the forwarded client is.
	loginPeerTestBurst = loginRateTestBurst * loginPeerTestMultiplier
	// loginPeerTestProxy and loginPeerTestOtherProxy are private addresses a
	// reverse proxy or tunnel connects from; the extractor trusts them without
	// configuration.
	loginPeerTestProxy      = "10.0.0.5"
	loginPeerTestOtherProxy = "10.0.0.6"
	// loginPeerTestClientFormat formats the documentation-range client address a
	// proxy reports in the forwarded header.
	loginPeerTestClientFormat = "203.0.113.%d"
	// loginPeerTestClients is the number of distinct clients one proxy serves in
	// the shared-proxy test.
	loginPeerTestClients = 3
)

// TestLoginPeerRateLimiterStoreConfig asserts the configured rate, burst and
// expiry of the peer limiter: ten clients' worth of attempts per window, with an
// idle peer kept until its bucket has fully refilled.
func TestLoginPeerRateLimiterStoreConfig(t *testing.T) {
	t.Parallel()
	cfg := loginPeerRateLimiterStoreConfig()

	assert.InDelta(t, float64(loginPeerTestBurst)/loginRateTestWindow.Seconds(), float64(cfg.Rate), loginRateTestRateTolerance,
		"rate must be the peer burst per window expressed per second")
	assert.Equal(t, loginPeerTestBurst, cfg.Burst)
	assert.GreaterOrEqual(t, cfg.ExpiresIn, loginRateTestWindow,
		"an idle peer must not be forgotten before its bucket has fully refilled")
}

// proxiedLoginResult posts a rejected login that arrives from the proxy at
// peer and names client in the forwarded client address header.
func proxiedLoginResult(e *echo.Echo, peer string, port int, client string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, loginRateTestLoginPath, strings.NewReader(loginRateTestWrongPayload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(proxytrust.HeaderCFConnectingIP, client)
	req.RemoteAddr = net.JoinHostPort(peer, strconv.Itoa(port))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestLoginRouteRateLimitPeerBudget verifies that a peer which names a new client
// in every request still runs out of attempts: forwarded client addresses are
// only as trustworthy as the peer that sends them, so the peer's own address
// bounds the attempts it can pass. Another peer is unaffected, and the budget
// comes back after the window.
func TestLoginRouteRateLimitPeerBudget(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		h.RegisterRoutes(h.Group)

		port := loginRateTestFirstPort
		// attempt sends one request from the peer, naming the n-th client.
		attempt := func(peer string, n int) *httptest.ResponseRecorder {
			port++
			return proxiedLoginResult(e, peer, port, fmt.Sprintf(loginPeerTestClientFormat, n))
		}

		for i := range loginPeerTestBurst {
			require.Equal(t, http.StatusUnauthorized, attempt(loginPeerTestProxy, i+1).Code,
				"attempt %d names a new client and is within the peer budget", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(loginPeerTestProxy, loginPeerTestBurst+1).Code,
			"the attempt after the peer budget is refused although its client is new")
		assert.Equal(t, http.StatusUnauthorized, attempt(loginPeerTestOtherProxy, 1).Code,
			"another peer has its own budget")

		time.Sleep(loginRateTestWindow)
		assert.Equal(t, http.StatusUnauthorized, attempt(loginPeerTestProxy, loginPeerTestBurst+2).Code,
			"the peer budget is back after the window")
	})
}

// TestLoginRouteRateLimitRefusedClientSparesPeerBudget verifies that attempts the
// per-client limiter refuses do not use up the peer budget: one client behind a
// proxy keeps retrying past the peer budget, and another client behind the same
// proxy still has its own attempts. The per-client limiter must run first for
// that to hold.
func TestLoginRouteRateLimitRefusedClientSparesPeerBudget(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		h.RegisterRoutes(h.Group)

		port := loginRateTestFirstPort
		// attempt sends one request through the proxy for the n-th client.
		attempt := func(n int) *httptest.ResponseRecorder {
			port++
			return proxiedLoginResult(e, loginPeerTestProxy, port, fmt.Sprintf(loginPeerTestClientFormat, n))
		}

		for range loginPeerTestBurst + 1 {
			attempt(1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(1).Code, "the retrying client is out of attempts")
		assert.Equal(t, http.StatusUnauthorized, attempt(2).Code,
			"another client behind the same proxy still has its own attempts")
	})
}

// TestLoginRouteRateLimitClientsBehindProxy verifies that the peer budget leaves
// the per-client limit as it was for clients that share a proxy: each client has
// its own attempts, and the one that runs out is refused alone.
func TestLoginRouteRateLimitClientsBehindProxy(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		h.RegisterRoutes(h.Group)

		port := loginRateTestFirstPort
		// attempt sends one request through the proxy for the n-th client.
		attempt := func(n int) *httptest.ResponseRecorder {
			port++
			return proxiedLoginResult(e, loginPeerTestProxy, port, fmt.Sprintf(loginPeerTestClientFormat, n))
		}

		for client := 1; client <= loginPeerTestClients; client++ {
			for i := range loginRateTestBurst {
				require.Equal(t, http.StatusUnauthorized, attempt(client).Code,
					"client %d attempt %d is within its own burst", client, i+1)
			}
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(1).Code, "the first client is out of attempts")
		assert.Equal(t, http.StatusUnauthorized, attempt(loginPeerTestClients+1).Code,
			"another client behind the same proxy still has its own attempts")
	})
}
