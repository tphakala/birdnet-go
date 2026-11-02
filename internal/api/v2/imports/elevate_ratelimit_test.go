// elevate_ratelimit_test.go pins how the import elevation route limits attempts:
// per client, with IPv6 clients counted per /64.
package importsapi

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
)

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
