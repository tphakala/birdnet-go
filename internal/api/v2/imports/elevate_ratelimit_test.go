// elevate_ratelimit_test.go pins how the import elevation route limits attempts:
// per client, with IPv6 clients counted per /64, and per connection peer, so a
// peer cannot get fresh attempts by naming a new client in every request.
package importsapi

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

const (
	// elevateRateTestFirstPort is the first source port of the simulated client;
	// each request uses the next one.
	elevateRateTestFirstPort = 40000
	// elevateRateTestIPv6Block formats an address in one documentation-range /64;
	// each request takes the next host part, as a client that rotates addresses
	// would. elevateRateTestOtherIPv6 is an address in a different /64.
	elevateRateTestIPv6Block = "2001:db8:1:2::%x"
	elevateRateTestOtherIPv6 = "2001:db8:1:3::1"

	// elevatePeerTestMultiplier is how many clients' budgets one connection peer
	// may spend. It is a literal, not the production constant, so the test pins
	// the policy independently.
	elevatePeerTestMultiplier = 10
	// elevatePeerTestBurst is the number of attempts one peer may pass in quick
	// succession, whoever the forwarded client is.
	elevatePeerTestBurst = elevateRateLimitBurst * elevatePeerTestMultiplier
	// elevatePeerTestProxy and elevatePeerTestOtherProxy are private addresses a
	// reverse proxy connects from; without configuration the limiter key believes
	// the client addresses they forward while their headers agree.
	elevatePeerTestProxy      = "10.0.0.5"
	elevatePeerTestOtherProxy = "10.0.0.6"
	// elevatePeerTestClientFormat formats the documentation-range client address a
	// proxy reports in the forwarded header (at most 254 distinct clients).
	elevatePeerTestClientFormat = "203.0.113.%d"
	// elevatePeerTestSecondClient is the neighbour client in the shared-proxy test.
	elevatePeerTestSecondClient = 2
	// elevatePeerTestRealClient is the public address a header-appending proxy
	// records as the connecting client, whatever the request claims.
	elevatePeerTestRealClient = "198.51.100.9"
	// elevatePeerTestEdge is a Cloudflare edge address a Cloudflare-fronted request
	// carries as the last X-Forwarded-For hop.
	elevatePeerTestEdge = "173.245.48.10"
	// elevatePeerTestRange is a listed range that holds elevatePeerTestProxy and
	// could hold clients as well.
	elevatePeerTestRange = "10.0.0.0/24"
)

// newProxiedElevateHandler creates a Handler on a full apitest core, so the
// per-client key resolves forwarded client addresses from the core's trusted-proxy
// settings as in production. With no repository an allowed elevation request ends
// at 503 before any side effect.
func newProxiedElevateHandler(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	return e, &Handler{Core: core, importMgr: newImportManager()}
}

// proxiedElevateRequest posts an elevation attempt that arrives from the proxy at
// peer and names client in the forwarded client address header.
func proxiedElevateRequest(e *echo.Echo, peer string, port int, client string) *httptest.ResponseRecorder {
	return elevateRequestWithHeaders(e, peer, port, map[string]string{proxytrust.HeaderCFConnectingIP: client})
}

