// notifications_ratelimit_test.go pins how the notification stream route limits
// connections: per client, with IPv6 clients counted per /64.
package notifications

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

const (
	// notificationRateTestFirstPort is the first source port of the simulated
	// client; each request uses the next one.
	notificationRateTestFirstPort = 40000
	// notificationRateTestIPv6Block formats an address in one documentation-range
	// /64; each request takes the next host part, as a client that rotates
	// addresses would. notificationRateTestOtherIPv6 is an address in a different
	// /64.
	notificationRateTestIPv6Block = "2001:db8:1:2::%x"
	notificationRateTestOtherIPv6 = "2001:db8:1:3::1"
	// notificationRateTestProxyIP is a private address a reverse proxy connects
	// from. notificationRateTestProxiedClient is the public address that proxy
	// appends to X-Forwarded-For, and notificationRateTestForgedFormat formats the
	// documentation-range addresses a client forges in CF-Connecting-IP.
	notificationRateTestProxyIP       = "10.0.0.5"
	notificationRateTestProxiedClient = "198.51.100.9"
	notificationRateTestForgedFormat  = "203.0.113.%d"
	// notificationRateTestStreamPath is the registered stream route.
	notificationRateTestStreamPath = "/api/v2/notifications/stream"
)

// TestNotificationStreamRouteRateLimitIPv6Prefix verifies that the notification
// stream route counts an IPv6 client per /64: a new address in the same /64
// shares the exhausted bucket, and a different /64 has its own. The request
// context is already cancelled, so an allowed stream returns instead of blocking
// in its event loop, and only a refused request is a 429.
func TestNotificationStreamRouteRateLimitIPv6Prefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		service := notification.NewService(notification.DefaultServiceConfig())
		t.Cleanup(service.Stop)
		New(core, service, nil).RegisterRoutes(core.Group)

		port := notificationRateTestFirstPort
		// request sends one request from the given address and a new source port.
		request := func(ip string) *httptest.ResponseRecorder {
			port++
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			req := httptest.NewRequest(http.MethodGet, notificationRateTestStreamPath, http.NoBody).WithContext(ctx)
			req.RemoteAddr = net.JoinHostPort(ip, strconv.Itoa(port))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		for i := range rateLimitBurst {
			assert.NotEqual(t, http.StatusTooManyRequests, request(fmt.Sprintf(notificationRateTestIPv6Block, i+1)).Code,
				"request %d is within the burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(fmt.Sprintf(notificationRateTestIPv6Block, rateLimitBurst+1)).Code,
			"a new address in the same /64 shares the exhausted bucket")
		assert.NotEqual(t, http.StatusTooManyRequests, request(notificationRateTestOtherIPv6).Code,
			"another /64 has its own bucket")
	})
}

// TestNotificationStreamRouteRateLimitForgedClientHeaderBehindProxy verifies that
// a client behind a proxy that appends the real client to X-Forwarded-For cannot
// buy a fresh bucket per connection by forging CF-Connecting-IP: the two headers
// disagree, so every forged request is charged to the proxy's shared unverified
// bucket, while a request that names only the real client has its own.
func TestNotificationStreamRouteRateLimitForgedClientHeaderBehindProxy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
		core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
		service := notification.NewService(notification.DefaultServiceConfig())
		t.Cleanup(service.Stop)
		New(core, service, nil).RegisterRoutes(core.Group)

		port := notificationRateTestFirstPort
		// request sends one request through the proxy. XFF names the real client;
		// a non-empty forged value is sent as CF-Connecting-IP.
		request := func(forged string) *httptest.ResponseRecorder {
			port++
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			req := httptest.NewRequest(http.MethodGet, notificationRateTestStreamPath, http.NoBody).WithContext(ctx)
			req.Header.Set(echo.HeaderXForwardedFor, notificationRateTestProxiedClient)
			if forged != "" {
				req.Header.Set(proxytrust.HeaderCFConnectingIP, forged)
			}
			req.RemoteAddr = net.JoinHostPort(notificationRateTestProxyIP, strconv.Itoa(port))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}

		for i := range rateLimitBurst {
			assert.NotEqual(t, http.StatusTooManyRequests, request(fmt.Sprintf(notificationRateTestForgedFormat, i+1)).Code,
				"request %d is within the shared bucket's burst", i+1)
		}
		assert.Equal(t, http.StatusTooManyRequests, request(fmt.Sprintf(notificationRateTestForgedFormat, rateLimitBurst+1)).Code,
			"a new forged client address does not buy a fresh bucket")
		assert.NotEqual(t, http.StatusTooManyRequests, request("").Code,
			"the real client behind the proxy is not charged to the forged bucket")
	})
}
