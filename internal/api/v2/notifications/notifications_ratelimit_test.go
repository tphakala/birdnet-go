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