// TestElevateRouteRateLimitPeerBudget verifies that a peer which names a new
// client in every request still runs out of attempts: a forwarded client address
// is only as trustworthy as the peer that sends it, so the peer's own address
// bounds the attempts it can pass. Another peer is unaffected, and the budget
// comes back after the window.
func TestElevateRouteRateLimitPeerBudget(t *testing.T) {
	e, c := newProxiedElevateHandler(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		c.RegisterImportRoutes(c.Group)

		port := elevateRateTestFirstPort
		// attempt sends one request from the peer, naming the n-th client.
		attempt := func(peer string, n int) *httptest.ResponseRecorder {
			port++
			return proxiedElevateRequest(e, peer, port, fmt.Sprintf(elevatePeerTestClientFormat, n))
		}

		for i := range elevatePeerTestBurst {
			require.NotEqual(t, http.StatusTooManyRequests, attempt(elevatePeerTestProxy, i+1).Code,
				"attempt %d names a new client and is within the peer budget", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(elevatePeerTestProxy, elevatePeerTestBurst+1).Code,
			"the attempt after the peer budget is refused although its client is new")
		assert.NotEqual(t, http.StatusTooManyRequests, attempt(elevatePeerTestOtherProxy, 1).Code,
			"another peer has its own budget")

		time.Sleep(elevateRateLimitWindow)
		for i := range elevatePeerTestBurst {
			require.NotEqual(t, http.StatusTooManyRequests, attempt(elevatePeerTestProxy, i+1).Code,
				"attempt %d after the window is within the refilled peer budget", i+1)
		}
	})
}

// TestElevateRouteRateLimitRefusedClientSparesPeerBudget verifies that attempts
// the per-client limiter refuses do not use up the peer budget: one client behind
// a proxy keeps retrying past the peer budget, and another client behind the same
// proxy still has its own attempts. The per-client limiter must run first for that
// to hold.
func TestElevateRouteRateLimitRefusedClientSparesPeerBudget(t *testing.T) {
	e, c := newProxiedElevateHandler(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		c.RegisterImportRoutes(c.Group)

		port := elevateRateTestFirstPort
		// attempt sends one request through the proxy for the n-th client.
		attempt := func(n int) *httptest.ResponseRecorder {
			port++
			return proxiedElevateRequest(e, elevatePeerTestProxy, port, fmt.Sprintf(elevatePeerTestClientFormat, n))
		}

		for range elevatePeerTestBurst + 1 {
			attempt(1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(1).Code, "the retrying client is out of attempts")
		assert.NotEqual(t, http.StatusTooManyRequests, attempt(elevatePeerTestSecondClient).Code,
			"another client behind the same proxy still has its own attempts")
	})
}

// TestElevateRouteRateLimitIPv6Prefix verifies that the elevation route counts an
// IPv6 client per /64: a new address in the same /64 shares the exhausted bucket,
// and a different /64 has its own. The handler has no datastore here, so an
// allowed request ends at an error before any staging and only a refused one is a
// 429.
func TestElevateRouteRateLimitIPv6Prefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, c := newImportHandler(t)
		c.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		c.RegisterImportRoutes(c.Group)

		port := elevateRateTestFirstPort
		// request sends one request from the given address and a new source port.
		request := func(ip string) *httptest.ResponseRecorder {
			port++
			req := httptest.NewRequest(http.MethodPost, apiV2Prefix+"/import/elevate", http.NoBody)
			req.RemoteAddr = net.JoinHostPort(ip, strconv.Itoa(port))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		for i := range elevateRateLimitBurst {
			assert.NotEqual(t, http.StatusTooManyRequests, request(fmt.Sprintf(elevateRateTestIPv6Block, i+1)).Code,
				"request %d is within the burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(fmt.Sprintf(elevateRateTestIPv6Block, elevateRateLimitBurst+1)).Code,
			"a new address in the same /64 shares the exhausted bucket")
		assert.NotEqual(t, http.StatusTooManyRequests, request(elevateRateTestOtherIPv6).Code,
			"another /64 has its own bucket")
	})
}

