// streams_health_ratelimit_test.go pins how the stream health SSE route limits
// connections: per client, with IPv6 clients counted per /64.
package audio

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

const (
	// streamHealthRateTestBurst is the number of connections one client may open in
	// quick succession.
	streamHealthRateTestBurst = 5
	// streamHealthRateTestFirstPort is the first source port of the simulated
	// client; each request uses the next one.
	streamHealthRateTestFirstPort = 40000
	// streamHealthRateTestIPv6Block formats an address in one documentation-range
	// /64; each request takes the next host part, as a client that rotates
	// addresses would. streamHealthRateTestOtherIPv6 is an address in a different
	// /64.
	streamHealthRateTestIPv6Block = "2001:db8:1:2::%x"
	streamHealthRateTestOtherIPv6 = "2001:db8:1:3::1"
	// streamHealthRateTestProxyIP is a private address a reverse proxy connects
	// from. streamHealthRateTestProxiedClient is the public address that proxy
	// appends to X-Forwarded-For, and streamHealthRateTestForgedFormat formats the
	// documentation-range addresses a client forges in CF-Connecting-IP.
	streamHealthRateTestProxyIP       = "10.0.0.5"
	streamHealthRateTestProxiedClient = "198.51.100.9"
	streamHealthRateTestForgedFormat  = "203.0.113.%d"
	// streamHealthRateTestPath is the registered stream route.
	streamHealthRateTestPath = "/api/v2/streams/health/stream"
)

// TestStreamHealthRouteRateLimitIPv6Prefix verifies that the stream health SSE
// route counts an IPv6 client per /64: a new address in the same /64 shares the
// exhausted bucket, and a different /64 has its own. Without an audio engine an
// allowed request ends at 503, which tells it apart from a refused one (429)
// without starting a stream.
func TestStreamHealthRouteRateLimitIPv6Prefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		h := &Handler{Core: core}
		h.RegisterStreamHealthRoutes(core.Group)

		port := streamHealthRateTestFirstPort
		// request sends one request from the given address and a new source port.
		request := func(ip string) *httptest.ResponseRecorder {
			port++
			req := httptest.NewRequest(http.MethodGet, streamHealthRateTestPath, http.NoBody)
			req.RemoteAddr = net.JoinHostPort(ip, strconv.Itoa(port))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		for i := range streamHealthRateTestBurst {
			require.Equal(t, http.StatusServiceUnavailable, request(fmt.Sprintf(streamHealthRateTestIPv6Block, i+1)).Code,
				"request %d is within the burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(fmt.Sprintf(streamHealthRateTestIPv6Block, streamHealthRateTestBurst+1)).Code,
			"a new address in the same /64 shares the exhausted bucket")
		assert.Equal(t, http.StatusServiceUnavailable, request(streamHealthRateTestOtherIPv6).Code,
			"another /64 has its own bucket")
	})
}

// TestStreamHealthRouteRateLimitForgedClientHeaderBehindProxy verifies that a
// client behind a proxy that appends the real client to X-Forwarded-For cannot
// buy a fresh bucket per connection by forging CF-Connecting-IP: the two headers
// disagree, so every forged request is charged to the proxy's shared unverified
// bucket, while a request that names only the real client has its own.
func TestStreamHealthRouteRateLimitForgedClientHeaderBehindProxy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		h := &Handler{Core: core}
		h.RegisterStreamHealthRoutes(core.Group)

		port := streamHealthRateTestFirstPort
		// request sends one request through the proxy. XFF names the real client;
		// a non-empty forged value is sent as CF-Connecting-IP.
		request := func(forged string) *httptest.ResponseRecorder {
			port++
			req := httptest.NewRequest(http.MethodGet, streamHealthRateTestPath, http.NoBody)
			req.Header.Set(echo.HeaderXForwardedFor, streamHealthRateTestProxiedClient)
			if forged != "" {
				req.Header.Set(proxytrust.HeaderCFConnectingIP, forged)
			}
			req.RemoteAddr = net.JoinHostPort(streamHealthRateTestProxyIP, strconv.Itoa(port))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		for i := range streamHealthRateTestBurst {
			require.Equal(t, http.StatusServiceUnavailable, request(fmt.Sprintf(streamHealthRateTestForgedFormat, i+1)).Code,
				"request %d is within the shared bucket's burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(fmt.Sprintf(streamHealthRateTestForgedFormat, streamHealthRateTestBurst+1)).Code,
			"a new forged client address does not buy a fresh bucket")
		assert.Equal(t, http.StatusServiceUnavailable, request("").Code,
			"the real client behind the proxy is not charged to the forged bucket")
	})
}