// TestElevateRouteRateLimitForgedClientHeaderBehindProxy verifies that a client
// cannot mint a fresh bucket per attempt by forging CF-Connecting-IP behind an
// unlisted proxy that appends the real client to X-Forwarded-For: the forged
// address disagrees with the appended one, so every attempt is charged to the
// proxy's shared unverified bucket and the attempt after its burst is refused.
func TestElevateRouteRateLimitForgedClientHeaderBehindProxy(t *testing.T) {
	e, c := newProxiedElevateHandler(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		c.RegisterImportRoutes(c.Group)

		port := elevateRateTestFirstPort
		// attempt sends one request through the proxy whose X-Forwarded-For names
		// the real client and whose CF-Connecting-IP names the n-th forged one.
		attempt := func(n int) *httptest.ResponseRecorder {
			port++
			req := httptest.NewRequest(http.MethodPost, apiV2Prefix+"/import/elevate", http.NoBody)
			req.Header.Set(echo.HeaderXForwardedFor, elevatePeerTestRealClient)
			req.Header.Set(proxytrust.HeaderCFConnectingIP, fmt.Sprintf(elevatePeerTestClientFormat, n))
			req.RemoteAddr = net.JoinHostPort(elevatePeerTestProxy, strconv.Itoa(port))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		for i := range elevateRateLimitBurst {
			require.NotEqual(t, http.StatusTooManyRequests, attempt(i+1).Code,
				"attempt %d is within the shared bucket's burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, attempt(elevateRateLimitBurst+1).Code,
			"a new forged client address does not buy a fresh bucket")

		// The forged bucket is the proxy's shared one: a request that names only
		// the real client, with nothing forged, has its own bucket.
		port++
		req := httptest.NewRequest(http.MethodPost, apiV2Prefix+"/import/elevate", http.NoBody)
		req.Header.Set(echo.HeaderXForwardedFor, elevatePeerTestRealClient)
		req.RemoteAddr = net.JoinHostPort(elevatePeerTestProxy, strconv.Itoa(port))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusTooManyRequests, rec.Code,
			"the real client behind the proxy is not charged to the forged bucket")
	})
}

// setElevateTrustedProxies publishes a settings snapshot that lists trusted, as a
// hot reload of the trusted-proxy list does.
func setElevateTrustedProxies(c *Handler, trusted ...string) {
	c.Settings.Store(&conf.Settings{Security: conf.Security{TrustedProxies: trusted}})
}

// elevateRequestWithHeaders posts an elevation attempt that arrives from peer
// with the given headers.
func elevateRequestWithHeaders(e *echo.Echo, peer string, port int, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, apiV2Prefix+"/import/elevate", http.NoBody)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.RemoteAddr = net.JoinHostPort(peer, strconv.Itoa(port))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestElevateRouteRateLimitPeerBudgetByTrustedProxies verifies which proxies
// share the peer budget between their clients. A proxy listed by its own address
// vouches for each client it names, so the peer budget does not apply to it; an
// unlisted proxy, a listed range that may hold clients and a request that passed
// a Cloudflare edge keep the budget. Every client is new, so only the peer
// budget can refuse an attempt after the client limiter lets it through.
func TestElevateRouteRateLimitPeerBudgetByTrustedProxies(t *testing.T) {
	cfHeader := func(n int) map[string]string {
		return map[string]string{proxytrust.HeaderCFConnectingIP: fmt.Sprintf(elevatePeerTestClientFormat, n)}
	}
	edgeHeaders := func(n int) map[string]string {
		client := fmt.Sprintf(elevatePeerTestClientFormat, n)
		return map[string]string{
			proxytrust.HeaderCFConnectingIP: client,
			echo.HeaderXForwardedFor:        client + ", " + elevatePeerTestEdge,
		}
	}
	tests := []struct {
		name         string
		trusted      []string
		headers      func(n int) map[string]string
		wantBackstop bool
	}{
		{name: "unlisted proxy", headers: cfHeader, wantBackstop: true},
		{name: "proxy listed by its own address", trusted: []string{elevatePeerTestProxy}, headers: cfHeader},
		{name: "proxy listed as a /32", trusted: []string{elevatePeerTestProxy + "/32"}, headers: cfHeader},
		{name: "proxy inside a listed range", trusted: []string{elevatePeerTestRange}, headers: cfHeader, wantBackstop: true},
		{name: "proxy listed alone and inside a range", trusted: []string{elevatePeerTestRange, elevatePeerTestProxy}, headers: cfHeader, wantBackstop: true},
		{name: "listed proxy behind a Cloudflare edge", trusted: []string{conf.TrustedProxyCloudflarePreset, elevatePeerTestProxy}, headers: edgeHeaders, wantBackstop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, c := newProxiedElevateHandler(t)
			setElevateTrustedProxies(c, tt.trusted...)

			synctest.Test(t, func(t *testing.T) {
				// Register inside the bubble so the limiter's clock is virtual.
				c.RegisterImportRoutes(c.Group)

				port := elevateRateTestFirstPort
				attempt := func(n int) *httptest.ResponseRecorder {
					port++
					return elevateRequestWithHeaders(e, elevatePeerTestProxy, port, tt.headers(n))
				}

				for i := range elevatePeerTestBurst {
					require.NotEqual(t, http.StatusTooManyRequests, attempt(i+1).Code,
						"attempt %d names a new verified or agreeing client and is within the peer budget", i+1)
				}
				got := attempt(elevatePeerTestBurst + 1).Code
				if tt.wantBackstop {
					assert.Equal(t, http.StatusTooManyRequests, got, "the attempt after the peer budget names a new client")
				} else {
					assert.NotEqual(t, http.StatusTooManyRequests, got, "the peer budget does not apply to this proxy")
				}
			})
		})
	}
}

// TestElevateRouteRateLimitPeerBudgetHotReload verifies that listing a spent
// proxy applies to the next request without a restart.
func TestElevateRouteRateLimitPeerBudgetHotReload(t *testing.T) {
	e, c := newProxiedElevateHandler(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		c.RegisterImportRoutes(c.Group)

		port := elevateRateTestFirstPort
		attempt := func(n int) *httptest.ResponseRecorder {
			port++
			return proxiedElevateRequest(e, elevatePeerTestProxy, port, fmt.Sprintf(elevatePeerTestClientFormat, n))
		}

		for i := range elevatePeerTestBurst {
			require.NotEqual(t, http.StatusTooManyRequests, attempt(i+1).Code, "attempt %d is within the peer budget", i+1)
		}
		require.Equal(t, http.StatusTooManyRequests, attempt(elevatePeerTestBurst+1).Code, "the unlisted proxy has spent its budget")

		setElevateTrustedProxies(c, elevatePeerTestProxy)
		assert.NotEqual(t, http.StatusTooManyRequests, attempt(elevatePeerTestBurst+2).Code,
			"a new client of the proxy just listed is no longer charged to the peer budget")
	})
}

// TestElevateRouteRateLimitDirectRequestSkipsPeerBudget verifies that a request
// that names no client is not refused for the traffic of the proxy at its
// address: the client limiter already keys it on the peer.
func TestElevateRouteRateLimitDirectRequestSkipsPeerBudget(t *testing.T) {
	e, c := newProxiedElevateHandler(t)

	synctest.Test(t, func(t *testing.T) {
		// Register inside the bubble so the limiter's clock is virtual.
		c.RegisterImportRoutes(c.Group)

		port := elevateRateTestFirstPort
		for i := range elevatePeerTestBurst {
			port++
			require.NotEqual(t, http.StatusTooManyRequests,
				proxiedElevateRequest(e, elevatePeerTestProxy, port, fmt.Sprintf(elevatePeerTestClientFormat, i+1)).Code)
		}
		port++
		require.Equal(t, http.StatusTooManyRequests,
			proxiedElevateRequest(e, elevatePeerTestProxy, port, fmt.Sprintf(elevatePeerTestClientFormat, elevatePeerTestBurst+1)).Code,
			"the proxy has spent the peer budget")

		for i := range elevateRateLimitBurst {
			port++
			require.NotEqual(t, http.StatusTooManyRequests, elevateRequestWithHeaders(e, elevatePeerTestProxy, port, nil).Code,
				"direct attempt %d is within the client's own burst", i+1)
		}
		port++
		assert.Equal(t, http.StatusTooManyRequests, elevateRequestWithHeaders(e, elevatePeerTestProxy, port, nil).Code,
			"the direct client is still bounded by its own limiter")
	})
}
